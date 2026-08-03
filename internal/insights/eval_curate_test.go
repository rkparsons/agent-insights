package insights

import (
	"testing"

	"tmux-ctrl/internal/transcript"
)

func stat(id string, turns, toolErrors, interrupts, rejections, edits, writes int, repo, cwd string, bytes int64) sessionStat {
	return sessionStat{
		Ref:   transcript.TranscriptRef{SessionID: id, Path: "/p/" + id + ".jsonl"},
		Bytes: bytes,
		Stats: AgentSessionStats{
			SessionID: id, Repo: repo, Cwd: cwd,
			AssistantTurns: turns, ToolErrors: toolErrors, Interrupts: interrupts,
			Rejections: rejections, Edits: edits, Writes: writes,
		},
	}
}

func cellsByID(cs []curatedSession) map[string]string {
	m := map[string]string{}
	for _, c := range cs {
		m[c.Ref.SessionID] = c.Cell
	}
	return m
}

func TestCurateDeterministicAndStratified(t *testing.T) {
	pool := []sessionStat{
		stat("o", 999, 0, 0, 0, 0, 0, "alpha", "/h", 50_000_000),     // outlier (max turns)
		stat("m", 30, 0, 0, 0, 1, 0, "x", "/work/insights-gen", 100), // meta (cwd)
		stat("zs", 3, 0, 0, 0, 0, 0, "alpha", "/h", 100),             // zero short
		stat("zq", 6, 0, 0, 0, 0, 0, "alpha", "/h", 100),             // zero quick_question
		stat("ze", 20, 0, 0, 0, 0, 0, "alpha", "/h", 100),            // zero exploration
		stat("zi", 20, 0, 0, 0, 4, 1, "alpha", "/h", 100),            // zero implementation
		stat("zl", 60, 0, 0, 0, 0, 0, "alpha", "/h", 100),            // zero long
		stat("zx1", 25, 0, 0, 0, 0, 0, "alpha", "/h", 100),           // gap fill
		stat("zx2", 26, 0, 0, 0, 0, 0, "alpha", "/h", 100),           // gap fill
		stat("fm", 30, 2, 0, 0, 1, 0, "alpha", "/h", 100),            // frictionful medium
		stat("fl", 70, 0, 1, 0, 1, 0, "alpha", "/h", 100),            // frictionful long
		stat("u", 40, 1, 0, 0, 0, 0, "", "/h", 100),                  // unmatched repo + friction
		stat("g1", 15, 0, 0, 0, 1, 0, "alpha", "/h", 100),            // zero impl (lower id than zi)
		stat("g2", 16, 0, 0, 0, 1, 0, "alpha", "/h", 100),            // zero extra
	}

	got := curate(pool)
	cells := cellsByID(got)

	if cells["o"] != "outlier" {
		t.Errorf("expected o=outlier, got %q", cells["o"])
	}
	if cells["m"] != "meta" {
		t.Errorf("expected m=meta, got %q", cells["m"])
	}
	// Every selected session is unique.
	seen := map[string]bool{}
	for _, c := range got {
		if seen[c.Ref.SessionID] {
			t.Errorf("session %s selected twice", c.Ref.SessionID)
		}
		seen[c.Ref.SessionID] = true
	}
	// Zero-friction sessions get 5 repeats; others 3.
	for _, c := range got {
		want := 3
		if isZeroFriction(c.Stats) {
			want = 5
		}
		if c.Repeats != want {
			t.Errorf("%s repeats=%d want %d", c.Ref.SessionID, c.Repeats, want)
		}
	}
	// Determinism: same input → identical output.
	again := curate(pool)
	if len(again) != len(got) {
		t.Fatalf("nondeterministic length: %d vs %d", len(again), len(got))
	}
	for i := range got {
		if again[i].Ref.SessionID != got[i].Ref.SessionID || again[i].Cell != got[i].Cell {
			t.Fatalf("nondeterministic at %d: %+v vs %+v", i, again[i], got[i])
		}
	}
}

func TestCuratePredicates(t *testing.T) {
	clean := AgentSessionStats{}
	if !isZeroFriction(clean) {
		t.Error("empty stats should be zero-friction")
	}
	if isZeroFriction(AgentSessionStats{ToolErrors: 1}) {
		t.Error("tool error → frictionful")
	}
	if !IsMeta(AgentSessionStats{Cwd: "/x/facet-spike"}) {
		t.Error("facet cwd → meta")
	}
	if !IsMeta(AgentSessionStats{Skills: []string{"analyzing-agent-sessions"}}) {
		t.Error("analyzing skill → meta")
	}
	if IsMeta(AgentSessionStats{Cwd: "/work/alpha"}) {
		t.Error("plain cwd → not meta")
	}
}

func TestCurateIDsSelectsOutlierAndCells(t *testing.T) {
	mk := func(id string, turns, errs int) AgentSessionStats {
		return AgentSessionStats{SessionID: id, Repo: "/Users/x/Developer/r", AssistantTurns: turns, ToolErrors: errs}
	}
	stats := []AgentSessionStats{
		mk("a-outlier", 200, 0),
		mk("b-zero-short", 2, 0),
		mk("c-friction-medium", 20, 3),
	}
	got := CurateIDs(stats, map[string]int64{"a-outlier": 999})
	if got["a-outlier"] != "outlier" {
		t.Fatalf("outlier cell = %q", got["a-outlier"])
	}
	if got["b-zero-short"] == "" || got["c-friction-medium"] == "" {
		t.Fatalf("cells missing: %v", got)
	}
}
