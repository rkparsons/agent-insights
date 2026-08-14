package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
	"github.com/rkparsons/agent-insights/internal/synthesis"
	"github.com/rkparsons/agent-insights/internal/transcript"
	"github.com/rkparsons/agent-insights/skills"
)

const consecutiveLLMFailureLimit = 3

// globalSynthesisTimeout bounds one L2 sample. The production run allows
// synthesis.DefaultGlobalTimeout for a tool-enabled cross-repo pass; eval must
// not kill a call that would have succeeded, because the spend is already gone.
const globalSynthesisTimeout = synthesis.DefaultGlobalTimeout

type OutcomeOptions struct {
	DataDir, CacheDir string
	Scope             string // "l2" (default) | "full"
	Population        string // "scoring" (default) | "as_consumed"
	Samples           int    // default 3
	L1Sample          bool   // Task 11
	PoolVersion       string // default "v1"
	ClaudeVersion     string // injected; CLI fills via claudeVersionString()
	// SynthesisModel is the configured L2 model (insights.Config.SynthesisModel);
	// "" defaults to insights.DefaultSynthesisModel. It is the only key the eval
	// takes from the live config — repo roots and the global asset root come
	// from the frozen snapshot (frozenAssetConfig).
	SynthesisModel string
	SkillDirs      map[string]string           // nil → defaultSkillDirs()
	Judge          insights.Judge              // nil → NewClaudeJudgePinned(pin)
	Synth          synthesis.GlobalSynthesizer // nil → NewClaudeGlobalSynthesizerPinned(pin)
}

type SampleOutput struct {
	SampleIndex int    `json:"sample_index"`
	Fresh       bool   `json:"fresh"` // this L2 sample was a cache miss (churn uses fresh only)
	RawKey      string `json:"raw_key"`
	VerifiedKey string `json:"verified_key"`
}

// BucketOutputs is one repo's contribution to the run: its population and the
// bundle built from it. Samples live on the record, not here — v2 synthesizes
// every bundle in one cross-repo call, so a sample is global.
type BucketOutputs struct {
	Bucket        string   `json:"bucket"`
	Population    []string `json:"population"`
	GapFallbacks  []string `json:"gap_fallbacks,omitempty"`
	PoolSliceHash string   `json:"pool_slice_hash"` // provenance: which pool content fed this bucket
	BundleKey     string   `json:"bundle_key"`      // cache key — the scoring plan fetches the bundle for id→session mapping
	BundleHash    string   `json:"bundle_hash"`
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
	CodeVersions       map[string]string `json:"code_versions"` // "facts", "synthesis", "eval"
	Buckets            []BucketOutputs   `json:"buckets"`
	// SampleOutputs are the global L2 samples — one cross-repo synthesis each,
	// over every bucket's bundle at once (v2). Samples above is the requested
	// count; this is what the run actually produced.
	SampleOutputs []SampleOutput  `json:"sample_outputs"`
	L1Sample      *L1SampleResult `json:"l1_sample,omitempty"`
	CacheHits     int             `json:"cache_hits"`
	CacheMisses   int             `json:"cache_misses"`
	// VerifierRejections names every sample whose raw output the deterministic
	// verifier refused. Structured, not sniffed out of Warnings: it is the
	// pre-spend refusal channel scoring fails closed on — a contract the
	// pipeline broke, not a transient loss.
	VerifierRejections []string `json:"verifier_rejections,omitempty"`
	Warnings           []string `json:"warnings,omitempty"`
	RecordPath         string   `json:"-"`
}

// VerifiedOutput is one L2 sample as scoring reads it: the verified v2 snapshot
// next to the raw model output it was built from (the raw shape is what a
// fabrication/validation probe has to read).
type VerifiedOutput struct {
	Snapshot insights.GlobalSynthesisJSON `json:"snapshot"`
	Raw      insights.RawGlobalSynthesis  `json:"raw"`
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

// RunOutcome runs the pipeline stages over the frozen corpus with the
// content-addressed cache; only changed stages cost anything. It produces
// verified outputs (one global v2 snapshot + its raw model output per sample)
// in the cache and a reproducibility record listing every hash the spec
// requires.
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
	if opts.SynthesisModel == "" {
		opts.SynthesisModel = insights.DefaultSynthesisModel
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
		// The L2 model is a config key under v2 (synthesis_model), not a pinned
		// constant: the configured id is recorded here, keys the L2 stage through
		// the env pin, and is what a switch re-baselines against.
		Models:       map[string]string{"l1": insights.AnalysisModel, "l2": opts.SynthesisModel},
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
	buckets := make([]string, 0, len(bench.Buckets))
	for k := range bench.Buckets {
		buckets = append(buckets, k)
	}
	sort.Strings(buckets)

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
	evalCV, err := CodeVersion("internal/eval")
	if err != nil {
		return rec, err
	}
	rec.CodeVersions["facts"] = factsCV
	rec.CodeVersions["synthesis"] = synthCV
	rec.CodeVersions["eval"] = evalCV

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
	pin, err := ComposeEnvPin(opts.DataDir, scratch, opts.SkillDirs, opts.ClaudeVersion, opts.SynthesisModel)
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

	judge := opts.Judge
	if judge == nil {
		judge = insights.NewClaudeJudgePinned(pin.ConfigDir, pin.WorkDir)
	}
	// The config the model's manifest is built from points into the frozen
	// corpus and omits dotfiles_repo; see frozenAssetConfig. pin.ConfigDir is
	// the global half of that redirect — the synthesizer names its config dir as
	// the manifest's ~/.claude.
	cfg, assetWarnings := frozenAssetConfig(opts.DataDir, buckets, rec.Models["l2"])
	rec.Warnings = append(rec.Warnings, assetWarnings...)
	synth := opts.Synth
	if synth == nil {
		synth = synthesis.NewClaudeGlobalSynthesizerPinned(cfg, pin.ConfigDir, pin.WorkDir)
	}

	poolDir := filepath.Join(opts.DataDir, "baseline-pool", opts.PoolVersion)

	consecutiveFailures := 0
	bundles := map[string]synthesis.EvidenceBundle{}
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
		bundles[bucket] = bundle

		rec.Buckets = append(rec.Buckets, BucketOutputs{Bucket: bucket, Population: ids,
			GapFallbacks: facts.GapFallbacks, PoolSliceHash: facts.PoolSliceHash,
			BundleKey: bundleKey, BundleHash: bundleHash})
	}

	if !opts.L1Sample {
		if err := runGlobalSamples(ctx, &rec, cache, synth, bundles, pin, cfg, bench.FrozenAt,
			opts.Samples, synthCV, &consecutiveFailures); err != nil {
			return rec, err
		}
	}

	rec.RecordPath = filepath.Join(opts.CacheDir, "run-records",
		rec.RanAt.Format("2006-01-02T15-04-05Z")+"-"+strconv.FormatInt(rec.RanAt.UnixNano()%1e9, 10)+".json")
	if err := writeJSON(rec.RecordPath, rec); err != nil {
		return rec, err
	}
	return rec, nil
}

// skillSetHash identifies every skill the env pin overlaid into the config dir
// the manifest points the model at — the synthesis skill it invokes AND every
// other one sitting readable beside it.
func skillSetHash(hashes map[string]string) string {
	names := make([]string, 0, len(hashes))
	for name := range hashes {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := []string{"skill-set"}
	for _, name := range names {
		parts = append(parts, name, hashes[name])
	}
	return cacheKey(parts...)
}

// assetRootsHash identifies which repo roots the manifest can name as readable
// (frozenAssetConfig resolved them) versus which it renders "unavailable". Keyed
// on bucket keys — the base names synthesis.repoRootsFor pairs on — so the hash
// tracks the manifest's meaning rather than where the data repo happens to live.
func assetRootsHash(cfg insights.Config) string {
	roots := make([]string, 0, len(cfg.Repos))
	for _, p := range cfg.Repos {
		roots = append(roots, filepath.Base(p))
	}
	sort.Strings(roots)
	return cacheKey(append([]string{"asset-roots"}, roots...)...)
}

// bundleSetHash identifies the exact evidence the model is shown: every repo's
// key and bundle bytes. v2 synthesizes all of them in one call, so a change in
// ANY bucket re-keys every sample — there is no per-bucket sample to key
// separately any more.
func bundleSetHash(bundles map[string]synthesis.EvidenceBundle) (string, error) {
	parts := []string{"bundle-set"}
	for _, k := range sortedKeysOfBundles(bundles) {
		raw, err := json.Marshal(bundles[k])
		if err != nil {
			return "", err
		}
		parts = append(parts, k, sha256hex(raw))
	}
	return cacheKey(parts...), nil
}

// runGlobalSamples takes opts.Samples independent draws of the one cross-repo
// synthesis, verifies each, and records the cache keys scoring reads. Sampling
// is global because the pipeline is: a sample is one GlobalSynthesis over every
// bucket's bundle, not one synthesis per repo.
//
// A sample that fails (LLM error, or model output the verifier rejects) is
// warned about and dropped, never half-recorded: scoring fails closed on a
// record with no samples rather than scoring whatever survived.
func runGlobalSamples(ctx context.Context, rec *RunRecord, cache *Cache, synth synthesis.GlobalSynthesizer,
	bundles map[string]synthesis.EvidenceBundle, pin EnvPin, cfg insights.Config, generatedAt time.Time,
	samples int, synthCV string, consecutiveFailures *int) error {
	if len(bundles) == 0 {
		return fmt.Errorf("no bundles built — nothing to synthesize (fail-closed)")
	}
	// The L2 keys are built from the pin's model, the run reports the record's:
	// a pin composed without the run's model would key every paid sample under
	// an identity that does not name the model that produced it.
	if pin.SynthesisModel == "" || pin.SynthesisModel != rec.Models["l2"] {
		return fmt.Errorf("env pin's synthesis model %q does not match the run's L2 model %q — refusing to key L2 spend on a model the pin does not name",
			pin.SynthesisModel, rec.Models["l2"])
	}
	setHash, err := bundleSetHash(bundles)
	if err != nil {
		return err
	}
	for s := 0; s < samples; s++ {
		// Everything the model can see has to be in this key:
		//
		//   - skillSetHash, not the synthesis skill alone: the pin overlays EVERY
		//     live skill into the config dir and the manifest advertises that
		//     whole skills/ directory as readable, so keying on one skill let an
		//     edit to any other one be served a stale answer for free.
		//   - the whole frozen asset corpus, not just the global half the env pin
		//     hashes: the model READS the repo CLAUDE.mds the manifest names.
		//   - assetRootsHash: WHICH repo roots the manifest could name. A root
		//     that resolves and a root rendered "unavailable" are different
		//     prompts. Bucket keys rather than paths, so relocating the data repo
		//     does not re-key a corpus that has not changed.
		//   - pin.L2EnvHash: CLI version, pristine snapshot, and the configured
		//     synthesis model (deliberately absent from EnvHash — see EnvPin), so
		//     a model switch re-keys L2 and nothing else.
		rawKey := cacheKey("l2", setHash, skillSetHash(pin.SkillHashes),
			synthesis.SchemaHash(), rec.ConfigSnapshotHash, assetRootsHash(cfg),
			pin.L2EnvHash, strconv.Itoa(s))
		var raw insights.RawGlobalSynthesis
		hit, err := cache.Get("l2", rawKey, &raw)
		if err != nil {
			return err
		}
		fresh := !hit
		if hit {
			rec.CacheHits++
		} else {
			rctx, cancel := context.WithTimeout(ctx, globalSynthesisTimeout)
			raw, err = synth.SynthesizeGlobal(rctx, bundles)
			cancel()
			if err != nil {
				*consecutiveFailures++
				rec.Warnings = append(rec.Warnings, fmt.Sprintf("sample %d: L2 failed: %v", s, err))
				if *consecutiveFailures >= consecutiveLLMFailureLimit {
					return fmt.Errorf("parked after %d consecutive LLM failures (see warnings)", *consecutiveFailures)
				}
				continue
			}
			*consecutiveFailures = 0
			rec.CacheMisses++
			if err := cache.Put("l2", rawKey, raw); err != nil {
				return err
			}
		}

		rawJSON, err := json.Marshal(raw)
		if err != nil {
			return err
		}
		// The model belongs in this key too: VerifyGlobal stamps meta.model from
		// the run's config, so a re-run on a switched model over identical raw
		// output would otherwise be served a snapshot naming the OLD model.
		// Verification is pure Go — re-keying it costs compute, never spend.
		verKey := cacheKey("verify", sha256hex(rawJSON), setHash, rec.ConfigSnapshotHash, rec.Models["l2"], synthCV)
		var vo VerifiedOutput
		hit, err = cache.Get("verify", verKey, &vo)
		if err != nil {
			return err
		}
		if hit {
			rec.CacheHits++
		} else {
			rec.CacheMisses++
			// generatedAt is the benchmark's freeze instant, not now: the
			// verified output is content-addressed, so a re-verify of the same
			// raw output must reproduce the same bytes.
			snap, err := synthesis.VerifyGlobal(ctx, raw, bundles, cfg, generatedAt)
			if err != nil {
				// Verification is deterministic — a rejected sample is model
				// output the contract refuses, not a transient failure, so it
				// does not count toward the consecutive-failure park. It is
				// recorded structurally as well as warned about: scoring
				// refuses a record carrying one, pre-spend.
				note := fmt.Sprintf("sample %d: verification rejected the synthesis: %v", s, err)
				rec.VerifierRejections = append(rec.VerifierRejections, note)
				rec.Warnings = append(rec.Warnings, note)
				continue
			}
			vo = VerifiedOutput{Snapshot: snap, Raw: raw}
			if err := cache.Put("verify", verKey, vo); err != nil {
				return err
			}
		}
		rec.SampleOutputs = append(rec.SampleOutputs, SampleOutput{SampleIndex: s, Fresh: fresh,
			RawKey: rawKey, VerifiedKey: verKey})
	}
	return nil
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
