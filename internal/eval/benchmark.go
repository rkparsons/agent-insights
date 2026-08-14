package eval

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
	"github.com/rkparsons/agent-insights/internal/synthesis"
)

type BucketPopulations struct {
	RepoPath           string   `json:"repo_path"`
	AsConsumed         []string `json:"as_consumed"`
	Scoring            []string `json:"scoring"`
	Gaps               []string `json:"gaps"`
	WindowFrom         string   `json:"window_from"`
	WindowTo           string   `json:"window_to"`
	ExpectedAnalyzed   int      `json:"expected_analyzed"`
	ReconstructedCount int      `json:"reconstructed_count"`
	Resolved           bool     `json:"resolved"`
}

type Benchmark struct {
	FrozenAt time.Time                    `json:"frozen_at"`
	Buckets  map[string]BucketPopulations `json:"buckets"`
	Statuses map[string]string            `json:"statuses"`
	// NuanceWatermarks: per-target median nuance-pass count recorded when a
	// target's pass_at was recalibrated full->partial (spec amendment
	// 2026-07-09). Lives here, not in the rubric file, so watermark upkeep
	// never re-keys the rubric hash or its adjudications.
	NuanceWatermarks map[string]int `json:"nuance_watermarks,omitempty"`
}

// globalGroundTruthDir is the subdirectory of ground-truth/ that holds v2
// cross-repo snapshots (synthesis.Dir()'s own layout, frozen verbatim). It is
// NOT a repo bucket, and the v1 loader must skip it or every v2 freeze would
// card a phantom "global" repo.
const globalGroundTruthDir = "global"

// loadGroundTruth reads the newest RepoSynthesis per repo dir under
// dataDir/ground-truth (filenames are YYYY-MM-DD.json, so lexical desc ==
// chronological desc). A repo dir whose every .json file is unreadable or
// malformed is an error naming the dir, not a silently omitted bucket.
//
// These are the v1 (per-repo, theme-shaped) truths. They stay readable for
// historical records — the as_consumed control's pre-strip anchors come from
// them — while new freezes card the v2 global snapshot (loadGlobalGroundTruth).
func loadGroundTruth(dir string) (map[string]RepoSynthesis, error) {
	repoDirs, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]RepoSynthesis{}
	for _, rd := range repoDirs {
		if !rd.IsDir() || rd.Name() == globalGroundTruthDir {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(dir, rd.Name()))
		if err != nil {
			return nil, err
		}
		var names []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
				names = append(names, e.Name())
			}
		}
		sort.Sort(sort.Reverse(sort.StringSlice(names)))
		loaded := false
		for _, name := range names {
			raw, err := os.ReadFile(filepath.Join(dir, rd.Name(), name))
			if err != nil {
				continue
			}
			var s RepoSynthesis
			if err := json.Unmarshal(raw, &s); err != nil {
				continue
			}
			out[rd.Name()] = s
			loaded = true
			break
		}
		if !loaded && len(names) > 0 {
			return nil, fmt.Errorf("ground truth %s: all %d json file(s) unreadable or malformed", rd.Name(), len(names))
		}
	}
	return out, nil
}

// loadGlobalGroundTruth reads the newest v2 snapshot under
// <ground-truth>/global. Snapshot filenames are UTC instants
// (synthesis.snapshotTimeLayout), so lexical desc == chronological desc. ok is
// false (with a nil error) when no v2 run has been frozen yet — the v1 truths
// are then the only ground truth there is.
func loadGlobalGroundTruth(dir string) (insights.GlobalSynthesisJSON, bool, error) {
	entries, err := os.ReadDir(filepath.Join(dir, globalGroundTruthDir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return insights.GlobalSynthesisJSON{}, false, nil
		}
		return insights.GlobalSynthesisJSON{}, false, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, globalGroundTruthDir, name))
		if err != nil {
			continue
		}
		var s insights.GlobalSynthesisJSON
		if err := json.Unmarshal(raw, &s); err != nil {
			continue
		}
		return s, true, nil
	}
	if len(names) > 0 {
		return insights.GlobalSynthesisJSON{}, false, fmt.Errorf("ground truth %s: all %d snapshot(s) unreadable or malformed", globalGroundTruthDir, len(names))
	}
	return insights.GlobalSynthesisJSON{}, false, nil
}

// loadBenchmark reads dataDir/benchmark.json if present. ok is false (with a
// nil error) when there is no prior freeze to build on.
func loadBenchmark(dataDir string) (Benchmark, bool, error) {
	raw, err := os.ReadFile(filepath.Join(dataDir, "benchmark.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Benchmark{}, false, nil
		}
		return Benchmark{}, false, err
	}
	var b Benchmark
	if err := json.Unmarshal(raw, &b); err != nil {
		return Benchmark{}, false, err
	}
	return b, true, nil
}

// allBucketsResolved reports whether every bucket in b reconstructed its
// expected analysis count — the gate for treating an existing benchmark.json
// as canonical and reusable rather than rebuilding it from the live pool.
func allBucketsResolved(b Benchmark) bool {
	for _, bp := range b.Buckets {
		if !bp.Resolved {
			return false
		}
	}
	return true
}

// BuildBenchmark reconstructs, per ground-truth bucket, the as_consumed
// population (all bucket analyses; falling back to Start < GeneratedAt when the
// count disagrees — the report run applied no window filter, so Window is
// descriptive and possibly reversed) and the scoring population (as_consumed
// minus meta-sessions). An unresolved count mismatch is returned as a problem;
// its populations are still written for inspection.
//
// global, when non-nil, is the frozen v2 snapshot and is the authority: its
// repos[] names the buckets and carries each repo's analyzed count and window.
// truths (the v1 per-repo reports) are the fallback for data repos frozen
// before the v2 cutover — the buckets must never come from both, or a repo
// present in each would be reconstructed twice against different counts.
func BuildBenchmark(frozenAt time.Time, analyses []insights.AgentSessionAnalysis, truths map[string]RepoSynthesis, global *insights.GlobalSynthesisJSON, cfg insights.Config) (Benchmark, []string) {
	b := Benchmark{FrozenAt: frozenAt, Buckets: map[string]BucketPopulations{}, Statuses: map[string]string{}}
	var problems []string
	add := func(repo string, expected int, from, to string, generatedAt time.Time) {
		bp, problem := bucketPopulations(repo, expected, from, to, generatedAt, analyses, cfg)
		if problem != "" {
			problems = append(problems, problem)
		}
		b.Buckets[repo] = bp
	}
	if global != nil {
		for _, r := range global.Repos {
			add(r.Key, r.AnalyzedCount, r.Window.From, r.Window.To, global.GeneratedAt)
		}
	} else {
		for repo, t := range truths {
			add(repo, t.Window.AnalyzedCount, t.Window.From, t.Window.To, t.GeneratedAt)
		}
	}
	sort.Strings(problems)
	return b, problems
}

// bucketPopulations reconstructs one bucket against its ground-truth analyzed
// count. problem is non-empty when the count could not be reconciled; the
// populations are still returned for inspection.
func bucketPopulations(repo string, expected int, windowFrom, windowTo string, generatedAt time.Time,
	analyses []insights.AgentSessionAnalysis, cfg insights.Config) (BucketPopulations, string) {
	var bucket []insights.AgentSessionAnalysis
	for _, a := range analyses {
		if synthesis.RepoKey(a, cfg) == repo {
			bucket = append(bucket, a)
		}
	}
	candidates := bucket
	if len(candidates) != expected {
		var filtered []insights.AgentSessionAnalysis
		for _, a := range bucket {
			if a.Stats.Start.Before(generatedAt) {
				filtered = append(filtered, a)
			}
		}
		if len(filtered) == expected {
			candidates = filtered
		}
	}
	bp := BucketPopulations{
		ExpectedAnalyzed:   expected,
		ReconstructedCount: len(candidates),
		Resolved:           len(candidates) == expected,
		WindowFrom:         windowFrom,
		WindowTo:           windowTo,
	}
	if bp.WindowFrom > bp.WindowTo {
		bp.WindowFrom, bp.WindowTo = bp.WindowTo, bp.WindowFrom
	}
	for _, a := range candidates {
		bp.AsConsumed = append(bp.AsConsumed, a.Stats.SessionID)
		if bp.RepoPath == "" && a.Stats.Repo != "" {
			bp.RepoPath = stripWorktree(a.Stats.Repo)
		}
		if !insights.IsMeta(a.Stats) {
			bp.Scoring = append(bp.Scoring, a.Stats.SessionID)
		}
	}
	sort.Strings(bp.AsConsumed)
	sort.Strings(bp.Scoring)
	if bp.Resolved {
		return bp, ""
	}
	return bp, fmt.Sprintf("%s: reconstructed %d analyses, report says %d", repo, len(candidates), expected)
}

// stripWorktree truncates a path at a "/.worktrees/" segment, if present —
// the same convention synthesis.RepoKey uses for grouping, applied locally
// here so a worktree checkout's path can't land in benchmark.json's
// RepoPath.
func stripWorktree(p string) string {
	if i := strings.Index(p, "/.worktrees/"); i >= 0 {
		return p[:i]
	}
	return p
}
