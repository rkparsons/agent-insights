package insightseval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"tmux-ctrl/internal/insights"
	"tmux-ctrl/internal/synthesis"
	"tmux-ctrl/internal/transcript"
	"tmux-ctrl/skills"
)

const consecutiveLLMFailureLimit = 3

// l2SynthesisTimeout bounds one L2 synthesis subprocess. v4-era syntheses run
// 8–14 minutes; a kill at the deadline discards the output but not the spend,
// so the bound errs generous.
const l2SynthesisTimeout = 20 * time.Minute

type OutcomeOptions struct {
	DataDir, CacheDir string
	Scope             string                // "l2" (default) | "full"
	Population        string                // "scoring" (default) | "as_consumed"
	Samples           int                   // default 3
	L1Sample          bool                  // Task 11
	PoolVersion       string                // default "v1"
	ClaudeVersion     string                // injected; CLI fills via claudeVersionString()
	SkillDirs         map[string]string     // nil → defaultSkillDirs()
	Judge             insights.Judge        // nil → NewClaudeJudgePinned(pin) — Task 11/12
	Synth             synthesis.Synthesizer // nil → NewClaudeSynthesizerPinned(pin) — Task 12
}

type SampleOutput struct {
	SampleIndex int    `json:"sample_index"`
	Fresh       bool   `json:"fresh"` // this L2 sample was a cache miss (churn uses fresh only)
	RawKey      string `json:"raw_key"`
	VerifiedKey string `json:"verified_key"`
}

type BucketOutputs struct {
	Bucket        string         `json:"bucket"`
	Population    []string       `json:"population"`
	GapFallbacks  []string       `json:"gap_fallbacks,omitempty"`
	PoolSliceHash string         `json:"pool_slice_hash"` // provenance: which pool content fed this bucket
	BundleKey     string         `json:"bundle_key"`      // cache key — the scoring plan fetches the bundle for id→session mapping
	BundleHash    string         `json:"bundle_hash"`
	Samples       []SampleOutput `json:"samples"`
}

type L1SampleResult struct { // populated by Task 11
	Cells    map[string]string `json:"cells"`
	Analyzed int               `json:"analyzed"`
	Hits     int               `json:"cache_hits"`
	Misses   int               `json:"cache_misses"`
}

type RunRecord struct { // the spec's reproducibility record
	RanAt              time.Time         `json:"ran_at"`
	Scope              string            `json:"scope"`
	Population         string            `json:"population"`
	PoolVersion        string            `json:"pool_version"`
	Samples            int               `json:"samples"`
	ManifestHash       string            `json:"manifest_hash"`
	BenchmarkHash      string            `json:"benchmark_hash"`
	RubricSetHash      string            `json:"rubric_set_hash"`
	SchemaHashes       map[string]string `json:"schema_hashes"` // "l1", "l2"
	Models             map[string]string `json:"models"`        // "l1", "l2"
	ClaudeVersion      string            `json:"claude_version"`
	EnvHash            string            `json:"env_hash"`
	ConfigSnapshotHash string            `json:"config_snapshot_hash"` // whole tree (verify-stage key)
	SkillHashes        map[string]string `json:"skill_hashes"`
	CodeVersions       map[string]string `json:"code_versions"` // "facts", "synthesis", "insightseval"
	Buckets            []BucketOutputs   `json:"buckets"`
	L1Sample           *L1SampleResult   `json:"l1_sample,omitempty"`
	CacheHits          int               `json:"cache_hits"`
	CacheMisses        int               `json:"cache_misses"`
	Warnings           []string          `json:"warnings,omitempty"`
	RecordPath         string            `json:"-"`
}

type VerifiedOutput struct {
	Synthesis synthesis.RepoSynthesis    `json:"synthesis"`
	Raw       synthesis.RawSynthesis     `json:"raw"` // spec enabler 1: RawSynthesis next to RepoSynthesis
	Report    synthesis.ValidationReport `json:"report"`
}

// defaultSkillDirs materializes the in-repo skills package to a scratch dir and
// points at those copies — the variable under test; everything else in the
// nested-claude env is frozen. The bytes are the ones the pipeline binary ships,
// so the hashes (and the l1/l2 cache keys built from them) are the same values
// the live ~/.claude trees produced before the skills moved in-repo.
//
// The scratch dir is deliberately outside the env-pin's own tree: the nested
// claude's cwd is pin.WorkDir and its skills come from pin.ConfigDir, and a
// second copy anywhere above that cwd would make skill resolution ambiguous.
//
// Cache key constraint: eval delivers skills via the pinned config-dir snapshot
// while production delivers via workdir materialization. Since the delivery
// mechanism is not an input to any cache key (SkillHashes hash only the dirs),
// switching eval delivery from config-dir to workdir would require manual l1/l2
// cache invalidation—a silent hit across that switch would be semantically wrong.
func defaultSkillDirs() (map[string]string, func(), error) {
	root, cleanup, err := skills.TempWorkdir()
	if err != nil {
		return nil, func() {}, err
	}
	dirs := map[string]string{}
	for _, name := range skills.Names() {
		dirs[name] = filepath.Join(root, ".claude", "skills", name)
	}
	return dirs, cleanup, nil
}

// snapshotAdoptPaths applies the adopted-check's path selection
// (synthesis.AdoptPathsUnder, covered by the synthesis code version in the
// verify cache key) to the frozen config-snapshot layout.
func snapshotAdoptPaths(dataDir, bucket string) []string {
	return synthesis.AdoptPathsUnder(
		filepath.Join(dataDir, "config-snapshot", "global"),
		filepath.Join(dataDir, "config-snapshot", "repos", bucket),
	)
}

// RunOutcome runs the pipeline stages over the frozen corpus with the
// content-addressed cache; only changed stages cost anything. It produces
// verified outputs (RepoSynthesis + RawSynthesis per sample) in the cache and
// a reproducibility record listing every hash the spec requires.
func RunOutcome(ctx context.Context, opts OutcomeOptions) (RunRecord, error) {
	if opts.Samples == 0 {
		opts.Samples = 3
	}
	if opts.Scope == "" {
		opts.Scope = "l2"
	}
	if opts.Population == "" {
		opts.Population = "scoring"
	}
	if opts.PoolVersion == "" {
		opts.PoolVersion = "v1"
	}
	if opts.Scope != "l2" && opts.Scope != "full" {
		return RunRecord{}, fmt.Errorf("unknown scope %q", opts.Scope)
	}
	if opts.Population != "scoring" && opts.Population != "as_consumed" {
		return RunRecord{}, fmt.Errorf("unknown population %q", opts.Population)
	}
	if opts.SkillDirs == nil {
		dirs, cleanup, err := defaultSkillDirs()
		if err != nil {
			return RunRecord{}, err
		}
		defer cleanup()
		opts.SkillDirs = dirs
	}

	rec := RunRecord{
		RanAt: time.Now().UTC(), Scope: opts.Scope, Population: opts.Population,
		PoolVersion: opts.PoolVersion, Samples: opts.Samples,
		SchemaHashes: map[string]string{"l1": insights.SchemaHash(), "l2": synthesis.SchemaHash()},
		Models:       map[string]string{"l1": insights.AnalysisModel, "l2": synthesis.SynthesisModel},
		CodeVersions: map[string]string{},
	}

	cache := NewCache(opts.CacheDir)
	corpus, err := OpenCorpus(opts.DataDir, filepath.Join(opts.CacheDir, "corpus-plain"))
	if err != nil {
		return rec, err
	}
	rec.ManifestHash = corpus.ManifestHash()

	benchRaw, err := os.ReadFile(filepath.Join(opts.DataDir, "benchmark.json"))
	if err != nil {
		return rec, fmt.Errorf("benchmark: %w", err)
	}
	rec.BenchmarkHash = sha256hex(benchRaw)
	var bench Benchmark
	if err := json.Unmarshal(benchRaw, &bench); err != nil {
		return rec, fmt.Errorf("benchmark: %w", err)
	}
	if len(bench.Buckets) == 0 {
		return rec, fmt.Errorf("benchmark has no buckets — nothing to run (fail-closed)")
	}

	if rec.RubricSetHash, err = RubricSetHash(opts.DataDir); err != nil {
		return rec, err
	}
	if _, err := LoadRubrics(opts.DataDir); err != nil { // fail fast on invalid rubrics
		return rec, err
	}

	factsCV, err := CodeVersion("internal/insights", "internal/transcript")
	if err != nil {
		return rec, err
	}
	synthCV, err := CodeVersion("internal/synthesis")
	if err != nil {
		return rec, err
	}
	evalCV, err := CodeVersion("internal/insightseval")
	if err != nil {
		return rec, err
	}
	rec.CodeVersions["facts"] = factsCV
	rec.CodeVersions["synthesis"] = synthCV
	rec.CodeVersions["insightseval"] = evalCV

	// Sweep scratch remnants from interrupted runs first: a killed run's
	// deferred cleanup never fired, and the ephemeral config dir holds the
	// materialized credential — stale copies must not outlive their run.
	scratchRoot := filepath.Join(opts.CacheDir, "scratch")
	if err := os.RemoveAll(scratchRoot); err != nil {
		return rec, err
	}
	scratch := filepath.Join(scratchRoot, strconv.FormatInt(time.Now().UnixNano(), 10))
	// The ephemeral config/cwd only need to outlive the nested claude calls;
	// removing the whole scratch root at exit (not just this run's subdir)
	// leaves no trace of the run and keeps re-runs from accumulating copies.
	defer os.RemoveAll(scratchRoot)
	pin, err := ComposeEnvPin(opts.DataDir, scratch, opts.SkillDirs, opts.ClaudeVersion)
	if err != nil {
		return rec, err
	}
	rec.ClaudeVersion = pin.ClaudeVersion
	rec.EnvHash = pin.EnvHash
	rec.SkillHashes = pin.SkillHashes
	rec.Warnings = append(rec.Warnings, pin.DriftWarnings...)
	rec.Warnings = append(rec.Warnings, pin.SnapshotWarnings...)
	if rec.ConfigSnapshotHash, err = hashTree(filepath.Join(opts.DataDir, "config-snapshot")); err != nil {
		return rec, err
	}

	synth := opts.Synth
	if synth == nil {
		synth = synthesis.NewClaudeSynthesizerPinned(pin.ConfigDir, pin.WorkDir)
	}
	judge := opts.Judge
	if judge == nil {
		judge = insights.NewClaudeJudgePinned(pin.ConfigDir, pin.WorkDir)
	}

	poolDir := filepath.Join(opts.DataDir, "baseline-pool", opts.PoolVersion)
	buckets := make([]string, 0, len(bench.Buckets))
	for k := range bench.Buckets {
		buckets = append(buckets, k)
	}
	sort.Strings(buckets)

	consecutiveFailures := 0
	for _, bucket := range buckets {
		bp := bench.Buckets[bucket]
		ids := bp.Scoring
		if opts.Population == "as_consumed" {
			ids = bp.AsConsumed
		}
		if opts.Scope == "full" {
			ids = stripGaps(ids, bp.Gaps, bucket, &rec)
		}
		if len(ids) == 0 {
			return rec, fmt.Errorf("bucket %s: empty %s population (fail-closed)", bucket, opts.Population)
		}

		facts, err := RecomputeFacts(corpus, cache, factsCV, poolDir, ids)
		if err != nil {
			return rec, fmt.Errorf("bucket %s: %w", bucket, err)
		}
		rec.CacheHits += facts.CacheHits
		rec.CacheMisses += facts.CacheMisses
		if opts.Scope == "full" && len(facts.GapFallbacks) > 0 {
			// stripGaps removed every RECORDED gap; anything left has no
			// frozen transcript yet isn't in benchmark.json — never blend
			// pool judged fields into a full-scope bundle.
			return rec, fmt.Errorf("bucket %s: ids with no frozen transcript that are not recorded gaps: %v", bucket, facts.GapFallbacks)
		}
		for _, id := range facts.GapFallbacks {
			rec.Warnings = append(rec.Warnings, fmt.Sprintf("%s: %s served from pool stats (transcript pruned pre-freeze)", bucket, id))
		}

		analyses := facts.Analyses
		if opts.L1Sample {
			if err := runL1Sample(ctx, &rec, cache, corpus, facts, pin, judge, bucket); err != nil {
				return rec, err
			}
			continue // sample mode: L1 iteration only, no bundle/L2
		}
		if opts.Scope == "full" {
			analyses, err = runL1Full(ctx, &rec, cache, corpus, facts, pin, judge, poolDir, &consecutiveFailures)
			if err != nil {
				return rec, err
			}
		}

		// Keyed on the analyses actually fed to BuildBundle: equals
		// pool+facts content in l2 scope, reflects fresh L1 output in full
		// scope — the two scopes must never serve each other's bundles.
		analysesJSON, err := json.Marshal(analyses)
		if err != nil {
			return rec, err
		}
		bundleKey := cacheKey("bundle", sha256hex(analysesJSON), synthCV)
		var bundle synthesis.EvidenceBundle
		hit, err := cache.Get("bundle", bundleKey, &bundle)
		if err != nil {
			return rec, err
		}
		if hit {
			rec.CacheHits++
		} else {
			rec.CacheMisses++
			bundle = synthesis.BuildBundle(bucket, analyses)
			if err := cache.Put("bundle", bundleKey, bundle); err != nil {
				return rec, err
			}
		}
		bundleJSON, err := json.Marshal(bundle)
		if err != nil {
			return rec, err
		}
		bundleHash := sha256hex(bundleJSON)

		adopt := synthesis.NewAdoptCheckerFromFiles(snapshotAdoptPaths(opts.DataDir, bucket))
		bo := BucketOutputs{Bucket: bucket, Population: ids, GapFallbacks: facts.GapFallbacks,
			PoolSliceHash: facts.PoolSliceHash, BundleKey: bundleKey, BundleHash: bundleHash}

		// appendBucketAndReturn appends the current (possibly partial) bucket to the record
		// before returning an error, preserving in-flight state on park/error.
		appendBucketAndReturn := func(err error) (RunRecord, error) {
			rec.Buckets = append(rec.Buckets, bo)
			return rec, err
		}

		for s := 0; s < opts.Samples; s++ {
			rawKey := cacheKey("l2", bundleHash, pin.SkillHashes["synthesizing-workflow-insights"],
				synthesis.SchemaHash(), synthesis.SynthesisModel, pin.EnvHash, strconv.Itoa(s))
			var raw synthesis.RawSynthesis
			hit, err := cache.Get("l2", rawKey, &raw)
			if err != nil {
				return appendBucketAndReturn(err)
			}
			fresh := !hit
			if hit {
				rec.CacheHits++
			} else {
				rctx, cancel := context.WithTimeout(ctx, l2SynthesisTimeout)
				raw, err = synth.Synthesize(rctx, bundle)
				cancel()
				if err != nil {
					consecutiveFailures++
					rec.Warnings = append(rec.Warnings, fmt.Sprintf("%s sample %d: L2 failed: %v", bucket, s, err))
					if consecutiveFailures >= consecutiveLLMFailureLimit {
						return appendBucketAndReturn(fmt.Errorf("parked after %d consecutive LLM failures (see warnings)", consecutiveFailures))
					}
					continue
				}
				consecutiveFailures = 0
				rec.CacheMisses++
				if err := cache.Put("l2", rawKey, raw); err != nil {
					return appendBucketAndReturn(err)
				}
			}

			rawJSON, err := json.Marshal(raw)
			if err != nil {
				return appendBucketAndReturn(err)
			}
			verKey := cacheKey("verify", sha256hex(rawJSON), bundleHash, bucket, rec.ConfigSnapshotHash, synthCV)
			var vo VerifiedOutput
			hit, err = cache.Get("verify", verKey, &vo)
			if err != nil {
				return appendBucketAndReturn(err)
			}
			if hit {
				rec.CacheHits++
			} else {
				rec.CacheMisses++
				rs, report := synthesis.Finalize(bucket, bundle, raw, adopt, bench.FrozenAt)
				vo = VerifiedOutput{Synthesis: rs, Raw: raw, Report: report}
				if err := cache.Put("verify", verKey, vo); err != nil {
					return appendBucketAndReturn(err)
				}
			}
			bo.Samples = append(bo.Samples, SampleOutput{SampleIndex: s, Fresh: fresh, RawKey: rawKey, VerifiedKey: verKey})
		}
		rec.Buckets = append(rec.Buckets, bo)
	}

	rec.RecordPath = filepath.Join(opts.CacheDir, "run-records",
		rec.RanAt.Format("2006-01-02T15-04-05Z")+"-"+strconv.FormatInt(rec.RanAt.UnixNano()%1e9, 10)+".json")
	if err := writeJSON(rec.RecordPath, rec); err != nil {
		return rec, err
	}
	return rec, nil
}

// stripGaps removes ids with no frozen transcript from a full-scope
// population (they cannot be re-analyzed); every strip is noted in the record.
func stripGaps(ids, gaps []string, bucket string, rec *RunRecord) []string {
	gapSet := map[string]bool{}
	for _, g := range gaps {
		gapSet[g] = true
	}
	var out []string
	for _, id := range ids {
		if gapSet[id] {
			rec.Warnings = append(rec.Warnings, fmt.Sprintf("%s: %s stripped from full-scope population (gap)", bucket, id))
			continue
		}
		out = append(out, id)
	}
	return out
}

// l1Key builds the L1 stage cache key per the spec table.
func l1Key(reducedText string, pin EnvPin) string {
	return cacheKey("l1", sha256hex([]byte(reducedText)),
		pin.SkillHashes["analyzing-agent-sessions"], insights.SchemaHash(),
		insights.AnalysisModel, pin.EnvHash)
}

// judgeSession runs (or serves cached) one L1 analysis from the frozen
// transcript: decode → Analyze (extract + judge + quote validation), mtime
// restamped from the manifest so incremental semantics match the freeze.
func judgeSession(ctx context.Context, cache *Cache, corpus *Corpus, facts FactsResult, pin EnvPin, judge insights.Judge, poolRepo map[string]string, id string) (insights.AgentSessionAnalysis, bool, error) {
	key := l1Key(facts.Reduced[id].Text, pin)
	var a insights.AgentSessionAnalysis
	hit, err := cache.Get("l1", key, &a)
	if err != nil || hit {
		return a, hit, err
	}
	ref, err := corpus.Ref(id)
	if err != nil {
		return a, false, err
	}
	events, canary, _, err := transcript.LoadTranscript(ref.Path)
	if err != nil {
		return a, false, err
	}
	repo := poolRepo[id]
	jctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	a, _, err = insights.Analyze(jctx, events, canary, id, func(string) string { return repo }, judge)
	cancel()
	if err != nil {
		return a, false, err
	}
	entry, _ := corpus.Entry(id)
	a.TranscriptMtime = entry.Mtime
	return a, false, cache.Put("l1", key, a)
}

// poolRepos recovers the freeze-verified repo identity per session from the
// facts result (stats carry the pool's Repo either way).
func poolRepos(facts FactsResult) map[string]string {
	out := map[string]string{}
	for _, a := range facts.Analyses {
		out[a.Stats.SessionID] = a.Stats.Repo
	}
	return out
}

func runL1Full(ctx context.Context, rec *RunRecord, cache *Cache, corpus *Corpus, facts FactsResult, pin EnvPin, judge insights.Judge, poolDir string, consecutiveFailures *int) ([]insights.AgentSessionAnalysis, error) {
	repos := poolRepos(facts)
	var out []insights.AgentSessionAnalysis
	for _, prior := range facts.Analyses {
		id := prior.Stats.SessionID
		if !corpus.Has(id) {
			// RunOutcome already rejects full-scope gap fallbacks before this
			// runs; pool judged fields must never blend into a full-scope
			// bundle, so anything reaching here is an internal inconsistency.
			return nil, fmt.Errorf("full scope: %s has no frozen transcript", id)
		}
		a, hit, err := judgeSession(ctx, cache, corpus, facts, pin, judge, repos, id)
		if err != nil {
			*consecutiveFailures++
			rec.Warnings = append(rec.Warnings, fmt.Sprintf("L1 %s failed: %v", id, err))
			if *consecutiveFailures >= consecutiveLLMFailureLimit {
				return nil, fmt.Errorf("parked after %d consecutive LLM failures (see warnings)", *consecutiveFailures)
			}
			return nil, fmt.Errorf("L1 %s failed (full sweep must be complete before its pool can be used): %w", id, err)
		}
		*consecutiveFailures = 0
		if hit {
			rec.CacheHits++
		} else {
			rec.CacheMisses++
		}
		out = append(out, a)
	}
	return out, nil
}

func runL1Sample(ctx context.Context, rec *RunRecord, cache *Cache, corpus *Corpus, facts FactsResult, pin EnvPin, judge insights.Judge, bucket string) error {
	stats := make([]insights.AgentSessionStats, 0, len(facts.Analyses))
	sizes := map[string]int64{}
	for _, a := range facts.Analyses {
		id := a.Stats.SessionID
		if !corpus.Has(id) {
			continue // nothing to re-judge without a transcript
		}
		stats = append(stats, a.Stats)
		if e, ok := corpus.Entry(id); ok {
			sizes[id] = e.Bytes
		}
	}
	cells := insights.CurateIDs(stats, sizes)
	if rec.L1Sample == nil {
		rec.L1Sample = &L1SampleResult{Cells: map[string]string{}}
	}
	repos := poolRepos(facts)
	ids := make([]string, 0, len(cells))
	for id := range cells {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		rec.L1Sample.Cells[bucket+"/"+id] = cells[id]
		_, hit, err := judgeSession(ctx, cache, corpus, facts, pin, judge, repos, id)
		if err != nil {
			return fmt.Errorf("l1-sample %s: %w", id, err)
		}
		rec.L1Sample.Analyzed++
		if hit {
			rec.L1Sample.Hits++
			rec.CacheHits++
		} else {
			rec.L1Sample.Misses++
			rec.CacheMisses++
		}
	}
	return nil
}
