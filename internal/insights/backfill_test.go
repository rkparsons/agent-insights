package insights

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

func TestRunBackfillCanceledNotRecorded(t *testing.T) {
	projects := t.TempDir()
	t.Setenv("TMUX_CTRL_CLAUDE_PROJECTS_DIR", projects)
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	writeSession(t, projects, "proj", "abrt", 6)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // user abort before processing
	judge := fakeJudge{err: context.Canceled}
	sum, err := RunBackfill(ctx, noRepo, judge, Options{MinAssistantTurns: DefaultMinAssistantTurns, Timeout: time.Minute})
	if err == nil {
		t.Error("want non-nil error on canceled parent ctx")
	}
	if sum.Errored != 0 {
		t.Errorf("canceled session must not be recorded as errored: %+v", sum)
	}
	m, _ := loadManifest()
	if _, ok := m["abrt"]; ok {
		t.Error("canceled session must leave no manifest entry")
	}
}

func TestRunBackfillReprocessesOnNewerMtime(t *testing.T) {
	projects := t.TempDir()
	t.Setenv("TMUX_CTRL_CLAUDE_PROJECTS_DIR", projects)
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	writeSession(t, projects, "proj", "grow", 6)
	judge := fakeJudge{fields: substantialJudged()}
	opts := Options{MinAssistantTurns: DefaultMinAssistantTurns, Timeout: time.Minute}
	if _, err := RunBackfill(context.Background(), noRepo, judge, opts); err != nil {
		t.Fatal(err)
	}
	// bump the transcript mtime beyond the stamped decode-time value
	p := filepath.Join(projects, "proj", "grow.jsonl")
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(p, future, future); err != nil {
		t.Fatal(err)
	}
	sum, err := RunBackfill(context.Background(), noRepo, judge, opts)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Analyzed != 1 || sum.SkippedIncremental != 0 {
		t.Errorf("a newer transcript mtime must re-process, got %+v", sum)
	}
}

func TestRunBackfillRecordsErrored(t *testing.T) {
	projects := t.TempDir()
	t.Setenv("TMUX_CTRL_CLAUDE_PROJECTS_DIR", projects)
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	writeSession(t, projects, "proj", "boom", 6)
	judge := fakeJudge{err: errors.New("judge failed")}
	sum, err := RunBackfill(context.Background(), noRepo, judge, Options{MinAssistantTurns: DefaultMinAssistantTurns, Timeout: time.Minute})
	if err != nil {
		t.Fatalf("loop should continue past errors, not return: %v", err)
	}
	if sum.Errored != 1 {
		t.Errorf("want Errored=1, got %+v", sum)
	}
	m, _ := loadManifest()
	if e, ok := m["boom"]; !ok || e.Outcome != "errored" {
		t.Errorf("want errored manifest entry, got %+v ok=%v", e, ok)
	}
}

func TestBackfillSkipStaleGateOnThresholdChange(t *testing.T) {
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	mt := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	ref := claude.TranscriptRef{SessionID: "s", Mtime: mt}
	m := map[string]ManifestEntry{"s": {SessionID: "s", TranscriptMtime: mt, Outcome: "gated", Threshold: 5}}
	if _, skip := backfillSkip(ref, m, 5); !skip {
		t.Error("same threshold should skip")
	}
	if _, skip := backfillSkip(ref, m, 3); skip {
		t.Error("lower threshold should re-evaluate (not skip)")
	}
}

func TestMetaTranscriptExclusion(t *testing.T) {
	meta := []string{
		"/h/.claude/projects/-Users-r-Developer-tmux-ctrl--worktrees-insights-generation/aa.jsonl",
		"/h/.claude/projects/-Users-r-Developer-tmux-ctrl--worktrees-insights-generation-src/bb.jsonl",
		"/h/.claude/projects/-Users-r-Developer-insights-eval-data/cc.jsonl",
		"/h/.claude/projects/-Users-r-Developer-client-project--worktrees-run-insights-command/dd.jsonl",
		"/h/.claude/projects/-Users-r-Developer-client-project--worktrees-facet-extractor/ee.jsonl",
		"/h/.claude/projects/-private-tmp-claude-501--Users-r--worktrees-insights-generation-x-scratchpad/ff.jsonl",
	}
	for _, p := range meta {
		if !metaTranscript(p) {
			t.Errorf("want meta-excluded: %s", p)
		}
	}
	nonMeta := []string{
		"/h/.claude/projects/-Users-r-Developer-tmux-ctrl/gg.jsonl",
		"/h/.claude/projects/-Users-r-Developer-client-project--worktrees-fix-login/hh.jsonl",
		"/h/.claude/projects/-Users-r-Developer-dotfiles/ii.jsonl",
	}
	for _, p := range nonMeta {
		if metaTranscript(p) {
			t.Errorf("want kept: %s", p)
		}
	}
}

func TestPlanCountsMetaEvenUnderForce(t *testing.T) {
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	refs := []claude.TranscriptRef{
		{SessionID: "meta1", Path: "/h/.claude/projects/-Users-r-Developer-insights-eval-data/meta1.jsonl"},
		{SessionID: "real1", Path: "/h/.claude/projects/-Users-r-Developer-tmux-ctrl/real1.jsonl"},
	}
	c := planCounts(refs, map[string]ManifestEntry{}, Options{Force: true, MinAssistantTurns: 5})
	if c.Meta != 1 || c.ToProcess != 1 {
		t.Errorf("want Meta=1 ToProcess=1 under --force, got %+v", c)
	}
}
