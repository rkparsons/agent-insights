package synthesis

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func TestClaudeSynthesizerParsesEnvelope(t *testing.T) {
	data, err := os.ReadFile("testdata/envelope.json")
	if err != nil {
		t.Fatal(err)
	}
	s := claudeSynthesizer{run: func(ctx context.Context, stdin []byte) ([]byte, error) { return data, nil }}
	raw, err := s.Synthesize(context.Background(), EvidenceBundle{Repo: "alpha"})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if len(raw.Themes) != 1 || raw.Themes[0].Kind != "friction" {
		t.Fatalf("themes = %+v", raw.Themes)
	}
	if raw.Themes[0].EvidenceIDs[1] != "F2" {
		t.Errorf("evidence_ids = %v, want [F1 F2]", raw.Themes[0].EvidenceIDs)
	}
	if len(raw.Recommendations) != 1 || raw.Recommendations[0].Type != "claude_md_rule" {
		t.Errorf("recs = %+v", raw.Recommendations)
	}
}

func TestClaudeSynthesizerNullStructuredOutput(t *testing.T) {
	s := claudeSynthesizer{run: func(ctx context.Context, stdin []byte) ([]byte, error) {
		return []byte(`{"is_error": false, "result": "", "structured_output": null}`), nil
	}}
	if _, err := s.Synthesize(context.Background(), EvidenceBundle{}); err == nil {
		t.Error("expected error on null structured_output")
	}
}

func TestWrapClaudeExit(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 3")
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected exit 3 to error")
	}
	wrapped := wrapClaudeExit([]byte("boom from stdout"), err)
	if wrapped == nil {
		t.Fatal("expected a wrapped error")
	}
	if !strings.Contains(wrapped.Error(), "exit 3") || !strings.Contains(wrapped.Error(), "boom from stdout") {
		t.Fatalf("wrapped error = %q, want it to mention exit 3 and stdout tail", wrapped.Error())
	}

	plain := errors.New("not an exit error")
	if got := wrapClaudeExit(nil, plain); got != plain {
		t.Fatalf("non-ExitError must pass through unchanged, got %v", got)
	}
}

func TestNewSynthesizeCommandPinsConfigDirAndCwd(t *testing.T) {
	cmd, err := newSynthesizeCommand(context.Background(), "m", "s", nil, "/tmp/cfg", "/tmp/work")
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Dir != "/tmp/work" {
		t.Fatalf("Dir = %q", cmd.Dir)
	}
	if !slices.Contains(cmd.Env, "CLAUDE_CONFIG_DIR=/tmp/cfg") {
		t.Fatal("env missing pinned CLAUDE_CONFIG_DIR")
	}
	// An unpinned config dir stays inherited (production); the workdir never can.
	inherited, err := newSynthesizeCommand(context.Background(), "m", "s", nil, "", "/tmp/work")
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range inherited.Env {
		if kv == "CLAUDE_CONFIG_DIR=" {
			t.Fatal("unpinned command must not append an empty CLAUDE_CONFIG_DIR")
		}
	}
}

// The nested claude resolves the skill from its cwd, so an empty workDir is a
// wiring bug that must fail loudly rather than run against whatever skills are
// ambient in the caller's cwd.
func TestNewSynthesizeCommandRejectsEmptyWorkDir(t *testing.T) {
	if _, err := newSynthesizeCommand(context.Background(), "m", "s", nil, "/tmp/cfg", ""); err == nil {
		t.Fatal("expected an error for an empty workDir")
	}
	s := NewClaudeSynthesizerPinned("/tmp/cfg", "")
	if _, err := s.Synthesize(context.Background(), EvidenceBundle{}); err == nil {
		t.Fatal("expected the synthesizer to refuse to run without a workdir")
	}
}

func TestSynthesizeStdinOmitsSessionDates(t *testing.T) {
	var captured []byte
	s := claudeSynthesizer{run: func(ctx context.Context, stdin []byte) ([]byte, error) {
		captured = stdin
		return []byte(`{"is_error":false,"result":"","structured_output":{"themes":[],"recommendations":[]}}`), nil
	}}
	b := EvidenceBundle{Repo: "r", SessionDates: map[string]string{"00000000-0000-4000-8000-000000000001": "2026-07-03"}}
	if _, err := s.Synthesize(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(captured), "session_dates") {
		t.Errorf("stdin payload leaked session_dates: %s", captured)
	}
	if strings.Contains(string(captured), "2026-07-03") {
		t.Errorf("stdin payload leaked a session date: %s", captured)
	}
}
