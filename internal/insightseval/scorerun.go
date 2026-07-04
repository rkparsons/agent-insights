package insightseval

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

	"tmux-ctrl/internal/synthesis"
)

type ScoreOptions struct {
	DataDir, CacheDir string
	RecordPath        string // "" → newest record in <cache>/run-records
	Repeats           int    // matcher repeats per (target, sample); default 3
	ClaudeVersion     string
	Matcher           Matcher   // nil → NewClaudeMatcherPinned(pin)
	ScoredAt          time.Time // zero → time.Now().UTC(); injected for verdict-purity tests
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

// bucketData is one bucket's scoring material: per-sample items + one-lines.
type bucketData struct {
	outputs  BucketOutputs
	items    map[int][]ScoredItem
	oneLines map[string]string
}

// itemsForSample concatenates the named buckets' items at one sample index
// (a bucket missing that index contributes nothing).
func itemsForSample(buckets map[string]bucketData, repos []string, sampleIndex int) []ScoredItem {
	var out []ScoredItem
	for _, b := range repos {
		if bd, ok := buckets[b]; ok {
			out = append(out, bd.items[sampleIndex]...)
		}
	}
	return out
}

func sortedBucketNames(buckets map[string]bucketData) []string {
	names := make([]string, 0, len(buckets))
	for n := range buckets {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
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
	var rec RunRecord
	recPath := opts.RecordPath
	var err error
	if recPath != "" {
		rec, err = LoadRunRecord(recPath)
	} else {
		rec, recPath, err = LatestRunRecord(opts.CacheDir)
	}
	if err != nil {
		return Verdict{}, none, err
	}
	// Fail closed on non-scoreable records: an --l1-sample run writes a
	// SUCCESSFUL record with zero buckets, which would otherwise score as a
	// vacuous PASS, land in runs/, and poison later delta baselines.
	if len(rec.Buckets) == 0 || rec.L1Sample != nil {
		return Verdict{}, none, fmt.Errorf("record %s has no scoreable buckets (l1-sample or empty record) — run a full `insights eval outcome` first", recPath)
	}
	for _, b := range rec.Buckets {
		if len(b.Samples) == 0 {
			// phase-2 seam contract: outcome exits 0 on a skipped-samples
			// bucket; the fail-closed duty lives here
			return Verdict{}, none, fmt.Errorf("bucket %s has zero samples — scoring fails closed, never vacuously passes", b.Bucket)
		}
	}

	rubrics, err := LoadRubrics()
	if err != nil {
		return Verdict{}, none, err
	}
	rubricSetHash, err := RubricSetHash()
	if err != nil {
		return Verdict{}, none, err
	}
	statuses, err := Statuses(opts.DataDir)
	if err != nil {
		return Verdict{}, none, err
	}
	if err := validateStatusCoverage(rubrics, statuses); err != nil {
		return Verdict{}, none, err
	}
	adj, err := LoadAdjudications(opts.DataDir)
	if err != nil {
		return Verdict{}, none, err
	}
	prior, err := LoadCommittedVerdicts(opts.DataDir)
	if err != nil {
		return Verdict{}, none, err
	}
	ever := everPassedTargets(prior, adj)
	cache := NewCache(opts.CacheDir)

	// Same scratch discipline as RunOutcome: stale credential copies must
	// never outlive their run. No skill overlay — the matcher is not a skill;
	// the EnvHash formula (claude version + snapshot hash) matches the
	// record's, so CLI/config drift between outcome and score is detectable.
	scratchRoot := filepath.Join(opts.CacheDir, "scratch")
	if err := os.RemoveAll(scratchRoot); err != nil {
		return Verdict{}, none, err
	}
	scratch := filepath.Join(scratchRoot, strconv.FormatInt(opts.ScoredAt.UnixNano(), 10))
	defer os.RemoveAll(scratchRoot)
	pin, err := ComposeEnvPin(opts.DataDir, scratch, map[string]string{}, opts.ClaudeVersion)
	if err != nil {
		return Verdict{}, none, err
	}
	var warnings []string
	if pin.EnvHash != rec.EnvHash {
		warnings = append(warnings, fmt.Sprintf("matcher env %s differs from the record's pipeline env %s (claude updated between outcome and score?)", pin.EnvHash, rec.EnvHash))
	}
	m := opts.Matcher
	if m == nil {
		m = NewClaudeMatcherPinned(pin.ConfigDir, pin.WorkDir)
	}

	// Probes first, fail-closed: a failed majority invalidates the scoring
	// stage before any target spend.
	probes, err := RunProbes(ctx, cache, m, pin.EnvHash, rubrics, opts.Repeats)
	if err != nil {
		return Verdict{}, none, err
	}
	for _, p := range probes {
		if !p.Pass {
			return Verdict{}, none, fmt.Errorf("matcher integrity probe %s FAILED (majority %s over %v) — scoring invalidated, recalibrate first", p.Class, p.Majority, p.Granularities)
		}
	}

	buckets := map[string]bucketData{}
	oneLines := map[string]map[string]string{}
	for _, b := range rec.Buckets {
		var bundle synthesis.EvidenceBundle
		hit, err := cache.Get("bundle", b.BundleKey, &bundle)
		if err != nil {
			return Verdict{}, none, err
		}
		if !hit {
			return Verdict{}, none, fmt.Errorf("bucket %s: bundle missing from cache — re-run `insights eval outcome`", b.Bucket)
		}
		bd := bucketData{outputs: b, items: map[int][]ScoredItem{}, oneLines: sessionOneLines(bundle)}
		for _, s := range b.Samples {
			var vo VerifiedOutput
			hit, err := cache.Get("verify", s.VerifiedKey, &vo)
			if err != nil {
				return Verdict{}, none, err
			}
			if !hit {
				return Verdict{}, none, fmt.Errorf("bucket %s sample %d: verified output missing from cache — re-run `insights eval outcome`", b.Bucket, s.SampleIndex)
			}
			bd.items[s.SampleIndex] = BuildScoredItems(b.Bucket, vo, bundle)
		}
		buckets[b.Bucket] = bd
		oneLines[b.Bucket] = bd.oneLines
	}

	var truths map[string]synthesis.RepoSynthesis
	if rec.Population == "as_consumed" {
		// the run-0 control scores against PRE-strip anchors from ground-truth/
		truths, err = loadGroundTruth(filepath.Join(opts.DataDir, "ground-truth"))
		if err != nil {
			return Verdict{}, none, err
		}
	}

	var results []TargetResult
	var invalidated []string
	anchorsByTarget := map[string][]string{}
	for _, r := range rubrics {
		if r.Part == "negative" {
			continue
		}
		status := statuses[r.ID]
		if status == "invalidated" {
			invalidated = append(invalidated, r.ID)
			continue
		}
		bd, haveBucket := buckets[r.Repos[0]]
		if !haveBucket {
			warnings = append(warnings, fmt.Sprintf("%s: expected bucket %s not in record — scored absent", r.ID, r.Repos[0]))
		}
		var preStrip []string
		if truths != nil && r.AnchorTheme != "" {
			preStrip, err = PreStripAnchors(truths, r)
			if err != nil {
				return Verdict{}, none, err
			}
		}
		anchors := EffectiveAnchors(r, bd.outputs.Population, preStrip)
		if len(r.AnchorSessionIDs) > 0 && len(anchors) == 0 {
			warnings = append(warnings, fmt.Sprintf("%s: no effective anchors in the active population — corroboration degraded to no-anchor", r.ID))
		}
		anchorsByTarget[r.ID] = anchors
		var samples []SampleScore
		for _, s := range bd.outputs.Samples {
			items := itemsForSample(buckets, r.Repos, s.SampleIndex)
			ss, err := scoreTargetSample(ctx, cache, m, pin.EnvHash, r, items, anchors, adj, s.SampleIndex, opts.Repeats)
			if err != nil {
				return Verdict{}, none, err
			}
			samples = append(samples, ss)
		}
		tv, cards := AggregateTarget(r, status, samples, len(anchors), adj, ever[r.ID])
		results = append(results, TargetResult{Rubric: r, Verdict: tv, Samples: samples, Pending: cards})
	}

	var negatives []NegativeViolation
	allBuckets := sortedBucketNames(buckets)
	for _, r := range rubrics {
		if r.Part != "negative" {
			continue
		}
		idxSet := map[int]bool{}
		for _, bd := range buckets {
			for idx := range bd.items {
				idxSet[idx] = true
			}
		}
		idxs := make([]int, 0, len(idxSet))
		for i := range idxSet {
			idxs = append(idxs, i)
		}
		sort.Ints(idxs)
		var vioIdx []int
		var refs []string
		for _, idx := range idxs {
			violated, matched, err := scoreNegativeSample(ctx, cache, m, pin.EnvHash, r, itemsForSample(buckets, allBuckets, idx), opts.Repeats)
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

	v, extra, err := ComposeVerdict(VerdictInputs{Record: rec, RecordName: recPath,
		ScoredAt: opts.ScoredAt, RubricSetHash: rubricSetHash, MatcherEnvHash: pin.EnvHash,
		Results: results, Negatives: negatives, Probes: probes,
		Invalidated: invalidated, Warnings: warnings, Adj: adj, Prior: prior}, cache)
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
	cards, err := BuildCards(results, anchorsByTarget, oneLines)
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

// ProbeRun runs only the integrity probes — the calibration entry point.
func ProbeRun(ctx context.Context, opts ScoreOptions) ([]ProbeResult, error) {
	if opts.Repeats <= 0 {
		opts.Repeats = 3
	}
	rubrics, err := LoadRubrics()
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
