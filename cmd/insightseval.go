package cmd

import (
	"fmt"
	"os"
	"path/filepath"

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
	for repo, bp := range rep.Benchmark.Buckets {
		fmt.Fprintf(os.Stderr, "freeze: %s · as_consumed=%d scoring=%d (report says %d, resolved=%v)\n",
			repo, len(bp.AsConsumed), len(bp.Scoring), bp.ExpectedAnalyzed, bp.Resolved)
	}
	if !rep.Issues.Clean() {
		for _, g := range rep.Issues.Gaps {
			fmt.Fprintf(os.Stderr, "freeze: GAP %s (transcript already pruned)\n", g)
		}
		for _, s := range rep.Issues.Skews {
			fmt.Fprintf(os.Stderr, "freeze: SKEW %s (re-judge: tmux-ctrl insights analyze <id>, then re-run freeze)\n", s)
		}
		for _, c := range rep.Issues.CountMismatches {
			fmt.Fprintf(os.Stderr, "freeze: COUNT MISMATCH %s\n", c)
		}
		fmt.Fprintln(os.Stderr, "freeze: ISSUES FOUND — baseline-pool/v1 NOT written; resolve and re-run")
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "freeze: clean · baseline-pool/v1 written (%d analyses)\n", rep.PoolCopied)
}
