package synthesis

import (
	"reflect"
	"testing"
	"time"

	"tmux-ctrl/internal/insights"
)

func TestDueRepos(t *testing.T) {
	now := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	mk := func(n int) []insights.AgentSessionAnalysis {
		out := make([]insights.AgentSessionAnalysis, n)
		return out
	}
	syn := func(repo string, ageDays, analyzed int) RepoSynthesis {
		return RepoSynthesis{Repo: repo, GeneratedAt: now.Add(-time.Duration(ageDays) * 24 * time.Hour),
			Window: Window{AnalyzedCount: analyzed}}
	}
	groups := map[string][]insights.AgentSessionAnalysis{
		"never-synthesized": mk(12),
		"young":             mk(20),
		"old-no-new":        mk(30),
		"old-with-new":      mk(31),
		"old-shrunk":        mk(9),
	}
	syntheses := []RepoSynthesis{
		syn("young", 3, 15),
		syn("old-no-new", 20, 30),
		syn("old-with-new", 20, 30),
		syn("old-shrunk", 20, 12),
	}
	got := DueRepos(groups, syntheses, DefaultCadence, now)
	want := []string{"never-synthesized", "old-shrunk", "old-with-new"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
