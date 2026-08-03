package insightseval

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"tmux-ctrl/internal/insights"
	"tmux-ctrl/internal/synthesis"
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

// loadGroundTruth reads the newest RepoSynthesis per repo dir under
// dataDir/ground-truth (filenames are YYYY-MM-DD.json, so lexical desc ==
// chronological desc — same convention as synthesis.LoadSyntheses). A repo
// dir whose every .json file is unreadable or malformed is an error naming
// the dir, not a silently omitted bucket.
func loadGroundTruth(dir string) (map[string]synthesis.RepoSynthesis, error) {
	repoDirs, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]synthesis.RepoSynthesis{}
	for _, rd := range repoDirs {
		if !rd.IsDir() {
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
			var s synthesis.RepoSynthesis
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
func BuildBenchmark(frozenAt time.Time, analyses []insights.AgentSessionAnalysis, truths map[string]synthesis.RepoSynthesis) (Benchmark, []string) {
	icfg, _ := insights.LoadConfig() // best-effort; grouping still works unaliased on error
	b := Benchmark{FrozenAt: frozenAt, Buckets: map[string]BucketPopulations{}, Statuses: map[string]string{}}
	var problems []string
	for repo, t := range truths {
		var bucket []insights.AgentSessionAnalysis
		for _, a := range analyses {
			if synthesis.RepoKey(a, icfg.Aliases) == repo {
				bucket = append(bucket, a)
			}
		}
		candidates := bucket
		if len(candidates) != t.Window.AnalyzedCount {
			var filtered []insights.AgentSessionAnalysis
			for _, a := range bucket {
				if a.Stats.Start.Before(t.GeneratedAt) {
					filtered = append(filtered, a)
				}
			}
			if len(filtered) == t.Window.AnalyzedCount {
				candidates = filtered
			}
		}
		bp := BucketPopulations{
			ExpectedAnalyzed:   t.Window.AnalyzedCount,
			ReconstructedCount: len(candidates),
			Resolved:           len(candidates) == t.Window.AnalyzedCount,
		}
		bp.WindowFrom, bp.WindowTo = t.Window.From, t.Window.To
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
		if !bp.Resolved {
			problems = append(problems, fmt.Sprintf("%s: reconstructed %d analyses, report says %d", repo, len(candidates), t.Window.AnalyzedCount))
		}
		b.Buckets[repo] = bp
	}
	sort.Strings(problems)
	return b, problems
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
