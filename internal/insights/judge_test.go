package insights

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

func TestNewAnalyzeCommandArgvAndEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-should-be-scrubbed")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "tok-should-be-scrubbed")
	cmd, err := newAnalyzeCommand(context.Background(), "claude-opus-4-8", `{"x":1}`, []byte("reduced input"), "", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	args := strings.Join(cmd.Args, "\x00")
	for _, want := range []string{"claude", "-p", "/analyzing-agent-sessions", "--output-format", "json", "--json-schema", `{"x":1}`, "--model", "claude-opus-4-8", "--no-session-persistence"} {
		if !strings.Contains(args, want) {
			t.Errorf("argv missing %q; got %v", want, cmd.Args)
		}
	}
	if strings.Contains(args, "--bare") {
		t.Error("argv must not contain --bare")
	}
	if cmd.Stdin == nil {
		t.Error("stdin not wired")
	}
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") || strings.HasPrefix(kv, "ANTHROPIC_AUTH_TOKEN=") {
			t.Errorf("env not scrubbed: %s", kv)
		}
	}
}

func runnerReturning(data []byte, err error) commandRunner {
	return func(ctx context.Context, stdin []byte) ([]byte, error) { return data, err }
}

func TestJudgeParsesEnvelopeFixture(t *testing.T) {
	data, err := os.ReadFile("testdata/envelope.json")
	if err != nil {
		t.Fatal(err)
	}
	j := claudeJudge{run: runnerReturning(data, nil)}
	jf, err := j.Judge(context.Background(), ReducedInput{Text: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if jf.UnderlyingGoal == "" || jf.SessionType == "" || jf.Outcome == "" || jf.BriefSummary == "" {
		t.Errorf("scalar judged field empty (schema/JudgedFields drift?): %+v", jf)
	}
	if len(jf.FrictionIncidents) == 0 || len(jf.StandingPreferences) == 0 {
		t.Fatalf("fixture must be schema-complete (>=1 friction, >=1 preference): %+v", jf)
	}
	if jf.FrictionIncidents[0].Type == "" || jf.FrictionIncidents[0].OneLine == "" {
		t.Errorf("friction item fields empty: %+v", jf.FrictionIncidents[0])
	}
	if jf.StandingPreferences[0].Rule == "" || jf.StandingPreferences[0].EvidenceQuote == "" {
		t.Errorf("preference item fields empty: %+v", jf.StandingPreferences[0])
	}
	if jf.FrictionIncidents[0].File == "" {
		t.Errorf("friction item file empty (fixture missing file field?): %+v", jf.FrictionIncidents[0])
	}
	if jf.StandingPreferences[0].Scope == "" {
		t.Errorf("preference item scope empty (fixture missing scope field?): %+v", jf.StandingPreferences[0])
	}
}

func TestJudgeErrorBranches(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		err  error
	}{
		{"runner error", nil, errors.New("exit 1: boom")},
		{"empty stdout", []byte("   "), nil},
		{"malformed envelope", []byte("{not json"), nil},
		{"is_error true", []byte(`{"is_error":true,"result":"model failed"}`), nil},
		{"missing structured_output", []byte(`{"is_error":false,"result":"x"}`), nil},
		{"garbage structured_output", []byte(`{"is_error":false,"structured_output":"not-an-object"}`), nil},
		{"null structured_output", []byte(`{"is_error":false,"structured_output":null}`), nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			j := claudeJudge{run: runnerReturning(c.data, c.err)}
			if _, err := j.Judge(context.Background(), ReducedInput{Text: "x"}); err == nil {
				t.Error("expected error, got nil")
			}
		})
	}
}

func TestParseClaudeEnvelope(t *testing.T) {
	cases := []struct {
		name      string
		out       []byte
		wantErr   string
		wantPayld string
	}{
		{"empty output", nil, "claude returned empty output", ""},
		{"whitespace-only output", []byte("   \n"), "claude returned empty output", ""},
		{"malformed envelope", []byte("{not json"), "parse claude envelope:", ""},
		{"is_error", []byte(`{"is_error":true,"result":"model failed"}`), "claude reported error: model failed", ""},
		{"missing structured_output", []byte(`{"is_error":false,"result":"x"}`), "claude envelope missing structured_output", ""},
		{"null structured_output", []byte(`{"is_error":false,"structured_output":null}`), "claude envelope missing structured_output", ""},
		{"ok", []byte(`{"is_error":false,"result":"","structured_output":{"a":1}}`), "", `{"a":1}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			payload, err := ParseClaudeEnvelope(c.out)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if string(payload) != c.wantPayld {
				t.Errorf("payload = %q, want %q", payload, c.wantPayld)
			}
		})
	}
}

func TestNewClaudeJudgeConfigured(t *testing.T) {
	j, ok := NewClaudeJudge(t.TempDir()).(claudeJudge)
	if !ok {
		t.Fatal("NewClaudeJudge did not return a claudeJudge")
	}
	if j.model != analysisModel {
		t.Errorf("model = %q, want %q", j.model, analysisModel)
	}
	if j.schema == "" {
		t.Error("schema is empty")
	}
	if j.run == nil {
		t.Error("runner is nil")
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

func TestNewAnalyzeCommandPinsConfigDirAndCwd(t *testing.T) {
	cmd, err := newAnalyzeCommand(context.Background(), "m", "s", nil, "/tmp/cfg", "/tmp/work")
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
	inherited, err := newAnalyzeCommand(context.Background(), "m", "s", nil, "", "/tmp/work")
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
func TestNewAnalyzeCommandRejectsEmptyWorkDir(t *testing.T) {
	if _, err := newAnalyzeCommand(context.Background(), "m", "s", nil, "/tmp/cfg", ""); err == nil {
		t.Fatal("expected an error for an empty workDir")
	}
	j := NewClaudeJudgePinned("/tmp/cfg", "")
	if _, err := j.Judge(context.Background(), ReducedInput{Text: "x"}); err == nil {
		t.Fatal("expected the judge to refuse to run without a workdir")
	}
}
