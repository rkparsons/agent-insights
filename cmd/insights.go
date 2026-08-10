package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
	"github.com/rkparsons/agent-insights/internal/synthesis"
)

const insightsUsage = "usage: agent-insights backfill [--quiet-for 24h] [--timeout 10m] [--threshold N] [--force] [--dry-run] | analyze <session-id|path> [--force] | synthesize [--repo <repo-key>] [--min-sessions N] [--due] [--dry-run] [--log <path>] | enrich [--repo <repo-key>] [--dry-run] [--timeout 10m] | status --json | show --json | acted <key> | unacted <key> | eval <freeze|outcome|score|adjudicate|probes|statuses> ..."

// RunInsights dispatches `agent-insights ...`. Mirrors RunHookHandler /
// RunStatusExplain: a thin os.Args branch over the insights/synthesis packages.
func RunInsights(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, insightsUsage)
		os.Exit(2)
	}
	if args[0] == "eval" {
		RunInsightsEval(args[1:])
		return
	}

	// Single load, threaded down: every remaining subcommand either needs cfg
	// directly (backfill/analyze's resolver, synthesize's --due cadence,
	// status's due-computation) or is cheap enough that loading it uniformly
	// keeps "malformed config is a hard error everywhere" simple. eval is the
	// one other entry point in this file and loads its own config already.
	icfg, err := insights.LoadConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent-insights: load config: %v\n", err)
		os.Exit(1)
	}

	switch args[0] {
	case "synthesize":
		runSynthesize(icfg, args[1:])
	case "enrich":
		runEnrich(args[1:])
	case "status":
		runStatus(icfg, args[1:])
	case "show":
		runShow(args[1:])
	case "acted":
		runActed(args[1:], true)
	case "unacted":
		runActed(args[1:], false)
	case "backfill":
		runBackfill(icfg, args[1:])
	case "analyze":
		runAnalyze(icfg, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "agent-insights: unknown subcommand %q\n", args[0])
		fmt.Fprintln(os.Stderr, insightsUsage)
		os.Exit(2)
	}
}

func runSynthesize(icfg insights.Config, args []string) {
	sopts, serr := parseSynthesizeArgs(args)
	if serr != nil {
		fmt.Fprintf(os.Stderr, "agent-insights: %v\n", serr)
		fmt.Fprintln(os.Stderr, "usage: agent-insights synthesize [--repo <repo-key>] [--min-sessions N] [--due] [--dry-run] [--log <path>]")
		os.Exit(2)
	}
	if sopts.Due {
		sopts.Cadence = time.Duration(icfg.CadenceDays) * 24 * time.Hour
	}
	sum, err := synthesis.RunSynthesize(context.Background(), synthesis.NewClaudeSynthesizer, sopts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "insights synthesize: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "synthesis: %d repos · %d written · %d skipped\n", sum.Repos, sum.Written, sum.Skipped)
}

// runEnrich handles `insights enrich [--repo K] [--dry-run] [--timeout D]` —
// backfills title/last_seen onto stored syntheses (idempotent).
func runEnrich(args []string) {
	opts, err := parseEnrichArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent-insights: %v\n", err)
		fmt.Fprintln(os.Stderr, "usage: agent-insights enrich [--repo <repo-key>] [--dry-run] [--timeout 10m]")
		os.Exit(2)
	}
	workDir, err := os.MkdirTemp("", "agent-insights-enrich-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent-insights: enrich: %v\n", err)
		os.Exit(1)
	}
	sum, err := synthesis.RunEnrich(context.Background(), synthesis.NewClaudeTitler(workDir), opts)
	os.RemoveAll(workDir) // not deferred: os.Exit below would skip it
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent-insights: enrich: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "enrich: %d snapshots · %d updated · %d titles · %d last_seen · %d skipped\n",
		sum.Snapshots, sum.Updated, sum.TitlesFilled, sum.LastSeenFilled, sum.Skipped)
}

// runAnalyze handles `insights analyze <session-id|path> [--force]`.
func runAnalyze(icfg insights.Config, args []string) {
	target, opts, err := parseAnalyzeArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent-insights: %v\n", err)
		fmt.Fprintln(os.Stderr, "usage: agent-insights analyze <session-id|path> [--force]")
		os.Exit(2)
	}
	repo := icfg.Resolver()
	sum, err := insights.RunSingle(context.Background(), target, repo, insights.NewClaudeJudge, opts)
	printRunSummary(sum)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent-insights: %v\n", err)
		os.Exit(1)
	}
}

// runBackfill handles `insights backfill [--quiet-for D] [--timeout D] [--threshold N] [--force] [--dry-run]`.
// Prints the pre-run split before spending; --dry-run stops there.
func runBackfill(icfg insights.Config, args []string) {
	opts, err := parseBackfillArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent-insights: %v\n", err)
		fmt.Fprintln(os.Stderr, "usage: agent-insights backfill [--quiet-for 24h] [--timeout 10m] [--threshold N] [--force] [--dry-run]")
		os.Exit(2)
	}
	plan, planErr := insights.BackfillPlan(opts)
	if planErr != nil {
		fmt.Fprintf(os.Stderr, "agent-insights: %v\n", planErr)
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

	repo := icfg.Resolver()
	sum, err := insights.RunBackfill(context.Background(), repo, insights.NewClaudeJudge, opts)
	printRunSummary(sum)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent-insights: %v\n", err)
		os.Exit(1)
	}
}

func printRunSummary(sum insights.RunSummary) {
	fmt.Fprintf(os.Stderr, "insights: scanned=%d analyzed=%d skipped-incremental=%d skipped-gate=%d skipped-quiet=%d skipped-meta=%d errored=%d dropped-preferences=%d\n",
		sum.Scanned, sum.Analyzed, sum.SkippedIncremental, sum.SkippedGate, sum.SkippedQuiet, sum.SkippedMeta, sum.Errored, sum.DroppedPreferences)
	if sum.Parked {
		fmt.Fprintf(os.Stderr, "insights: parked — %d done · %d remaining · re-run the same command to continue\n", sum.Analyzed, sum.Remaining)
	}
}

// runStatus handles `insights status --json`.
func runStatus(icfg insights.Config, args []string) {
	if !isJSONFlag(args) {
		fmt.Fprintln(os.Stderr, "usage: agent-insights status --json")
		os.Exit(2)
	}
	status, err := buildStatusJSON(icfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent-insights: status: %v\n", err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(status); err != nil {
		fmt.Fprintf(os.Stderr, "agent-insights: status: %v\n", err)
		os.Exit(1)
	}
}

func isJSONFlag(args []string) bool {
	return len(args) == 1 && args[0] == "--json"
}

// buildStatusJSON gathers status --json's sources: DueRepos reuses the same
// GroupByRepo/DueRepos pipeline functions synthesize --due runs on, rather
// than reimplementing due-ness here.
func buildStatusJSON(icfg insights.Config) (insights.StatusJSON, error) {
	analyses, err := synthesis.LoadAnalyses()
	if err != nil && !os.IsNotExist(err) {
		return insights.StatusJSON{}, err
	}
	groups := synthesis.GroupByRepo(analyses, icfg.MinSessions, icfg)
	syntheses, err := synthesis.LoadSyntheses()
	if err != nil {
		return insights.StatusJSON{}, err
	}
	cadence := time.Duration(icfg.CadenceDays) * 24 * time.Hour
	due := synthesis.DueRepos(groups, syntheses, cadence, time.Now())
	sort.Strings(due)

	actedMap, err := synthesis.LoadActedKeys()
	if err != nil {
		return insights.StatusJSON{}, err
	}
	acted := make([]string, 0, len(actedMap))
	for k := range actedMap {
		acted = append(acted, k)
	}
	sort.Strings(acted)

	running := insights.LockHeld()
	var lastRun *insights.LastRunJSON
	if rs, ok := synthesis.ReadRunState(); ok {
		lastRun = lastRunJSON(rs, running)
	}

	return insights.BuildStatus(insights.InsightsDir(), insights.SynthesizeLogPath(), running, insights.HeldOp(), due, acted, lastRun), nil
}

// lastRunJSON projects RunState for status --json. A "running" record with a
// free lock means the run died without rewriting its state — surfaced as an
// error rather than left looking idle, since the TUI has no other signal.
func lastRunJSON(rs synthesis.RunState, lockHeld bool) *insights.LastRunJSON {
	lr := insights.LastRunJSON{StartedAt: rs.StartedAt.UTC().Format(time.RFC3339)}
	if rs.FinishedAt != nil {
		lr.FinishedAt = rs.FinishedAt.UTC().Format(time.RFC3339)
	}
	switch {
	case rs.Status == "failed":
		lr.Error = rs.Reason
	case rs.Status == "running" && !lockHeld:
		lr.Error = "run died (no exit record)"
	}
	return &lr
}

// runShow handles `insights show --json`.
func runShow(args []string) {
	if !isJSONFlag(args) {
		fmt.Fprintln(os.Stderr, "usage: agent-insights show --json")
		os.Exit(2)
	}
	syntheses, err := synthesis.LoadSyntheses()
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent-insights: show: %v\n", err)
		os.Exit(1)
	}
	show := synthesis.BuildShowJSON(syntheses)
	if err := json.NewEncoder(os.Stdout).Encode(show); err != nil {
		fmt.Fprintf(os.Stderr, "agent-insights: show: %v\n", err)
		os.Exit(1)
	}
}

// runActed handles `insights acted <key>` / `insights unacted <key>`.
func runActed(args []string, mark bool) {
	verb := "acted"
	if !mark {
		verb = "unacted"
	}
	if len(args) != 1 || args[0] == "" || args[0][0] == '-' {
		fmt.Fprintf(os.Stderr, "usage: agent-insights %s <key>\n", verb)
		os.Exit(2)
	}
	key := args[0]
	var err error
	if mark {
		err = synthesis.MarkActed(key)
	} else {
		err = synthesis.UnmarkActed(key)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent-insights: %s: %v\n", verb, err)
		os.Exit(1)
	}
}

// parseAnalyzeArgs parses `analyze <session-id|path> [--force]`.
func parseAnalyzeArgs(args []string) (target string, opts insights.Options, err error) {
	opts = insights.Options{MinAssistantTurns: insights.DefaultMinAssistantTurns, Timeout: 10 * time.Minute}
	for _, a := range args {
		switch {
		case a == "--force":
			opts.Force = true
		default:
			if len(a) > 0 && a[0] == '-' {
				return "", opts, fmt.Errorf("unknown flag %q", a)
			}
			if target != "" {
				return "", opts, fmt.Errorf("analyze takes exactly one session argument")
			}
			target = a
		}
	}
	if target == "" {
		return "", opts, fmt.Errorf("analyze needs a <session-id|path>")
	}
	return target, opts, nil
}

// parseBackfillArgs parses `backfill [--quiet-for D] [--timeout D] [--threshold N] [--force] [--dry-run]`.
func parseBackfillArgs(args []string) (insights.Options, error) {
	opts := insights.Options{MinAssistantTurns: insights.DefaultMinAssistantTurns, Timeout: 10 * time.Minute}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--force":
			opts.Force = true
		case "--dry-run":
			opts.DryRun = true
		case "--threshold":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--threshold needs a value")
			}
			i++
			n, perr := strconv.Atoi(args[i])
			if perr != nil {
				return opts, fmt.Errorf("--threshold: %w", perr)
			}
			opts.MinAssistantTurns = n
		case "--timeout":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--timeout needs a value")
			}
			i++
			d, perr := time.ParseDuration(args[i])
			if perr != nil {
				return opts, fmt.Errorf("--timeout: %w", perr)
			}
			opts.Timeout = d
		case "--quiet-for":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("--quiet-for needs a value")
			}
			i++
			d, perr := time.ParseDuration(args[i])
			if perr != nil {
				return opts, fmt.Errorf("--quiet-for: %w", perr)
			}
			opts.QuietFor = d
		default:
			if len(a) > 0 && a[0] == '-' {
				return opts, fmt.Errorf("unknown flag %q", a)
			}
			return opts, fmt.Errorf("backfill takes no positional arguments (got %q)", a)
		}
	}
	return opts, nil
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

// parseEnrichArgs parses `enrich [--repo <repo-key>] [--dry-run] [--timeout D]`.
func parseEnrichArgs(args []string) (synthesis.EnrichOptions, error) {
	var o synthesis.EnrichOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dry-run":
			o.DryRun = true
		case "--repo":
			i++
			if i >= len(args) {
				return o, fmt.Errorf("--repo needs a value")
			}
			o.Repo = args[i]
		case "--timeout":
			i++
			if i >= len(args) {
				return o, fmt.Errorf("--timeout needs a value")
			}
			d, err := time.ParseDuration(args[i])
			if err != nil {
				return o, fmt.Errorf("--timeout: %w", err)
			}
			o.Timeout = d
		default:
			return o, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return o, nil
}
