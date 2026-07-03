package insightseval

import (
	"fmt"
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
// sidechains, benchmark reconstruction (from the frozen ground truth, so
// re-runs never depend on live synthesis state), assertions, then — unless a
// blocking issue (skew or count mismatch) was found — the baseline pool.
// benchmark.json is written even when dirty, for inspection; the pool is the
// one artifact gated on Blocking(). Gaps (transcripts pruned before the
// freeze ever ran) are recorded into their bucket but never gate the pool.
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
	var problems []string
	rep.Benchmark, problems = BuildBenchmark(frozenAt, analyses, truths)
	rep.Issues = AssertFrozen(rep.Benchmark, rep.Manifest, problems)
	recordGaps(rep.Benchmark, rep.Issues.Gaps)
	if err := writeJSON(filepath.Join(dataDir, "benchmark.json"), rep.Benchmark); err != nil {
		return rep, fmt.Errorf("benchmark: %w", err)
	}

	if !rep.Issues.Blocking() {
		rep.PoolCopied, err = CopyBaselinePool(dataDir)
		if err != nil {
			return rep, fmt.Errorf("baseline pool: %w", err)
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

// recordGaps copies gap ids ("<repo>/<id>", already sorted by AssertFrozen)
// into their bucket's Gaps field with the repo prefix stripped, so
// benchmark.json durably records transcripts pruned before the freeze ever
// ran — a no-gaps gate can never pass once a transcript is gone.
func recordGaps(b Benchmark, gaps []string) {
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
