package synthesis

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
)

func frictionAnalysis(id, quote, file string) insights.AgentSessionAnalysis {
	a := analysisWith("/Users/dev/Developer/alpha", "/Users/dev/Developer/alpha")
	a.Stats.SessionID = id
	a.Stats.Start = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	a.Outcome = "fully_achieved"
	a.FrictionIncidents = []insights.FrictionIncident{{Type: "wrong_approach", OneLine: "x", EvidenceQuote: quote, File: file}}
	return a
}

func TestBuildBundleIdsAndRelativize(t *testing.T) {
	g := []insights.AgentSessionAnalysis{
		frictionAnalysis("bbb", "second quote here", "/Users/dev/Developer/alpha/.worktrees/w/apps/api/x.ts"),
		frictionAnalysis("aaa", "first quote here", "apps/ui/y.ts"),
	}
	b := BuildBundle("alpha", g)

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

func TestBuildBundleWindowIsChronological(t *testing.T) {
	// SessionID order is the REVERSE of Start order: the later-starting session
	// ("aaa") sorts first by SessionID, so a SessionID sort yields From>To.
	early := frictionAnalysis("zzz", "q", "apps/x.ts")
	early.Stats.Start = time.Date(2026, 6, 24, 0, 0, 0, 0, time.UTC)
	late := frictionAnalysis("aaa", "q", "apps/y.ts")
	late.Stats.Start = time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC)

	b := BuildBundle("alpha", []insights.AgentSessionAnalysis{early, late})

	if b.From != "2026-06-24" || b.To != "2026-06-30" {
		t.Errorf("window = %s–%s, want 2026-06-24–2026-06-30 (chronological, From<=To)", b.From, b.To)
	}
}

func TestBuildBundleRedactsHomePath(t *testing.T) {
	g := []insights.AgentSessionAnalysis{frictionAnalysis("aaa", "q", "/Users/dev/secret/notes.txt")}
	b := BuildBundle("alpha", g)
	if b.Friction[0].File != "[redacted]" {
		t.Errorf("home path outside repo = %q, want [redacted]", b.Friction[0].File)
	}
}

func readAnalysis(id string, reads int) insights.AgentSessionAnalysis {
	a := analysisWith("/Users/dev/Developer/alpha", "/Users/dev/Developer/alpha")
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

func TestComputeSignalsZeroHeavyFrictionDensity(t *testing.T) {
	var g []insights.AgentSessionAnalysis
	for i := 0; i < 27; i++ { // clean: zero friction, some assistant turns
		a := analysisWith("/Users/dev/Developer/alpha", "/Users/dev/Developer/alpha")
		a.Stats.SessionID = "clean" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		a.Stats.AssistantTurns = 10
		g = append(g, a)
	}
	for i := 0; i < 3; i++ { // frictional outliers: density 0.5 each
		a := analysisWith("/Users/dev/Developer/alpha", "/Users/dev/Developer/alpha")
		a.Stats.SessionID = "fric" + string(rune('a'+i))
		a.Stats.AssistantTurns = 10
		a.Stats.Rejections = 5
		g = append(g, a)
	}
	// Sanity: clean sessions have zero friction, outliers have nonzero density.
	for _, a := range g {
		total := a.Stats.Interrupts + a.Stats.Rejections + a.Stats.ToolErrors
		if strings.HasPrefix(a.Stats.SessionID, "clean") && total != 0 {
			t.Fatalf("session %s: expected zero friction, got %d", a.Stats.SessionID, total)
		}
		if strings.HasPrefix(a.Stats.SessionID, "fric") && total == 0 {
			t.Fatalf("session %s: expected nonzero friction", a.Stats.SessionID)
		}
	}
	var fd *OppSignal
	for i, s := range computeSignals(g) {
		_ = i
		if s.Kind == "friction_density" {
			s := s
			fd = &s
		}
	}
	if fd == nil {
		t.Fatal("friction_density signal suppressed when p90 collapses to 0")
	}
	if fd.Magnitude != 3 {
		t.Errorf("friction_density magnitude = %d, want 3", fd.Magnitude)
	}
}

func TestMechanicalFrictionSignalFloor(t *testing.T) {
	group := []insights.AgentSessionAnalysis{
		mechSession("s1", map[string]int{"edit_before_read": 1}, nil, nil),
		mechSession("s2", map[string]int{"wrong_cwd": 1}, nil, nil),
	}
	b := BuildBundle("r", group)
	for _, g := range b.Signals {
		if g.Kind == "mechanical_friction" {
			t.Errorf("signal emitted below floor: %+v", g)
		}
	}
}

func TestMechanicalFrictionSignalEmitted(t *testing.T) {
	group := []insights.AgentSessionAnalysis{
		mechSession("s1", map[string]int{"edit_before_read": 1}, nil, nil),
		mechSession("s2", map[string]int{"wrong_cwd": 1}, nil, nil),
		mechSession("s3", map[string]int{"permission": 2}, nil, nil),
	}
	b := BuildBundle("r", group)
	var found *OppSignal
	for i := range b.Signals {
		if b.Signals[i].Kind == "mechanical_friction" {
			found = &b.Signals[i]
		}
	}
	if found == nil {
		t.Fatal("mechanical_friction signal missing at floor")
	}
	if found.Magnitude != 3 || len(found.MemberSessions) != 3 {
		t.Errorf("signal = %+v, want magnitude 3", found)
	}
}

func TestBuildBundleSessionDates(t *testing.T) {
	group := []insights.AgentSessionAnalysis{
		{Stats: insights.AgentSessionStats{SessionID: "00000000-0000-4000-8000-000000000001",
			Start: time.Date(2026, 7, 3, 10, 0, 0, 0, time.UTC)}},
		{Stats: insights.AgentSessionStats{SessionID: "00000000-0000-4000-8000-000000000002",
			Start: time.Date(2026, 7, 9, 22, 30, 0, 0, time.UTC)}},
	}
	b := BuildBundle("r", group)
	want := map[string]string{
		"00000000-0000-4000-8000-000000000001": "2026-07-03",
		"00000000-0000-4000-8000-000000000002": "2026-07-09",
	}
	if !reflect.DeepEqual(b.SessionDates, want) {
		t.Errorf("SessionDates = %v, want %v", b.SessionDates, want)
	}
	data, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var rt EvidenceBundle
	if err := json.Unmarshal(data, &rt); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(rt.SessionDates, want) {
		t.Errorf("SessionDates lost in JSON round-trip: %v", rt.SessionDates)
	}
}
