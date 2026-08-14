package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rkparsons/agent-insights/internal/synthesis"
)

type ScoreOptions struct {
	DataDir, CacheDir string
	RecordPath        string // "" → newest record in <cache>/run-records
	Repeats           int    // matcher repeats per (target, sample); default 3
	ClaudeVersion     string
	Matcher           Matcher   // nil → NewClaudeMatcherPinned(pin)
	ScoredAt          time.Time // zero → time.Now().UTC(); injected for verdict-purity tests
	Targets           []string  // ScoreTargets dev loop only: positive rubric ids to score
	MaxSamples        int       // ScoreTargets dev loop only: 0 = all record samples
}

type ScoreArtifacts struct {
	CardsDir string
	RunsPath string
}

func LoadRunRecord(path string) (RunRecord, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return RunRecord{}, err
	}
	var rec RunRecord
	if err := json.Unmarshal(raw, &rec); err != nil {
		return RunRecord{}, fmt.Errorf("run record %s: %w", path, err)
	}
	return rec, nil
}

// LatestRunRecord picks the newest record by RanAt — file names alone do not
// order (the nano-suffix has variable width).
func LatestRunRecord(cacheDir string) (RunRecord, string, error) {
	dir := filepath.Join(cacheDir, "run-records")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return RunRecord{}, "", fmt.Errorf("no run records — run `insights eval outcome` first: %w", err)
	}
	var best RunRecord
	bestPath := ""
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		rec, err := LoadRunRecord(filepath.Join(dir, e.Name()))
		if err != nil {
			return RunRecord{}, "", err
		}
		if bestPath == "" || rec.RanAt.After(best.RanAt) {
			best, bestPath = rec, filepath.Join(dir, e.Name())
		}
	}
	if bestPath == "" {
		return RunRecord{}, "", errors.New("no run records — run `insights eval outcome` first")
	}
	return best, bestPath, nil
}

// validateStatusCoverage fail-closes on any scored rubric without a status —
// an unseeded target must never silently default at scoring time.
func validateStatusCoverage(rubrics []Rubric, statuses map[string]string) error {
	var missing, invalid []string
	for _, r := range rubrics {
		if r.Part == "negative" {
			continue
		}
		s, ok := statuses[r.ID]
		if !ok {
			missing = append(missing, r.ID)
			continue
		}
		if !validStatuses[s] {
			invalid = append(invalid, r.ID+"="+s)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("statuses missing for %v — run `insights eval statuses seed`", missing)
	}
	if len(invalid) > 0 {
		return fmt.Errorf("invalid status values %v — fix benchmark.json (valid: must_pass, expected_fail, expected_partial, needs_reconfirmation, invalidated)", invalid)
	}
	return nil
}

// loadScoreableRecord resolves the record (explicit path or newest) and fails
// closed on non-scoreable shapes: an --l1-sample run writes a SUCCESSFUL
// record with zero buckets, which would otherwise score as a vacuous PASS,
// land in runs/, and poison later delta baselines; a run whose every global
// sample failed exits outcome with 0, so the fail-closed duty lives here.
func loadScoreableRecord(opts ScoreOptions) (RunRecord, string, error) {
	var rec RunRecord
	recPath := opts.RecordPath
	var err error
	if recPath != "" {
		rec, err = LoadRunRecord(recPath)
	} else {
		rec, recPath, err = LatestRunRecord(opts.CacheDir)
	}
	if err != nil {
		return RunRecord{}, "", err
	}
	if len(rec.Buckets) == 0 || rec.L1Sample != nil {
		return RunRecord{}, "", fmt.Errorf("record %s has no scoreable buckets (l1-sample or empty record) — run a full `insights eval outcome` first", recPath)
	}
	if len(rec.SampleOutputs) == 0 {
		return RunRecord{}, "", fmt.Errorf("record %s has zero global samples — scoring fails closed, never vacuously passes; re-run `insights eval outcome`", recPath)
	}
	return rec, recPath, nil
}

// scoreSession is the shared setup behind ScoreRun and the ScoreTargets dev
// loop — one loading/validation/probe path so the dev loop always predicts
// the committed sweep. Creation is fail-closed before any target spend.
type scoreSession struct {
	rec        RunRecord
	recPath    string
	rubrics    []Rubric
	statuses   map[string]string
	watermarks map[string]int
	adj        map[string]Adjudication
	prior      []namedVerdict
	ever       map[string]bool
	cache      *Cache
	envHash    string
	m          Matcher
	probes     []ProbeResult
	// items is the sample's whole global card set — findings and dropped
	// entries from every repo at once, keyed by sample index.
	items      map[int][]ScoredItem
	population []string          // union of every bucket's population (anchor denominator)
	oneLines   map[string]string // session id → pool one-line, across every bucket
	truths     map[string]RepoSynthesis
	repeats    int
	warnings   []string
	// hardErrors is the pre-spend refusal channel. Task 9: v1 filled it from
	// Finalize's ValidationReport; under v2 a hard verifier failure produces no
	// sample at all (outcome warns and drops it), so the equivalent signal is
	// meta.validation_notes plus the dropped-sample count — wired in with the
	// probe recast. The gate below stays so that wiring is a one-line change.
	hardErrors []string
}

func newScoreSession(ctx context.Context, opts ScoreOptions, scratchStamp time.Time) (*scoreSession, func(), error) {
	s := &scoreSession{repeats: opts.Repeats}
	var err error
	if s.rec, s.recPath, err = loadScoreableRecord(opts); err != nil {
		return nil, nil, err
	}
	if s.rubrics, err = LoadRubrics(opts.DataDir); err != nil {
		return nil, nil, err
	}
	if s.statuses, err = Statuses(opts.DataDir); err != nil {
		return nil, nil, err
	}
	if err = validateStatusCoverage(s.rubrics, s.statuses); err != nil {
		return nil, nil, err
	}
	if s.watermarks, err = NuanceWatermarks(opts.DataDir); err != nil {
		return nil, nil, err
	}
	if s.adj, err = LoadAdjudications(opts.DataDir); err != nil {
		return nil, nil, err
	}
	if s.prior, err = LoadCommittedVerdicts(opts.DataDir); err != nil {
		return nil, nil, err
	}
	s.ever = everPassedTargets(s.prior, s.adj)
	s.cache = NewCache(opts.CacheDir)

	// Cached material loads before any matcher spend so pre-spend gates (hard
	// synthesis errors) fire without costing a probe read.
	if err = s.loadSamples(); err != nil {
		return nil, nil, err
	}
	if len(s.hardErrors) > 0 {
		return nil, nil, fmt.Errorf("record %s carries synthesis hard errors (%s) — scoring refused before any matcher spend; fix the pipeline and re-run `insights eval outcome`",
			filepath.Base(s.recPath), strings.Join(s.hardErrors, "; "))
	}
	if s.rec.Population == "as_consumed" {
		// the run-0 control scores against PRE-strip anchors from ground-truth/
		if s.truths, err = loadGroundTruth(filepath.Join(opts.DataDir, "ground-truth")); err != nil {
			return nil, nil, err
		}
	}

	// Same scratch discipline as RunOutcome: stale credential copies must
	// never outlive their run. No skill overlay — the matcher is not a skill;
	// the EnvHash formula (claude version + snapshot hash) matches the
	// record's, so CLI/config drift between outcome and score is detectable.
	scratchRoot := filepath.Join(opts.CacheDir, "scratch")
	if err := os.RemoveAll(scratchRoot); err != nil {
		return nil, nil, err
	}
	cleanup := func() { os.RemoveAll(scratchRoot) }
	scratch := filepath.Join(scratchRoot, strconv.FormatInt(scratchStamp.UnixNano(), 10))
	pin, err := ComposeEnvPin(opts.DataDir, scratch, map[string]string{}, opts.ClaudeVersion)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	s.envHash = pin.EnvHash
	if pin.EnvHash != s.rec.EnvHash {
		s.warnings = append(s.warnings, fmt.Sprintf("matcher env %s differs from the record's pipeline env %s (claude updated between outcome and score?)", pin.EnvHash, s.rec.EnvHash))
	}
	s.m = opts.Matcher
	if s.m == nil {
		s.m = NewClaudeMatcherPinned(pin.ConfigDir, pin.WorkDir)
	}

	// Probes first, fail-closed: a failed majority invalidates the scoring
	// stage before any target spend.
	if s.probes, err = RunProbes(ctx, s.cache, s.m, s.envHash, s.rubrics, s.repeats); err != nil {
		cleanup()
		return nil, nil, err
	}
	for _, p := range s.probes {
		if !p.Pass {
			cleanup()
			return nil, nil, fmt.Errorf("matcher integrity probe %s FAILED (majority %s over %v) — scoring invalidated, recalibrate first", p.Class, p.Majority, p.Granularities)
		}
	}
	return s, cleanup, nil
}

// loadSamples reads the record's cached material: every bucket's bundle (for
// the id→session index and the card one-lines) and every global sample's
// verified snapshot, carded into one item set per sample.
func (s *scoreSession) loadSamples() error {
	s.items = map[int][]ScoredItem{}
	s.oneLines = map[string]string{}
	bundles := map[string]synthesis.EvidenceBundle{}
	var population []string
	for _, b := range s.rec.Buckets {
		var bundle synthesis.EvidenceBundle
		hit, err := s.cache.Get("bundle", b.BundleKey, &bundle)
		if err != nil {
			return err
		}
		if !hit {
			return fmt.Errorf("bucket %s: bundle missing from cache — re-run `insights eval outcome`", b.Bucket)
		}
		bundles[b.Bucket] = bundle
		population = append(population, b.Population...)
		// Session ids are unique across repos, so the one-line vocabulary is a
		// single global map — a card no longer knows which bucket it came from.
		for id, line := range sessionOneLines(bundle) {
			s.oneLines[id] = line
		}
	}
	s.population = sortedSet(population)
	for _, smp := range s.rec.SampleOutputs {
		var vo VerifiedOutput
		hit, err := s.cache.Get("verify", smp.VerifiedKey, &vo)
		if err != nil {
			return err
		}
		if !hit {
			return fmt.Errorf("sample %d: verified output missing from cache — re-run `insights eval outcome`", smp.SampleIndex)
		}
		s.items[smp.SampleIndex] = BuildGlobalScoredItems(vo.Snapshot, bundles)
	}
	return nil
}

// scoreTarget scores one positive rubric over the record's samples
// (maxSamples > 0 keeps only the first ones — dev loop) and aggregates its
// verdict. Returns the effective anchors for card building.
func (s *scoreSession) scoreTarget(ctx context.Context, r Rubric, maxSamples int) (TargetResult, []string, error) {
	var preStrip []string
	var err error
	if s.truths != nil && r.AnchorTheme != "" {
		if preStrip, err = PreStripAnchors(s.truths, r); err != nil {
			return TargetResult{}, nil, err
		}
	}
	anchors, capAnchors := AnchorSets(r, s.population, preStrip)
	if len(r.AnchorSessionIDs) > 0 && len(anchors) == 0 {
		s.warnings = append(s.warnings, fmt.Sprintf("%s: no effective anchors in the active population — corroboration degraded to no-anchor", r.ID))
	}
	sampleOuts := s.rec.SampleOutputs
	if maxSamples > 0 && maxSamples < len(sampleOuts) {
		sampleOuts = sampleOuts[:maxSamples]
	}
	var samples []SampleScore
	for _, so := range sampleOuts {
		ss, err := scoreTargetSample(ctx, s.cache, s.m, s.envHash, r, s.items[so.SampleIndex], anchors, capAnchors, s.adj, so.SampleIndex, s.repeats)
		if err != nil {
			return TargetResult{}, nil, err
		}
		samples = append(samples, ss)
	}
	tv, cards := AggregateTarget(r, s.statuses[r.ID], samples, len(anchors), s.adj, s.ever[r.ID])
	return TargetResult{Rubric: r, Verdict: tv, Samples: samples, Pending: cards}, anchors, nil
}

// ScoreRun scores one run record end to end: probes (fail-closed), per-target
// matcher scoring over cached verified outputs, aggregation, cards, verdict
// composition, delta, persistence. A pure function of its inputs — re-running
// with an unchanged cache and unchanged data-repo state reproduces the same
// verdict (modulo ScoredAt).
func ScoreRun(ctx context.Context, opts ScoreOptions) (Verdict, ScoreArtifacts, error) {
	none := ScoreArtifacts{}
	if opts.Repeats <= 0 {
		opts.Repeats = 3
	}
	if opts.ScoredAt.IsZero() {
		opts.ScoredAt = time.Now().UTC()
	}
	rubricSetHash, err := RubricSetHash(opts.DataDir)
	if err != nil {
		return Verdict{}, none, err
	}
	s, cleanup, err := newScoreSession(ctx, opts, opts.ScoredAt)
	if err != nil {
		return Verdict{}, none, err
	}
	defer cleanup()

	var results []TargetResult
	var invalidated []string
	anchorsByTarget := map[string][]string{}
	for _, r := range s.rubrics {
		if r.Part == "negative" {
			continue
		}
		if s.statuses[r.ID] == "invalidated" {
			invalidated = append(invalidated, r.ID)
			continue
		}
		res, anchors, err := s.scoreTarget(ctx, r, 0)
		if err != nil {
			return Verdict{}, none, err
		}
		anchorsByTarget[r.ID] = anchors
		results = append(results, res)
	}

	var negatives []NegativeViolation
	idxs := make([]int, 0, len(s.items))
	for i := range s.items {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)
	for _, r := range s.rubrics {
		if r.Part != "negative" {
			continue
		}
		var vioIdx []int
		var refs []string
		for _, idx := range idxs {
			violated, matched, err := scoreNegativeSample(ctx, s.cache, s.m, s.envHash, r, s.items[idx], opts.Repeats)
			if err != nil {
				return Verdict{}, none, err
			}
			if violated {
				vioIdx = append(vioIdx, idx)
				refs = append(refs, matched...)
			}
		}
		if len(vioIdx) > 0 {
			negatives = append(negatives, NegativeViolation{RubricID: r.ID, SampleIndexes: vioIdx, ItemRefs: sortedSet(refs)})
		}
	}

	v, extra, err := ComposeVerdict(VerdictInputs{Record: s.rec, RecordName: s.recPath,
		ScoredAt: opts.ScoredAt, RubricSetHash: rubricSetHash, MatcherEnvHash: s.envHash,
		Results: results, Negatives: negatives, Probes: s.probes,
		Invalidated: invalidated, Warnings: s.warnings, Adj: s.adj, Prior: s.prior,
		Watermarks: s.watermarks}, s.cache)
	if err != nil {
		return Verdict{}, none, err
	}
	byID := map[string]int{}
	for i := range results {
		byID[results[i].Rubric.ID] = i
	}
	for _, c := range extra {
		if i, ok := byID[c.TargetID]; ok {
			results[i].Pending = append(results[i].Pending, c)
		}
	}
	cards, err := BuildCards(results, anchorsByTarget, s.oneLines)
	if err != nil {
		return Verdict{}, none, err
	}
	v.CardCount = len(cards)
	cardsDir, err := WriteCards(opts.CacheDir, opts.ScoredAt.Format("2006-01-02T15-04-05Z"), cards)
	if err != nil {
		return Verdict{}, none, err
	}
	runsPath, err := PersistVerdict(opts.DataDir, opts.CacheDir, v)
	if err != nil {
		return v, ScoreArtifacts{CardsDir: cardsDir}, err
	}
	return v, ScoreArtifacts{CardsDir: cardsDir, RunsPath: runsPath}, nil
}

// ScoreTargets is the dev loop: score only opts.Targets against the record
// and return the per-target results — no verdict composition, no cards, no
// delta, and NOTHING persisted beyond the content-addressed match cache
// (runs/ and verdicts/ are never written). MaxSamples > 0 limits samples per
// target for cheaper reads. The same session setup as ScoreRun (fail-closed
// record checks, status validation, probes) keeps its results predictive of
// the committed sweep.
func ScoreTargets(ctx context.Context, opts ScoreOptions) ([]TargetResult, []string, error) {
	if len(opts.Targets) == 0 {
		return nil, nil, errors.New("no targets requested")
	}
	if opts.Repeats <= 0 {
		opts.Repeats = 3
	}
	// Id validation precedes the session: a typo'd target must be named
	// before any cache loading or probe spend.
	rubrics, err := LoadRubrics(opts.DataDir)
	if err != nil {
		return nil, nil, err
	}
	byID := map[string]Rubric{}
	for _, r := range rubrics {
		if r.Part != "negative" {
			byID[r.ID] = r
		}
	}
	var unknown []string
	for _, id := range opts.Targets {
		if _, ok := byID[id]; !ok {
			unknown = append(unknown, id)
		}
	}
	if len(unknown) > 0 {
		return nil, nil, fmt.Errorf("unknown target id(s) %v — positive rubric ids only (negative rubrics have no per-target dev loop)", unknown)
	}

	s, cleanup, err := newScoreSession(ctx, opts, time.Now().UTC())
	if err != nil {
		return nil, nil, err
	}
	defer cleanup()

	var results []TargetResult
	for _, id := range opts.Targets {
		if s.statuses[id] == "invalidated" {
			s.warnings = append(s.warnings, id+": invalidated — skipped")
			continue
		}
		res, _, err := s.scoreTarget(ctx, byID[id], opts.MaxSamples)
		if err != nil {
			return nil, nil, err
		}
		results = append(results, res)
	}
	return results, s.warnings, nil
}

// ProbeRun runs only the integrity probes — the calibration entry point.
func ProbeRun(ctx context.Context, opts ScoreOptions) ([]ProbeResult, error) {
	if opts.Repeats <= 0 {
		opts.Repeats = 3
	}
	rubrics, err := LoadRubrics(opts.DataDir)
	if err != nil {
		return nil, err
	}
	scratchRoot := filepath.Join(opts.CacheDir, "scratch")
	if err := os.RemoveAll(scratchRoot); err != nil {
		return nil, err
	}
	scratch := filepath.Join(scratchRoot, strconv.FormatInt(time.Now().UnixNano(), 10))
	defer os.RemoveAll(scratchRoot)
	pin, err := ComposeEnvPin(opts.DataDir, scratch, map[string]string{}, opts.ClaudeVersion)
	if err != nil {
		return nil, err
	}
	m := opts.Matcher
	if m == nil {
		m = NewClaudeMatcherPinned(pin.ConfigDir, pin.WorkDir)
	}
	return RunProbes(ctx, NewCache(opts.CacheDir), m, pin.EnvHash, rubrics, opts.Repeats)
}

// FindCardByPrefix locates one adjudicable card by key-hash prefix across all
// locally cached card dirs.
func FindCardByPrefix(cacheDir, prefix string) (Card, error) {
	paths, err := filepath.Glob(filepath.Join(cacheDir, "cards", "*", "card-*.json"))
	if err != nil {
		return Card{}, err
	}
	var found []Card
	seen := map[string]bool{}
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return Card{}, err
		}
		var c Card
		if err := json.Unmarshal(raw, &c); err != nil {
			return Card{}, fmt.Errorf("%s: %w", p, err)
		}
		if strings.HasPrefix(c.KeyHash, prefix) && !seen[c.KeyHash] {
			seen[c.KeyHash] = true
			found = append(found, c)
		}
	}
	if len(found) == 0 {
		return Card{}, fmt.Errorf("no card with key prefix %q", prefix)
	}
	if len(found) > 1 {
		return Card{}, fmt.Errorf("key prefix %q is ambiguous (%d cards) — use more characters", prefix, len(found))
	}
	if !found[0].Adjudicable {
		return Card{}, fmt.Errorf("card %s (%s/%s) is informational — not adjudicable", prefix, found[0].TargetID, found[0].Trigger)
	}
	return found[0], nil
}
