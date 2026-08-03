package insights

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tmux-ctrl/skills"
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
	sum, err := RunSingle(context.Background(), "sess-x", noRepo, fixedJudge(judge), Options{MinAssistantTurns: DefaultMinAssistantTurns, Timeout: time.Minute})
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

// TestRunCreatesSkillWorkdir pins the delivery contract: a run materializes the
// skills package into a scratch cwd, every nested claude command runs there, and
// the directory does not outlive the run.
func TestRunCreatesSkillWorkdir(t *testing.T) {
	projects := t.TempDir()
	t.Setenv("TMUX_CTRL_CLAUDE_PROJECTS_DIR", projects)
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	writeSession(t, projects, "proj", "sess-w", 6)

	var cmdDir string
	factory := func(workDir string) Judge {
		cmd, err := newAnalyzeCommand(context.Background(), analysisModel, analysisSchema, nil, "", workDir)
		if err != nil {
			t.Fatalf("newAnalyzeCommand with the run's workDir: %v", err)
		}
		cmdDir = cmd.Dir
		// Checked here, not after the run: the directory is gone by then.
		for _, name := range skills.Names() {
			if _, err := os.Stat(filepath.Join(cmd.Dir, ".claude", "skills", name, "SKILL.md")); err != nil {
				t.Errorf("%s not materialized into the run's cwd: %v", name, err)
			}
		}
		return fakeJudge{fields: substantialJudged()}
	}
	if _, err := RunBackfill(context.Background(), noRepo, factory, Options{MinAssistantTurns: DefaultMinAssistantTurns, Timeout: time.Minute}); err != nil {
		t.Fatal(err)
	}

	if cmdDir == "" {
		t.Fatal("the run never built a judge for a workdir")
	}
	if _, err := os.Stat(cmdDir); !os.IsNotExist(err) {
		t.Errorf("run left its scratch cwd %s behind (err=%v)", cmdDir, err)
	}
}

func TestRunSingleIncrementalSkip(t *testing.T) {
	projects := t.TempDir()
	t.Setenv("TMUX_CTRL_CLAUDE_PROJECTS_DIR", projects)
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	writeSession(t, projects, "proj", "sess-y", 6)
	judge := fakeJudge{fields: substantialJudged()}
	opts := Options{MinAssistantTurns: DefaultMinAssistantTurns, Timeout: time.Minute}

	if _, err := RunSingle(context.Background(), "sess-y", noRepo, fixedJudge(judge), opts); err != nil {
		t.Fatal(err)
	}
	sum, err := RunSingle(context.Background(), "sess-y", noRepo, fixedJudge(judge), opts)
	if err != nil {
		t.Fatal(err)
	}
	if sum.SkippedIncremental != 1 || sum.Analyzed != 0 {
		t.Errorf("second run should skip-incremental, got %+v", sum)
	}
}
