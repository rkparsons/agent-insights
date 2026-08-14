package eval

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
	"github.com/rkparsons/agent-insights/internal/synthesis"
)

type FreezeReport struct {
	Manifest                  Manifest     `json:"manifest"`
	FreezeStats               FreezeStats  `json:"freeze_stats"`
	Benchmark                 Benchmark    `json:"benchmark"`
	Issues                    FreezeIssues `json:"issues"`
	GroundTruth               int          `json:"ground_truth_files"`
	GlobalGroundTruth         int          `json:"global_ground_truth_files"`
	PoolCopied                int          `json:"pool_files"`
	ConfigCopied              int          `json:"config_files"`
	PoolSkipped               bool         `json:"pool_skipped"`
	PoolRetained              bool         `json:"pool_retained"`
	GroundTruthRetained       bool         `json:"ground_truth_retained"`
	GlobalGroundTruthRetained bool         `json:"global_ground_truth_retained"`
}

// RunFreeze executes the full freeze: scaffold, ground-truth copy, corpus +
// sidechains, benchmark reconstruction, assertions, then — unless a blocking
// issue (skew or count mismatch) was found — the baseline pool.
//
// benchmark.json and baseline-pool/v1 are canonical once written: if an
// existing benchmark.json has every bucket resolved, it is reused byte-for-
// byte (no rebuild, no re-write) instead of being rebuilt from whatever the
// live pool looks like today; if baseline-pool/v1 already exists, it is never
// re-copied (new live analyses do not silently join it, and a changed live
// analysis does not hard-fail it). FreezeIssues — including the skew check,
// which reads the stamped mtime from v1 once v1 is canonical rather than the
// (possibly since-rejudged) live pool — are still recomputed fresh every run.
// Gaps (transcripts pruned before the freeze ever ran) are recorded into
// their bucket but never gate the pool. cfg is loaded once by the CLI entry
// point (cmd/eval.go) and threaded through to every RepoKey grouping
// call below, so FreezeCorpus/BuildBenchmark never load it themselves.
func RunFreeze(dataDir string, cfg insights.Config) (FreezeReport, error) {
	var rep FreezeReport
	if err := EnsureRepoScaffold(dataDir); err != nil {
		return rep, err
	}
	gtDir := filepath.Join(dataDir, "ground-truth")
	if dirExists(gtDir) {
		// Ground truth is canonical once frozen: re-copying would let a newer
		// live synthesis slip in and silently shift loadGroundTruth's
		// newest-file pick — the anchors' source of truth.
		rep.GroundTruthRetained = true
	} else {
		n, err := CopyGroundTruth(dataDir)
		if err != nil {
			return rep, fmt.Errorf("ground truth: %w", err)
		}
		rep.GroundTruth = n
	}
	// The v2 anchors are their own canonical-once leg. A data repo frozen
	// before the cutover already has (retained) v1 ground truth, so folding
	// this into the branch above would mean its v2 snapshots never arrive at
	// all; write-once for the same reason the v1 leg is, so a newer live
	// snapshot can never shift loadGlobalGroundTruth's newest-file pick.
	if dirExists(filepath.Join(gtDir, globalGroundTruthDir)) {
		rep.GlobalGroundTruthRetained = true
	} else {
		n, err := CopyGlobalGroundTruth(dataDir)
		if err != nil {
			return rep, fmt.Errorf("global ground truth: %w", err)
		}
		rep.GlobalGroundTruth = n
	}

	analyses, err := synthesis.LoadAnalyses()
	if err != nil {
		return rep, fmt.Errorf("load pool: %w", err)
	}
	byID := make(map[string]insights.AgentSessionAnalysis, len(analyses))
	for _, a := range analyses {
		byID[a.Stats.SessionID] = a
	}

	frozenAt := time.Now().UTC()
	rep.Manifest, rep.FreezeStats, err = FreezeCorpus(dataDir, byID, frozenAt, cfg)
	if err != nil {
		return rep, fmt.Errorf("corpus: %w", err)
	}

	truths, err := loadGroundTruth(gtDir)
	if err != nil {
		return rep, fmt.Errorf("read frozen ground truth: %w", err)
	}
	// A frozen v2 snapshot outranks the v1 per-repo reports: it names the
	// buckets and their analyzed counts (BuildBenchmark). The v1 truths stay
	// loaded regardless — historical records and the as_consumed control's
	// pre-strip anchors still read them.
	globalTruth, hasGlobal, err := loadGlobalGroundTruth(gtDir)
	if err != nil {
		return rep, fmt.Errorf("read frozen global ground truth: %w", err)
	}
	var global *insights.GlobalSynthesisJSON
	if hasGlobal {
		global = &globalTruth
	}

	existingBenchmark, hasBenchmark, err := loadBenchmark(dataDir)
	if err != nil {
		return rep, fmt.Errorf("load existing benchmark: %w", err)
	}
	reuseBenchmark := hasBenchmark && len(existingBenchmark.Buckets) > 0 && allBucketsResolved(existingBenchmark)

	// Cutover refusals are COLLECTED, not returned one at a time: both the
	// benchmark and the config snapshot are canonical-once artifacts a v2
	// cutover invalidates together, and an operator who is told about one,
	// archives it, re-runs and is then told about the other cannot tell whether
	// the ritual is converging. Both are decided before anything is written.
	var refusals []string
	if reuseBenchmark && hasGlobal {
		if why := benchmarkCutoverMismatch(existingBenchmark, globalTruth); why != "" {
			// Refuse rather than auto-rebuild: benchmark.json's populations are
			// what every committed v1 verdict's BenchmarkHash names, and every
			// bucket population feeds a cache key. Silently rewriting it would
			// relabel history and re-buy L2 without the operator ever seeing a
			// cutover happen. Archiving is a deliberate, one-line act.
			refusals = append(refusals, fmt.Sprintf("benchmark.json predates the v2 cutover (%s): reusing it would pin v1 buckets the frozen v2 ground truth does not describe — archive it (`mv %s %s`) and re-run `agent-insights eval freeze` to rebuild the buckets from the v2 snapshot",
				why, filepath.Join(dataDir, "benchmark.json"), filepath.Join(dataDir, "benchmark-v1.json")))
			// Rebuild in memory anyway, so the config-snapshot check below reads
			// the buckets the operator will actually end up with.
			reuseBenchmark = false
		}
	}

	var problems []string
	if reuseBenchmark {
		rep.Benchmark = existingBenchmark
	} else {
		rep.Benchmark, problems = BuildBenchmark(frozenAt, analyses, truths, global, cfg)
	}

	// The asset corpus is append-only too, and the v2 cutover is exactly when it
	// moves (new skills, edited CLAUDE.mds, changed settings). Detecting the
	// conflict here turns what would otherwise be a bare append-only violation
	// from SnapshotConfig — thrown AFTER benchmark.json had been rewritten, and
	// repeated on every subsequent freeze — into the same archive instruction
	// the benchmark refusal gives.
	conflicts, err := ConfigSnapshotConflicts(dataDir, rep.Benchmark.Buckets, cfg)
	if err != nil {
		return rep, fmt.Errorf("config snapshot: %w", err)
	}
	if len(conflicts) > 0 {
		refusals = append(refusals, fmt.Sprintf("config-snapshot holds different bytes for %d frozen asset(s) (%s): the corpus the synthesis reads has moved since it was frozen, and fixtures are append-only — archive it (`mv %s %s`) and re-run `agent-insights eval freeze` to freeze the current corpus",
			len(conflicts), summarizePaths(conflicts, 3),
			filepath.Join(dataDir, "config-snapshot"), filepath.Join(dataDir, "config-snapshot-v1")))
	}
	if len(refusals) > 0 {
		return rep, fmt.Errorf("%s\n\n(benchmark.json and config-snapshot are untouched; the corpus and ground-truth legs are append-only and unaffected)",
			strings.Join(refusals, "\n\n"))
	}

	v1Dir := filepath.Join(dataDir, "baseline-pool", "v1")
	v1Exists := dirExists(v1Dir)
	poolMtime := insights.ReadAnalysisMtime
	if v1Exists {
		poolMtime = func(id string) (time.Time, bool) {
			return readStampedMtime(filepath.Join(v1Dir, id+".json"))
		}
	}
	rep.Issues = AssertFrozen(rep.Benchmark, rep.Manifest, problems, poolMtime)
	recordGaps(rep.Benchmark, rep.Issues.Gaps)

	if !reuseBenchmark {
		if err := writeJSON(filepath.Join(dataDir, "benchmark.json"), rep.Benchmark); err != nil {
			return rep, fmt.Errorf("benchmark: %w", err)
		}
	}

	if !rep.Issues.Blocking() {
		if !v1Exists {
			rep.PoolCopied, err = CopyBaselinePool(dataDir)
			if err != nil {
				return rep, fmt.Errorf("baseline pool: %w", err)
			}
		} else {
			rep.PoolRetained = true
		}
	} else {
		rep.PoolSkipped = true
	}

	rep.ConfigCopied, err = SnapshotConfig(dataDir, rep.Benchmark.Buckets, cfg)
	if err != nil {
		return rep, fmt.Errorf("config snapshot: %w", err)
	}
	return rep, nil
}

// benchmarkCutoverMismatch reports why an existing (resolved, therefore
// reusable) benchmark cannot be reused against frozen v2 ground truth, or ""
// when it still describes the same populations. Under v2 the global snapshot is
// the sole authority for which buckets exist and how many analyses each
// contributed (BuildBenchmark), so a benchmark reconstructed from v1 per-repo
// reports can pin buckets — and counts — the v2 anchors never had.
func benchmarkCutoverMismatch(b Benchmark, global insights.GlobalSynthesisJSON) string {
	want := make(map[string]int, len(global.Repos))
	var wantKeys []string
	for _, r := range global.Repos {
		want[r.Key] = r.AnalyzedCount
		wantKeys = append(wantKeys, r.Key)
	}
	var haveKeys []string
	for k := range b.Buckets {
		haveKeys = append(haveKeys, k)
	}
	sort.Strings(wantKeys)
	sort.Strings(haveKeys)
	if !slices.Equal(wantKeys, haveKeys) {
		return fmt.Sprintf("benchmark buckets %v, v2 ground-truth repos %v", haveKeys, wantKeys)
	}
	for _, k := range haveKeys {
		if got := b.Buckets[k].ExpectedAnalyzed; got != want[k] {
			return fmt.Sprintf("bucket %s expects %d analyses, the v2 snapshot says %d", k, got, want[k])
		}
	}
	return ""
}

// summarizePaths renders at most max paths, naming how many were elided.
func summarizePaths(paths []string, max int) string {
	if len(paths) <= max {
		return strings.Join(paths, ", ")
	}
	return fmt.Sprintf("%s, and %d more", strings.Join(paths[:max], ", "), len(paths)-max)
}

// dirExists reports whether path exists and is a directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// recordGaps resets every bucket's Gaps field and repopulates it from gaps
// ("<repo>/<id>", already sorted by AssertFrozen) with the repo prefix
// stripped, so benchmark.json durably records transcripts pruned before the
// freeze ever ran — a no-gaps gate can never pass once a transcript is gone.
// Reset-then-repopulate keeps this idempotent across runs that reuse an
// existing (already gap-annotated) benchmark rather than rebuilding it.
func recordGaps(b Benchmark, gaps []string) {
	for repo, bp := range b.Buckets {
		bp.Gaps = nil
		b.Buckets[repo] = bp
	}
	for _, g := range gaps {
		repo, id, ok := strings.Cut(g, "/")
		if !ok {
			continue
		}
		bp := b.Buckets[repo]
		bp.Gaps = append(bp.Gaps, id)
		b.Buckets[repo] = bp
	}
}
