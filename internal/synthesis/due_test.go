package synthesis

import (
	"reflect"
	"testing"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
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

// mkFresh builds n analyses for repo, all stamped with mtime as their
// transcript mtime and no AnalyzedAt — the fallback path. Stats.Repo is set
// directly so RepoKey resolves without cwd heuristics.
func mkFresh(repo string, n int, mtime time.Time) []insights.AgentSessionAnalysis {
	out := make([]insights.AgentSessionAnalysis, n)
	for i := range out {
		out[i].Stats.Repo = repo
		out[i].TranscriptMtime = mtime
	}
	return out
}

// mkAnalyzed builds n analyses whose transcripts are old but whose analyses
// were written at analyzedAt — the backfill-onboarding shape.
func mkAnalyzed(repo string, n int, transcriptMtime, analyzedAt time.Time) []insights.AgentSessionAnalysis {
	out := mkFresh(repo, n, transcriptMtime)
	for i := range out {
		out[i].AnalyzedAt = analyzedAt
	}
	return out
}

func TestGlobalDue(t *testing.T) {
	now := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)
	cfg := insights.Config{CadenceDays: 14, MinSessions: 10, DueNewSessions: 10}

	t.Run("sums across repos", func(t *testing.T) {
		lastGenerated := now.Add(-30 * 24 * time.Hour)
		fresh := now.Add(-1 * time.Hour)
		stale := lastGenerated.Add(-1 * time.Hour)
		var analyses []insights.AgentSessionAnalysis
		// repo-a: 8 new + 2 old (meets the 10-session bundle floor).
		analyses = append(analyses, mkFresh("repo-a", 8, fresh)...)
		analyses = append(analyses, mkFresh("repo-a", 2, stale)...)
		// repo-b: 7 new + 3 old (also meets the floor).
		analyses = append(analyses, mkFresh("repo-b", 7, fresh)...)
		analyses = append(analyses, mkFresh("repo-b", 3, stale)...)

		due, contributing := GlobalDue(analyses, cfg, lastGenerated, now)
		if !due {
			t.Fatalf("due = false, want true (8+7=15 >= threshold 10)")
		}
		want := []string{"repo-a", "repo-b"}
		if !reflect.DeepEqual(contributing, want) {
			t.Fatalf("contributing = %v, want %v", contributing, want)
		}
	})

	t.Run("sub-floor repo excluded", func(t *testing.T) {
		lastGenerated := now.Add(-30 * 24 * time.Hour)
		fresh := now.Add(-1 * time.Hour)
		var analyses []insights.AgentSessionAnalysis
		// alpha: 10 total (floor met), only 3 fresh.
		analyses = append(analyses, mkFresh("alpha", 3, fresh)...)
		analyses = append(analyses, mkFresh("alpha", 7, lastGenerated.Add(-time.Hour))...)
		// beta: 8 total, all fresh — below the 10-session floor, so its 8 new
		// sessions must never enter a bundle and must not count toward due.
		analyses = append(analyses, mkFresh("beta", 8, fresh)...)

		due, contributing := GlobalDue(analyses, cfg, lastGenerated, now)
		if due {
			t.Fatalf("due = true, want false (beta excluded leaves only alpha's 3 < threshold 10)")
		}
		want := []string{"alpha"}
		if !reflect.DeepEqual(contributing, want) {
			t.Fatalf("contributing = %v, want %v (beta must be excluded, sub-floor)", contributing, want)
		}
	})

	t.Run("meta-purge simulation still due", func(t *testing.T) {
		lastGenerated := now.Add(-30 * 24 * time.Hour)
		fresh := now.Add(-1 * time.Hour)
		stale := lastGenerated.Add(-1 * time.Hour)
		// Only 12 analyses survive in the pool (as if a meta-purge shrank a
		// once-larger store) — but correctness here never touches a prior
		// count, only per-analysis timestamps, so 10 fresh ones are still due.
		var analyses []insights.AgentSessionAnalysis
		analyses = append(analyses, mkFresh("repo-a", 10, fresh)...)
		analyses = append(analyses, mkFresh("repo-a", 2, stale)...)

		due, contributing := GlobalDue(analyses, cfg, lastGenerated, now)
		if !due {
			t.Fatalf("due = false, want true (10 fresh timestamps >= threshold 10)")
		}
		want := []string{"repo-a"}
		if !reflect.DeepEqual(contributing, want) {
			t.Fatalf("contributing = %v, want %v", contributing, want)
		}
	})

	t.Run("backfilled old transcripts count as new", func(t *testing.T) {
		// Onboarding shape: a year-old transcript pool analyzed today. Freshness
		// keyed on the transcript's own mtime would report nothing new and never
		// let a first global run happen.
		lastGenerated := now.Add(-30 * 24 * time.Hour)
		ancient := now.Add(-365 * 24 * time.Hour)
		analyses := mkAnalyzed("repo-a", 12, ancient, now.Add(-1*time.Hour))

		due, contributing := GlobalDue(analyses, cfg, lastGenerated, now)
		if !due {
			t.Fatalf("due = false, want true (12 analyses written after the snapshot)")
		}
		want := []string{"repo-a"}
		if !reflect.DeepEqual(contributing, want) {
			t.Fatalf("contributing = %v, want %v", contributing, want)
		}
	})

	t.Run("analyzed before the snapshot is not new", func(t *testing.T) {
		// The mirror case: recently-touched transcripts (a rebase, a copy) whose
		// analyses predate the snapshot must not manufacture due pressure.
		lastGenerated := now.Add(-30 * 24 * time.Hour)
		analyses := mkAnalyzed("repo-a", 12, now.Add(-1*time.Hour), lastGenerated.Add(-24*time.Hour))

		due, contributing := GlobalDue(analyses, cfg, lastGenerated, now)
		if due {
			t.Fatalf("due = true, want false (every analysis predates the snapshot)")
		}
		if len(contributing) != 0 {
			t.Fatalf("contributing = %v, want none", contributing)
		}
	})

	t.Run("cadence not yet elapsed not due regardless", func(t *testing.T) {
		lastGenerated := now.Add(-1 * 24 * time.Hour) // cadence is 14 days
		fresh := now.Add(-1 * time.Hour)
		analyses := mkFresh("repo-a", 20, fresh) // plenty fresh; floor and threshold both cleared

		due, _ := GlobalDue(analyses, cfg, lastGenerated, now)
		if due {
			t.Fatalf("due = true, want false (cadence not elapsed: 1 day < 14 days)")
		}
	})

	t.Run("zero-value lastGenerated due on threshold alone", func(t *testing.T) {
		fresh := now.Add(-1 * time.Hour)
		analyses := mkFresh("repo-a", 10, fresh)

		due, contributing := GlobalDue(analyses, cfg, time.Time{}, now)
		if !due {
			t.Fatalf("due = false, want true (no prior snapshot, qualifying total 10 >= threshold 10)")
		}
		want := []string{"repo-a"}
		if !reflect.DeepEqual(contributing, want) {
			t.Fatalf("contributing = %v, want %v", contributing, want)
		}
	})
}
