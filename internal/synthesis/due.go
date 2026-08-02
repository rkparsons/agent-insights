package synthesis

import (
	"sort"
	"time"

	"tmux-ctrl/internal/insights"
)

const DefaultCadence = 14 * 24 * time.Hour

// DueRepos returns the repo keys due for synthesis, sorted ascending. A repo
// is due when it has never been synthesized (groups are already
// min-sessions-filtered), or its latest synthesis is at least cadence old AND
// the analysis set changed since (AnalyzedCount was len(group) at generation;
// != not > because a meta-purge can shrink the store).
func DueRepos(groups map[string][]insights.AgentSessionAnalysis, syntheses []RepoSynthesis, cadence time.Duration, now time.Time) []string {
	latest := make(map[string]RepoSynthesis, len(syntheses))
	for _, s := range syntheses {
		latest[s.Repo] = s
	}
	var due []string
	for k, group := range groups {
		s, ok := latest[k]
		if !ok {
			due = append(due, k)
			continue
		}
		if now.Sub(s.GeneratedAt) >= cadence && len(group) != s.Window.AnalyzedCount {
			due = append(due, k)
		}
	}
	sort.Strings(due)
	return due
}
