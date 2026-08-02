package cmd

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"tmux-ctrl/internal/insights"
	"tmux-ctrl/internal/synthesis"
	"tmux-ctrl/internal/userconfig"
)

// RunInsights dispatches `tmux-ctrl insights ...`. Mirrors RunHookHandler /
// RunStatusExplain: a thin os.Args branch over the insights package.
func RunInsights(args []string) {
	if len(args) > 0 && args[0] == "eval" {
		RunInsightsEval(args[1:])
		return
	}
	if len(args) > 0 && args[0] == "synthesize" {
		sopts, serr := parseSynthesizeArgs(args[1:])
		if serr != nil {
			fmt.Fprintf(os.Stderr, "tmux-ctrl insights: %v\n", serr)
			fmt.Fprintln(os.Stderr, "usage: tmux-ctrl insights synthesize [--repo <repo-key>] [--min-sessions N] [--due] [--dry-run] [--log <path>]")
			os.Exit(2)
		}
		if sopts.Due {
			cfg, cfgErr := userconfig.Load()
			if cfgErr != nil {
				fmt.Fprintf(os.Stderr, "tmux-ctrl insights: load config: %v\n", cfgErr)
				os.Exit(1)
			}
			sopts.Cadence = time.Duration(cfg.InsightsCadenceDays()) * 24 * time.Hour
		}
		sum, err := synthesis.RunSynthesize(context.Background(), synthesis.NewClaudeSynthesizer(), sopts)
		if err != nil {
			fmt.Fprintf(os.Stderr, "insights synthesize: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "synthesis: %d repos · %d written · %d skipped\n", sum.Repos, sum.Written, sum.Skipped)
		return
	}

	mode, target, opts, err := parseInsightsArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tmux-ctrl insights: %v\n", err)
		fmt.Fprintln(os.Stderr, "usage: tmux-ctrl insights analyze <session-id|path> | --backfill [--force] [--dry-run] [--threshold N] [--timeout 10m] [--quiet-for 24h] | synthesize [--repo <repo-key>] [--min-sessions N] [--due] [--dry-run] [--log <path>] | eval <freeze|outcome|score|adjudicate|probes|statuses> ...")
		os.Exit(2)
	}

	cfg, err := userconfig.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tmux-ctrl insights: load config: %v\n", err)
		os.Exit(1)
	}
	repo := insights.NewRepoResolver(&cfg)
	judge := insights.NewClaudeJudge()

	// Backfill prints the pre-run split before spending; --dry-run stops there.
	if mode == "backfill" {
		plan, planErr := insights.BackfillPlan(opts)
		if planErr != nil {
			fmt.Fprintf(os.Stderr, "tmux-ctrl insights: %v\n", planErr)
			os.Exit(1)
		}
		label := "insights:"
		if opts.DryRun {
			label = "insights (dry-run):"
		}
		fmt.Fprintf(os.Stderr, "%s %d to process · %d already done · %d gated · %d meta-excluded · %d quiet\n", label, plan.ToProcess, plan.Done, plan.Gated, plan.Meta, plan.Quiet)
		if opts.DryRun {
			return
		}
	}

	var sum insights.RunSummary
	switch mode {
	case "single":
		sum, err = insights.RunSingle(context.Background(), target, repo, judge, opts)
	case "backfill":
		sum, err = insights.RunBackfill(context.Background(), repo, judge, opts)
	}
	fmt.Fprintf(os.Stderr, "insights: scanned=%d analyzed=%d skipped-incremental=%d skipped-gate=%d skipped-quiet=%d skipped-meta=%d errored=%d dropped-preferences=%d\n",
		sum.Scanned, sum.Analyzed, sum.SkippedIncremental, sum.SkippedGate, sum.SkippedQuiet, sum.SkippedMeta, sum.Errored, sum.DroppedPreferences)
	if sum.Parked {
		fmt.Fprintf(os.Stderr, "insights: parked — %d done · %d remaining · re-run the same command to continue\n", sum.Analyzed, sum.Remaining)
	}
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
		case a == "--dry-run":
			opts.DryRun = true
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
		case a == "--quiet-for":
			if i+1 >= len(rest) {
				return "", "", opts, fmt.Errorf("--quiet-for needs a value")
			}
			i++
			d, perr := time.ParseDuration(rest[i])
			if perr != nil {
				return "", "", opts, fmt.Errorf("--quiet-for: %w", perr)
			}
			opts.QuietFor = d
		default:
			if len(a) > 0 && a[0] == '-' {
				return "", "", opts, fmt.Errorf("unknown flag %q", a)
			}
			target = a
		}
	}
	if opts.DryRun && !backfill {
		return "", "", opts, fmt.Errorf("--dry-run requires --backfill")
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

// parseSynthesizeArgs parses `synthesize [--repo <repo-key>] [--min-sessions N] [--due] [--dry-run] [--log <path>]`.
func parseSynthesizeArgs(args []string) (synthesis.Options, error) {
	var o synthesis.Options
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dry-run":
			o.DryRun = true
		case "--due":
			o.Due = true
		case "--log":
			i++
			if i >= len(args) {
				return o, fmt.Errorf("--log needs a value")
			}
			o.LogPath = args[i]
		case "--repo":
			i++
			if i >= len(args) {
				return o, fmt.Errorf("--repo needs a value")
			}
			o.Repo = args[i]
		case "--min-sessions":
			i++
			if i >= len(args) {
				return o, fmt.Errorf("--min-sessions needs a value")
			}
			n, err := strconv.Atoi(args[i])
			if err != nil {
				return o, fmt.Errorf("--min-sessions: %w", err)
			}
			o.MinSessions = n
		default:
			return o, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return o, nil
}
