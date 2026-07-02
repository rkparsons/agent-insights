package synthesis

import (
	"testing"
	"time"

	"tmux-ctrl/internal/insights"
)

func frictionAnalysis(id, quote, file string) insights.AgentSessionAnalysis {
	a := analysisWith("/Users/dev/Developer/client-project", "/Users/dev/Developer/client-project")
	a.Stats.SessionID = id
	a.Stats.Start = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	a.Outcome = "fully_achieved"
	a.FrictionIncidents = []insights.FrictionIncident{{Type: "wrong_approach", OneLine: "x", EvidenceQuote: quote, File: file}}
	return a
}

func TestBuildBundleIdsAndRelativize(t *testing.T) {
	g := []insights.AgentSessionAnalysis{
		frictionAnalysis("bbb", "second quote here", "/Users/dev/Developer/client-project/.worktrees/w/apps/api/x.ts"),
		frictionAnalysis("aaa", "first quote here", "apps/ui/y.ts"),
	}
	b := BuildBundle("client-project", g)

	if len(b.Friction) != 2 {
		t.Fatalf("friction items = %d, want 2", len(b.Friction))
	}
	// Deterministic order: sorted by session_id, so "aaa" first → F1.
	if b.Friction[0].ID != "F1" || b.Friction[0].SessionID != "aaa" {
		t.Errorf("first item = %+v, want F1/aaa (deterministic order)", b.Friction[0])
	}
	// Absolute worktree path relativized to repo-relative.
	if b.Friction[1].File != "apps/api/x.ts" {
		t.Errorf("file = %q, want apps/api/x.ts (relativized)", b.Friction[1].File)
	}
	if b.AnalyzedCount != 2 {
		t.Errorf("analyzed = %d, want 2", b.AnalyzedCount)
	}
}

func TestBuildBundleRedactsHomePath(t *testing.T) {
	g := []insights.AgentSessionAnalysis{frictionAnalysis("aaa", "q", "/Users/dev/secret/notes.txt")}
	b := BuildBundle("client-project", g)
	if b.Friction[0].File != "[redacted]" {
		t.Errorf("home path outside repo = %q, want [redacted]", b.Friction[0].File)
	}
}

func readAnalysis(id string, reads int) insights.AgentSessionAnalysis {
	a := analysisWith("/Users/dev/Developer/client-project", "/Users/dev/Developer/client-project")
	a.Stats.SessionID = id
	a.Stats.ToolCounts = map[string]int{"Read": reads}
	a.SessionType = "single_task"
	a.Outcome = "fully_achieved"
	return a
}

func TestComputeSignalsHighRead(t *testing.T) {
	var g []insights.AgentSessionAnalysis
	for i := 0; i < 9; i++ {
		g = append(g, readAnalysis("low"+string(rune('a'+i)), 1))
	}
	// 4 high-read sessions clear signalFloor(3) and land in the >= p90 tail.
	for i := 0; i < 4; i++ {
		g = append(g, readAnalysis("high"+string(rune('a'+i)), 40))
	}
	sigs := computeSignals(g)
	var hr *OppSignal
	for i := range sigs {
		if sigs[i].Kind == "high_read" {
			hr = &sigs[i]
		}
	}
	if hr == nil {
		t.Fatal("expected a high_read signal")
	}
	if hr.Magnitude < signalFloor {
		t.Errorf("high_read magnitude = %d, want >= %d", hr.Magnitude, signalFloor)
	}
	if hr.ID != "G1" {
		t.Errorf("first signal id = %q, want G1", hr.ID)
	}
}

func TestComputeSignalsBelowFloorOmitted(t *testing.T) {
	// Only 2 high-read sessions → below signalFloor → no signal emitted.
	g := []insights.AgentSessionAnalysis{readAnalysis("a", 1), readAnalysis("b", 1), readAnalysis("c", 40), readAnalysis("d", 40)}
	for _, s := range computeSignals(g) {
		if s.Kind == "high_read" {
			t.Errorf("high_read emitted with only 2 members (< floor): %+v", s)
		}
	}
}
