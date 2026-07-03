package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"tmux-ctrl/internal/insightseval"
)

// RunInsightsEval dispatches `tmux-ctrl insights eval ...`. Only `freeze` for
// now; the harness subcommands land with the eval-suite phase.
func RunInsightsEval(args []string) {
	if len(args) < 1 || args[0] != "freeze" {
		fmt.Fprintln(os.Stderr, "usage: tmux-ctrl insights eval freeze [--data <dir>]")
		os.Exit(2)
	}
	home, _ := os.UserHomeDir()
	dataDir := filepath.Join(home, "Developer", "insights-eval-data")
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--data":
			i++
			if i >= len(rest) {
				fmt.Fprintln(os.Stderr, "tmux-ctrl insights eval: --data needs a value")
				os.Exit(2)
			}
			dataDir = rest[i]
		default:
			fmt.Fprintf(os.Stderr, "tmux-ctrl insights eval: unknown flag %q\n", rest[i])
			os.Exit(2)
		}
	}
	rep, err := insightseval.RunFreeze(dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tmux-ctrl insights eval freeze: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "freeze: %d sessions · %d sidechains · %d ground-truth files · %d config files\n",
		len(rep.Manifest.Entries), len(rep.Manifest.Sidechains), rep.GroundTruth, rep.ConfigCopied)
	for _, repo := range sortedBucketKeys(rep.Benchmark.Buckets) {
		bp := rep.Benchmark.Buckets[repo]
		fmt.Fprintf(os.Stderr, "freeze: %s · as_consumed=%d scoring=%d (report says %d, resolved=%v)\n",
			repo, len(bp.AsConsumed), len(bp.Scoring), bp.ExpectedAnalyzed, bp.Resolved)
	}
	for _, g := range rep.Issues.Gaps {
		fmt.Fprintf(os.Stderr, "freeze: GAP %s (transcript pruned before freeze; recorded in benchmark.json)\n", g)
	}
	if rep.Issues.Blocking() {
		for _, s := range rep.Issues.Skews {
			fmt.Fprintf(os.Stderr, "freeze: SKEW %s (re-judge: tmux-ctrl insights analyze <id>, then re-run freeze)\n", s)
		}
		for _, c := range rep.Issues.CountMismatches {
			fmt.Fprintf(os.Stderr, "freeze: COUNT MISMATCH %s\n", c)
		}
		v1Exists := dirExists(filepath.Join(dataDir, "baseline-pool", "v1"))
		fmt.Fprintln(os.Stderr, "freeze: "+poolNotWrittenMessage(v1Exists))
		os.Exit(1)
	}
	if len(rep.Issues.Gaps) > 0 {
		fmt.Fprintf(os.Stderr, "freeze: clean apart from %d recorded gaps · baseline-pool/v1 written (%d analyses)\n", len(rep.Issues.Gaps), rep.PoolCopied)
		return
	}
	fmt.Fprintf(os.Stderr, "freeze: clean · baseline-pool/v1 written (%d analyses)\n", rep.PoolCopied)
}

// sortedBucketKeys returns the bucket repo keys in sorted order, for
// deterministic CLI output (Go map iteration order is randomized).
func sortedBucketKeys(buckets map[string]insightseval.BucketPopulations) []string {
	keys := make([]string, 0, len(buckets))
	for k := range buckets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// poolNotWrittenMessage reports on a blocked freeze whether baseline-pool/v1
// is truly absent (never written) or merely retained, unchanged, from an
// earlier clean run — a later run going Blocking() does not erase it.
func poolNotWrittenMessage(v1Exists bool) string {
	if v1Exists {
		return "ISSUES FOUND — baseline-pool/v1 retained from a prior clean run (not re-written); resolve and re-run"
	}
	return "ISSUES FOUND — baseline-pool/v1 NOT written; resolve and re-run"
}

// dirExists reports whether path exists and is a directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
