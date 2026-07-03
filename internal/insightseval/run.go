package insightseval

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tmux-ctrl/internal/insights"
	"tmux-ctrl/internal/synthesis"
)

type FreezeReport struct {
	Manifest     Manifest     `json:"manifest"`
	FreezeStats  FreezeStats  `json:"freeze_stats"`
	Benchmark    Benchmark    `json:"benchmark"`
	Issues       FreezeIssues `json:"issues"`
	GroundTruth  int          `json:"ground_truth_files"`
	PoolCopied   int          `json:"pool_files"`
	ConfigCopied int          `json:"config_files"`
	PoolSkipped  bool         `json:"pool_skipped"`
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
// their bucket but never gate the pool.
func RunFreeze(dataDir string) (FreezeReport, error) {
	var rep FreezeReport
	if err := EnsureRepoScaffold(dataDir); err != nil {
		return rep, err
	}
	n, err := CopyGroundTruth(dataDir)
	if err != nil {
		return rep, fmt.Errorf("ground truth: %w", err)
	}
	rep.GroundTruth = n

	analyses, err := synthesis.LoadAnalyses()
	if err != nil {
		return rep, fmt.Errorf("load pool: %w", err)
	}
	byID := make(map[string]insights.AgentSessionAnalysis, len(analyses))
	for _, a := range analyses {
		byID[a.Stats.SessionID] = a
	}

	frozenAt := time.Now().UTC()
	rep.Manifest, rep.FreezeStats, err = FreezeCorpus(dataDir, byID, frozenAt)
	if err != nil {
		return rep, fmt.Errorf("corpus: %w", err)
	}

	truths, err := loadGroundTruth(filepath.Join(dataDir, "ground-truth"))
	if err != nil {
		return rep, fmt.Errorf("read frozen ground truth: %w", err)
	}

	existingBenchmark, hasBenchmark, err := loadBenchmark(dataDir)
	if err != nil {
		return rep, fmt.Errorf("load existing benchmark: %w", err)
	}
	reuseBenchmark := hasBenchmark && allBucketsResolved(existingBenchmark)

	var problems []string
	if reuseBenchmark {
		rep.Benchmark = existingBenchmark
	} else {
		rep.Benchmark, problems = BuildBenchmark(frozenAt, analyses, truths)
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
		}
	} else {
		rep.PoolSkipped = true
	}

	rep.ConfigCopied, err = SnapshotConfig(dataDir, rep.Benchmark.Buckets)
	if err != nil {
		return rep, fmt.Errorf("config snapshot: %w", err)
	}
	return rep, nil
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
