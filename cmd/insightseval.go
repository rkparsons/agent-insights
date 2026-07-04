package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"tmux-ctrl/internal/insightseval"
)

// RunInsightsEval dispatches `tmux-ctrl insights eval <freeze|outcome|statuses>`.
func RunInsightsEval(args []string) {
	usage := "usage: tmux-ctrl insights eval freeze [--data <dir>] | outcome [--data <dir>] [--cache <dir>] [--scope l2|full] [--population scoring|as_consumed] [--samples N] [--l1-sample] | statuses [seed] [--data <dir>]"
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	switch args[0] {
	case "freeze":
		runEvalFreeze(args[1:])
	case "outcome":
		runEvalOutcome(args[1:])
	case "statuses":
		runEvalStatuses(args[1:])
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
}

func defaultDataDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Developer", "insights-eval-data")
}

func defaultCacheDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, "tmux-ctrl", "insights-eval")
}

// runEvalFreeze is the existing freeze body, verbatim, with the final summary
// line switched to poolSummaryMessage.
func runEvalFreeze(args []string) {
	dataDir := defaultDataDir()
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--data":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "tmux-ctrl insights eval: --data needs a value")
				os.Exit(2)
			}
			dataDir = args[i]
		default:
			fmt.Fprintf(os.Stderr, "tmux-ctrl insights eval freeze: unknown flag %q\n", args[i])
			os.Exit(2)
		}
	}
	rep, err := insightseval.RunFreeze(dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tmux-ctrl insights eval freeze: %v\n", err)
		os.Exit(1)
	}
	gt := fmt.Sprintf("%d ground-truth files", rep.GroundTruth)
	if rep.GroundTruthRetained {
		gt = "ground-truth retained (canonical)"
	}
	fmt.Fprintf(os.Stderr, "freeze: %d sessions · %d sidechains · %s · %d config files\n",
		len(rep.Manifest.Entries), len(rep.Manifest.Sidechains), gt, rep.ConfigCopied)
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
	suffix := ""
	if len(rep.Issues.Gaps) > 0 {
		suffix = fmt.Sprintf(" apart from %d recorded gaps", len(rep.Issues.Gaps))
	}
	fmt.Fprintf(os.Stderr, "freeze: clean%s · %s\n", suffix, poolSummaryMessage(rep.PoolRetained, rep.PoolCopied))
}

// poolSummaryMessage distinguishes a fresh v1 write from a retained
// (write-once) v1 on re-runs, where PoolCopied is legitimately 0.
func poolSummaryMessage(retained bool, copied int) string {
	if retained {
		return "baseline-pool/v1 retained from a prior run (write-once)"
	}
	return fmt.Sprintf("baseline-pool/v1 written (%d analyses)", copied)
}

func parseOutcomeArgs(args []string) (insightseval.OutcomeOptions, error) {
	opts := insightseval.OutcomeOptions{DataDir: defaultDataDir(), CacheDir: defaultCacheDir()}
	for i := 0; i < len(args); i++ {
		next := func() (string, error) {
			i++
			if i >= len(args) {
				return "", fmt.Errorf("%s needs a value", args[i-1])
			}
			return args[i], nil
		}
		var err error
		switch args[i] {
		case "--data":
			opts.DataDir, err = next()
		case "--cache":
			opts.CacheDir, err = next()
		case "--scope":
			opts.Scope, err = next()
		case "--population":
			opts.Population, err = next()
		case "--samples":
			var v string
			if v, err = next(); err == nil {
				opts.Samples, err = strconv.Atoi(v)
			}
		case "--l1-sample":
			opts.L1Sample = true
		default:
			return opts, fmt.Errorf("unknown flag %q", args[i])
		}
		if err != nil {
			return opts, err
		}
	}
	return opts, nil
}

func runEvalOutcome(args []string) {
	opts, err := parseOutcomeArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tmux-ctrl insights eval outcome: %v\n", err)
		os.Exit(2)
	}
	opts.ClaudeVersion, err = insightseval.ClaudeVersionString()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tmux-ctrl insights eval outcome: %v\n", err)
		os.Exit(1)
	}
	rec, err := insightseval.RunOutcome(context.Background(), opts)
	for _, w := range rec.Warnings {
		fmt.Fprintf(os.Stderr, "outcome: WARN %s\n", w)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "tmux-ctrl insights eval outcome: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "outcome: scope=%s population=%s pool=%s samples=%d · cache %d hits / %d misses\n",
		rec.Scope, rec.Population, rec.PoolVersion, rec.Samples, rec.CacheHits, rec.CacheMisses)
	for _, b := range rec.Buckets {
		fresh := 0
		for _, s := range b.Samples {
			if s.Fresh {
				fresh++
			}
		}
		fmt.Fprintf(os.Stderr, "outcome: %s · %d sessions (%d gap-fallback) · %d samples (%d fresh)\n",
			b.Bucket, len(b.Population), len(b.GapFallbacks), len(b.Samples), fresh)
	}
	if rec.L1Sample != nil {
		fmt.Fprintf(os.Stderr, "outcome: l1-sample · %d judged (%d cached)\n", rec.L1Sample.Analyzed, rec.L1Sample.Hits)
	}
	fmt.Fprintf(os.Stderr, "outcome: record %s\n", rec.RecordPath)
}

func runEvalStatuses(args []string) {
	dataDir := defaultDataDir()
	seed := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "seed":
			seed = true
		case "--data":
			i++
			if i >= len(args) {
				fmt.Fprintln(os.Stderr, "tmux-ctrl insights eval statuses: --data needs a value")
				os.Exit(2)
			}
			dataDir = args[i]
		default:
			fmt.Fprintf(os.Stderr, "tmux-ctrl insights eval statuses: unknown arg %q\n", args[i])
			os.Exit(2)
		}
	}
	if seed {
		added, err := insightseval.SeedStatuses(dataDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tmux-ctrl insights eval statuses seed: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "statuses: seeded %d (existing entries untouched)\n", added)
	}
	statuses, err := insightseval.Statuses(dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tmux-ctrl insights eval statuses: %v\n", err)
		os.Exit(1)
	}
	ids := make([]string, 0, len(statuses))
	for id := range statuses {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		fmt.Fprintf(os.Stderr, "statuses: %s = %s\n", id, statuses[id])
	}
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
