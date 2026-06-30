package cmd

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"tmux-ctrl/internal/insights"
	"tmux-ctrl/internal/userconfig"
)

// RunInsights dispatches `tmux-ctrl insights ...`. Mirrors RunHookHandler /
// RunStatusExplain: a thin os.Args branch over the insights package.
func RunInsights(args []string) {
	mode, target, opts, err := parseInsightsArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tmux-ctrl insights: %v\n", err)
		fmt.Fprintln(os.Stderr, "usage: tmux-ctrl insights analyze <session-id|path> | --backfill [--force] [--retry-errored] [--threshold N] [--timeout 10m]")
		os.Exit(2)
	}

	cfg, err := userconfig.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tmux-ctrl insights: load config: %v\n", err)
		os.Exit(1)
	}
	repo := insights.NewRepoResolver(&cfg)
	judge := insights.NewClaudeJudge()

	var sum insights.RunSummary
	switch mode {
	case "single":
		sum, err = insights.RunSingle(context.Background(), target, repo, judge, opts)
	case "backfill":
		sum, err = insights.RunBackfill(context.Background(), repo, judge, opts)
	}
	fmt.Fprintf(os.Stderr, "insights: scanned=%d analyzed=%d skipped-incremental=%d skipped-gate=%d errored=%d dropped-preferences=%d\n",
		sum.Scanned, sum.Analyzed, sum.SkippedIncremental, sum.SkippedGate, sum.Errored, sum.DroppedPreferences)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tmux-ctrl insights: %v\n", err)
		os.Exit(1)
	}
}

// parseInsightsArgs parses `analyze <session> | --backfill` plus flags. Returns mode
// "single" or "backfill", the single-mode target, and Options.
func parseInsightsArgs(args []string) (mode, target string, opts insights.Options, err error) {
	opts = insights.Options{MinAssistantTurns: insights.DefaultMinAssistantTurns, Timeout: 10 * time.Minute}
	if len(args) < 1 || args[0] != "analyze" {
		return "", "", opts, fmt.Errorf("expected: analyze ...")
	}
	rest := args[1:]
	backfill := false
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "--backfill":
			backfill = true
		case a == "--force":
			opts.Force = true
		case a == "--retry-errored":
			opts.RetryErrored = true
		case a == "--threshold":
			if i+1 >= len(rest) {
				return "", "", opts, fmt.Errorf("--threshold needs a value")
			}
			i++
			n, perr := strconv.Atoi(rest[i])
			if perr != nil {
				return "", "", opts, fmt.Errorf("--threshold: %w", perr)
			}
			opts.MinAssistantTurns = n
		case a == "--timeout":
			if i+1 >= len(rest) {
				return "", "", opts, fmt.Errorf("--timeout needs a value")
			}
			i++
			d, perr := time.ParseDuration(rest[i])
			if perr != nil {
				return "", "", opts, fmt.Errorf("--timeout: %w", perr)
			}
			opts.Timeout = d
		default:
			if len(a) > 0 && a[0] == '-' {
				return "", "", opts, fmt.Errorf("unknown flag %q", a)
			}
			target = a
		}
	}
	if backfill {
		if target != "" {
			return "", "", opts, fmt.Errorf("--backfill takes no session argument")
		}
		return "backfill", "", opts, nil
	}
	if target == "" {
		return "", "", opts, fmt.Errorf("analyze needs a <session-id|path> or --backfill")
	}
	return "single", target, opts, nil
}
