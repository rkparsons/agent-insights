package insights

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestNewAnalyzeCommandArgvAndEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-should-be-scrubbed")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "tok-should-be-scrubbed")
	cmd := newAnalyzeCommand(context.Background(), "claude-opus-4-8", `{"x":1}`, []byte("reduced input"))

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

func TestNewClaudeJudgeConfigured(t *testing.T) {
	j, ok := NewClaudeJudge().(claudeJudge)
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
