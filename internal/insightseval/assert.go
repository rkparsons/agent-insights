package insightseval

import (
	"sort"

	"tmux-ctrl/internal/insights"
)

type FreezeIssues struct {
	Gaps            []string `json:"gaps"`
	Skews           []string `json:"skews"`
	CountMismatches []string `json:"count_mismatches"`
}

func (i FreezeIssues) Clean() bool {
	return len(i.Gaps) == 0 && len(i.Skews) == 0 && len(i.CountMismatches) == 0
}

// Blocking reports whether the baseline pool must be withheld. Gaps
// (transcripts pruned before the freeze ever ran) are recorded but never
// blocking — a no-gaps gate can never pass once a transcript is gone.
func (i FreezeIssues) Blocking() bool {
	return len(i.Skews) > 0 || len(i.CountMismatches) > 0
}

// AssertFrozen verifies every benchmark id has a frozen corpus entry (gaps) and
// that the frozen transcript's mtime equals the pool analysis's stamped mtime
// (skews — the judged fields never saw content appended after analysis).
// Resolution for a skew: `tmux-ctrl insights analyze <id>`, then re-freeze.
func AssertFrozen(b Benchmark, m Manifest, countProblems []string) FreezeIssues {
	entries := map[string]ManifestEntry{}
	for _, e := range m.Entries {
		entries[e.SessionID] = e
	}
	iss := FreezeIssues{CountMismatches: countProblems}
	for repo, bp := range b.Buckets {
		for _, id := range bp.AsConsumed {
			e, ok := entries[id]
			if !ok {
				iss.Gaps = append(iss.Gaps, repo+"/"+id)
				continue
			}
			if mt, ok := insights.ReadAnalysisMtime(id); ok && !mt.Equal(e.Mtime) {
				iss.Skews = append(iss.Skews, repo+"/"+id)
			}
		}
	}
	sort.Strings(iss.Gaps)
	sort.Strings(iss.Skews)
	return iss
}
