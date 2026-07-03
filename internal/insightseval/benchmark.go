package insightseval

import (
	"encoding/json"
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
}

// loadGroundTruth reads the newest RepoSynthesis per repo dir under
// dataDir/ground-truth (filenames are YYYY-MM-DD.json, so lexical desc ==
// chronological desc — same convention as synthesis.LoadSyntheses).
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
			break
		}
	}
	return out, nil
}

// BuildBenchmark reconstructs, per ground-truth bucket, the as_consumed
// population (all bucket analyses; falling back to Start < GeneratedAt when the
// count disagrees — the report run applied no window filter, so Window is
// descriptive and possibly reversed) and the scoring population (as_consumed
// minus meta-sessions). An unresolved count mismatch is returned as a problem;
// its populations are still written for inspection.
func BuildBenchmark(frozenAt time.Time, analyses []insights.AgentSessionAnalysis, truths map[string]synthesis.RepoSynthesis) (Benchmark, []string) {
	b := Benchmark{FrozenAt: frozenAt, Buckets: map[string]BucketPopulations{}, Statuses: map[string]string{}}
	var problems []string
	for repo, t := range truths {
		var bucket []insights.AgentSessionAnalysis
		for _, a := range analyses {
			if synthesis.RepoKey(a) == repo {
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
				bp.RepoPath = a.Stats.Repo
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
