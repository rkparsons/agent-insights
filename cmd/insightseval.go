package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"tmux-ctrl/internal/insightseval"
)

// RunInsightsEval dispatches `tmux-ctrl insights eval <freeze|outcome|score|adjudicate|probes|statuses>`.
func RunInsightsEval(args []string) {
	usage := "usage: tmux-ctrl insights eval freeze [--data <dir>] | outcome [--data <dir>] [--cache <dir>] [--scope l2|full] [--population scoring|as_consumed] [--samples N] [--l1-sample] | score [--data <dir>] [--cache <dir>] [--record <path>] [--repeats N] | adjudicate <key-prefix> <accept|reject> [--note <s>] [--data <dir>] [--cache <dir>] | probes [--repeats N] [--data <dir>] [--cache <dir>] | statuses [seed] [--data <dir>]"
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	switch args[0] {
	case "freeze":
		runEvalFreeze(args[1:])
	case "outcome":
		runEvalOutcome(args[1:])
	case "score":
		runEvalScore(args[1:])
	case "adjudicate":
		runEvalAdjudicate(args[1:])
	case "probes":
		runEvalProbes(args[1:])
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

func parseScoreArgs(args []string) (insightseval.ScoreOptions, error) {
	opts := insightseval.ScoreOptions{DataDir: defaultDataDir(), CacheDir: defaultCacheDir()}
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
		case "--record":
			opts.RecordPath, err = next()
		case "--repeats":
			var v string
			if v, err = next(); err == nil {
				opts.Repeats, err = strconv.Atoi(v)
			}
		default:
			return opts, fmt.Errorf("unknown flag %q", args[i])
		}
		if err != nil {
			return opts, err
		}
	}
	return opts, nil
}

func runEvalScore(args []string) {
	opts, err := parseScoreArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tmux-ctrl insights eval score: %v\n", err)
		os.Exit(2)
	}
	opts.ClaudeVersion, err = insightseval.ClaudeVersionString()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tmux-ctrl insights eval score: %v\n", err)
		os.Exit(1)
	}
	v, arts, err := insightseval.ScoreRun(context.Background(), opts)
	for _, w := range v.Warnings {
		fmt.Fprintf(os.Stderr, "score: WARN %s\n", w)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "tmux-ctrl insights eval score: %v\n", err)
		os.Exit(1)
	}
	printVerdict(v, arts)
}

func printVerdict(v insightseval.Verdict, arts insightseval.ScoreArtifacts) {
	status := "PASS"
	if v.HardFail {
		status = "HARD FAIL"
	}
	fmt.Fprintf(os.Stderr, "score: %s · record %s · population=%s scope=%s pool=%s\n",
		status, v.RecordName, v.Tuple.Population, v.Tuple.Scope, v.Tuple.PoolVersion)
	for _, r := range v.HardFailReasons {
		fmt.Fprintf(os.Stderr, "score: FAIL %s\n", r)
	}
	fmt.Fprintf(os.Stderr, "score: part A weighted recall %.2f (%d/%d targets)\n",
		v.PartA.WeightedRecall, v.PartA.Passed, v.PartA.Scored)
	for _, tv := range v.Targets {
		mark := "MISS"
		switch {
		case tv.Pass:
			mark = "pass"
		case tv.ProvisionalFail:
			mark = "PROVISIONAL-FAIL (card pending)"
		case tv.MeetsExpectation:
			mark = "expected"
		}
		fmt.Fprintf(os.Stderr, "score: %-6s %-22s %-16s %s (agreement %.2f)\n",
			tv.ID, tv.Status, tv.Granularity, mark, tv.SampleAgreement)
	}
	if len(v.PartB) > 0 {
		fmt.Fprintf(os.Stderr, "score: part B gap progress: %v\n", v.PartB)
	}
	for _, n := range v.Negatives {
		fmt.Fprintf(os.Stderr, "score: NEGATIVE VIOLATION %s on samples %v\n", n.RubricID, n.SampleIndexes)
	}
	if v.Delta != nil {
		if v.Delta.FreshBaseline {
			fmt.Fprintln(os.Stderr, "score: delta: fresh baseline (no comparable prior verdict)")
		} else {
			fmt.Fprintf(os.Stderr, "score: delta vs %s: %d flip(s)\n", v.Delta.BaselineRun, len(v.Delta.Flips))
			for _, f := range v.Delta.Flips {
				fmt.Fprintf(os.Stderr, "score: FLIP %s %s → %s\n", f.TargetID, f.From, f.To)
			}
		}
	}
	fmt.Fprintf(os.Stderr, "score: %d card(s) → %s\n", v.CardCount, arts.CardsDir+"/cards.md")
	if arts.RunsPath != "" {
		fmt.Fprintf(os.Stderr, "score: verdict committed → %s\n", arts.RunsPath)
	}
}

func runEvalAdjudicate(args []string) {
	var positional []string
	note := ""
	dataDir, cacheDir := defaultDataDir(), defaultCacheDir()
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--note", "--data", "--cache":
			flag := args[i]
			i++
			if i >= len(args) {
				fmt.Fprintf(os.Stderr, "tmux-ctrl insights eval adjudicate: %s needs a value\n", flag)
				os.Exit(2)
			}
			switch flag {
			case "--note":
				note = args[i]
			case "--data":
				dataDir = args[i]
			case "--cache":
				cacheDir = args[i]
			}
		default:
			positional = append(positional, args[i])
		}
	}
	if len(positional) != 2 || (positional[1] != "accept" && positional[1] != "reject") {
		fmt.Fprintln(os.Stderr, "usage: tmux-ctrl insights eval adjudicate <key-prefix> <accept|reject> [--note <s>] [--data <dir>] [--cache <dir>]")
		os.Exit(2)
	}
	card, err := insightseval.FindCardByPrefix(cacheDir, positional[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "tmux-ctrl insights eval adjudicate: %v\n", err)
		os.Exit(1)
	}
	a := insightseval.Adjudication{Key: card.Key, Decision: positional[1], Note: note, DecidedAt: time.Now().UTC()}
	if err := insightseval.SaveAdjudication(dataDir, a); err != nil {
		fmt.Fprintf(os.Stderr, "tmux-ctrl insights eval adjudicate: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "adjudicate: %s %s/%s = %s (applies from the next score run)\n",
		positional[0], card.TargetID, card.Trigger, positional[1])
}

func runEvalProbes(args []string) {
	opts, err := parseScoreArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tmux-ctrl insights eval probes: %v\n", err)
		os.Exit(2)
	}
	opts.ClaudeVersion, err = insightseval.ClaudeVersionString()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tmux-ctrl insights eval probes: %v\n", err)
		os.Exit(1)
	}
	probes, err := insightseval.ProbeRun(context.Background(), opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tmux-ctrl insights eval probes: %v\n", err)
		os.Exit(1)
	}
	failed := false
	for _, p := range probes {
		verdict := "PASS"
		if !p.Pass {
			verdict, failed = "FAIL", true
		}
		fmt.Fprintf(os.Stderr, "probes: %-16s %s · majority %s over %v (rubric %s)\n",
			p.Class, verdict, p.Majority, p.Granularities, p.RubricID)
	}
	if failed {
		os.Exit(1)
	}
}
