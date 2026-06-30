package insights

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func substantialJudged() JudgedFields {
	return JudgedFields{UnderlyingGoal: "g", SessionType: "single_task", Outcome: "fully_achieved", BriefSummary: "b"}
}

// writeSession drops a transcript with n assistant messages into a temp projects tree
// and returns its session-id.
func writeSession(t *testing.T, projects, project, id string, assistantMsgs int) {
	t.Helper()
	dir := filepath.Join(projects, project)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user","cwd":"/repo/app","gitBranch":"main","version":"2.1.195","timestamp":"2026-06-29T09:00:00Z","message":{"content":"please do the thing for me"}}`
	for i := 0; i < assistantMsgs; i++ {
		body += "\n" + `{"type":"assistant","timestamp":"2026-06-29T09:0` + string(rune('0'+i)) + `:00Z","message":{"id":"m` + string(rune('0'+i)) + `","role":"assistant","model":"claude-opus-4-8","usage":{"input_tokens":1,"output_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0},"content":[{"type":"text","text":"working"}]}}`
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunSingleWritesAnalysisAndForcesGate(t *testing.T) {
	projects := t.TempDir()
	t.Setenv("TMUX_CTRL_CLAUDE_PROJECTS_DIR", projects)
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	// Only 2 assistant turns => below the gate; RunSingle must analyze it anyway.
	writeSession(t, projects, "proj", "sess-x", 2)

	judge := fakeJudge{fields: substantialJudged()}
	sum, err := RunSingle(context.Background(), "sess-x", noRepo, judge, Options{MinAssistantTurns: DefaultMinAssistantTurns, Timeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Analyzed != 1 {
		t.Errorf("want Analyzed=1 (gate forced), got %+v", sum)
	}
	if _, ok := ReadAnalysisMtime("sess-x"); !ok {
		t.Error("analysis file not written")
	}
}

func TestRunSingleIncrementalSkip(t *testing.T) {
	projects := t.TempDir()
	t.Setenv("TMUX_CTRL_CLAUDE_PROJECTS_DIR", projects)
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	writeSession(t, projects, "proj", "sess-y", 6)
	judge := fakeJudge{fields: substantialJudged()}
	opts := Options{MinAssistantTurns: DefaultMinAssistantTurns, Timeout: time.Minute}

	if _, err := RunSingle(context.Background(), "sess-y", noRepo, judge, opts); err != nil {
		t.Fatal(err)
	}
	sum, err := RunSingle(context.Background(), "sess-y", noRepo, judge, opts)
	if err != nil {
		t.Fatal(err)
	}
	if sum.SkippedIncremental != 1 || sum.Analyzed != 0 {
		t.Errorf("second run should skip-incremental, got %+v", sum)
	}
}
