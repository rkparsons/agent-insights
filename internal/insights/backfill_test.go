package insights

import (
	"context"
	"testing"
	"time"

	"tmux-ctrl/internal/sources/claude"
)

func TestRunBackfillGatesAndRecords(t *testing.T) {
	projects := t.TempDir()
	t.Setenv("TMUX_CTRL_CLAUDE_PROJECTS_DIR", projects)
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	writeSession(t, projects, "proj", "big", 6)   // substantial -> analyzed
	writeSession(t, projects, "proj", "small", 2) // trivial -> gated
	// a subagent transcript must be ignored entirely
	writeSession(t, projects, "proj/subagents", "agent-zzz", 9)

	judge := fakeJudge{fields: substantialJudged()}
	opts := Options{MinAssistantTurns: DefaultMinAssistantTurns, Timeout: time.Minute}
	sum, err := RunBackfill(context.Background(), noRepo, judge, opts)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Analyzed != 1 || sum.SkippedGate != 1 {
		t.Errorf("want 1 analyzed + 1 gated, got %+v", sum)
	}
	if _, ok := ReadAnalysisMtime("big"); !ok {
		t.Error("big should be analyzed")
	}
	if _, ok := ReadAnalysisMtime("small"); ok {
		t.Error("small should be gated, not analyzed")
	}
	m, _ := loadManifest()
	if e, ok := m["small"]; !ok || e.Outcome != "gated" || e.Threshold != DefaultMinAssistantTurns {
		t.Errorf("gated entry missing/wrong: %+v ok=%v", e, ok)
	}
}

func TestRunBackfillResumeSkips(t *testing.T) {
	projects := t.TempDir()
	t.Setenv("TMUX_CTRL_CLAUDE_PROJECTS_DIR", projects)
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	writeSession(t, projects, "proj", "big", 6)
	writeSession(t, projects, "proj", "small", 2)
	judge := fakeJudge{fields: substantialJudged()}
	opts := Options{MinAssistantTurns: DefaultMinAssistantTurns, Timeout: time.Minute}

	if _, err := RunBackfill(context.Background(), noRepo, judge, opts); err != nil {
		t.Fatal(err)
	}
	sum, err := RunBackfill(context.Background(), noRepo, judge, opts)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Analyzed != 0 || sum.SkippedIncremental != 1 || sum.SkippedGate != 1 {
		t.Errorf("resume should skip everything, got %+v", sum)
	}
}

func TestBackfillSkipStaleGateOnThresholdChange(t *testing.T) {
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	mt := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	ref := claude.TranscriptRef{SessionID: "s", Mtime: mt}
	m := map[string]ManifestEntry{"s": {SessionID: "s", TranscriptMtime: mt, Outcome: "gated", Threshold: 5}}
	if _, skip := backfillSkip(ref, m, 5, false); !skip {
		t.Error("same threshold should skip")
	}
	if _, skip := backfillSkip(ref, m, 3, false); skip {
		t.Error("lower threshold should re-evaluate (not skip)")
	}
}
