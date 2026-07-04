package insightseval

import (
	"context"
	"strings"
	"testing"
)

// fakeRunner scripts the raw stdout of the nested claude call.
type fakeRunner struct {
	out   string
	err   error
	stdin []byte
}

func (f *fakeRunner) run(_ context.Context, stdin []byte) ([]byte, error) {
	f.stdin = stdin
	return []byte(f.out), f.err
}

func samplePayload() MatchPayload {
	return MatchPayload{
		Rubric: MatchRubric{ID: "C-99", Part: "regression", Statement: "verify before asserting",
			RequiredNuances:          []string{"seek contradicting evidence"},
			ForbiddenGeneralizations: []string{"never assert anything"}},
		Items: []MatchItem{{ID: "client-project/theme/0", Bucket: "client-project", Surface: "theme", Text: "Confident conclusions before verifying"}},
	}
}

func TestClaudeMatcherParsesEnvelope(t *testing.T) {
	fr := &fakeRunner{out: `{"is_error":false,"result":"","structured_output":` +
		`{"matches":[{"item_id":"client-project/theme/0","granularity":"partial","nuance_results":[false],"forbidden_forms_matched":[]}]}}`}
	m := claudeMatcher{run: fr.run}
	res, err := m.Match(context.Background(), samplePayload())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 1 || res.Matches[0].Granularity != "partial" || res.Matches[0].ItemID != "client-project/theme/0" {
		t.Fatalf("res: %+v", res)
	}
	if !strings.Contains(string(fr.stdin), `"verify before asserting"`) {
		t.Fatalf("payload not on stdin: %s", fr.stdin)
	}
}

func TestClaudeMatcherErrorPaths(t *testing.T) {
	cases := []struct{ name, out string }{
		{"is_error", `{"is_error":true,"result":"Not logged in"}`},
		{"null structured", `{"is_error":false,"structured_output":null}`},
		{"malformed envelope", `nonsense`},
	}
	for _, tc := range cases {
		m := claudeMatcher{run: (&fakeRunner{out: tc.out}).run}
		if _, err := m.Match(context.Background(), samplePayload()); err == nil {
			t.Errorf("%s: want error", tc.name)
		}
	}
}

func TestClaudeMatcherRejectsInconsistentOutput(t *testing.T) {
	// schema-valid JSON that contradicts the payload must fail the read loudly
	cases := []struct{ name, out string }{
		{"unknown item", `{"structured_output":{"matches":[{"item_id":"nope/theme/9","granularity":"full","nuance_results":[true],"forbidden_forms_matched":[]}]}}`},
		{"nuance count", `{"structured_output":{"matches":[{"item_id":"client-project/theme/0","granularity":"full","nuance_results":[],"forbidden_forms_matched":[]}]}}`},
		{"forbidden index", `{"structured_output":{"matches":[{"item_id":"client-project/theme/0","granularity":"full","nuance_results":[true],"forbidden_forms_matched":[3]}]}}`},
		{"bad granularity", `{"structured_output":{"matches":[{"item_id":"client-project/theme/0","granularity":"absent","nuance_results":[true],"forbidden_forms_matched":[]}]}}`},
	}
	for _, tc := range cases {
		m := claudeMatcher{run: (&fakeRunner{out: tc.out}).run}
		if _, err := m.Match(context.Background(), samplePayload()); err == nil {
			t.Errorf("%s: want error", tc.name)
		}
	}
}

func TestNewMatchCommand(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-should-be-scrubbed")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "tok-should-be-scrubbed")
	t.Setenv("CLAUDE_CONFIG_DIR", "/elsewhere")
	cmd := newMatchCommand(context.Background(), []byte(`{"rubric":{}}`), "/tmp/cfg", "/tmp/work")

	args := strings.Join(cmd.Args, "\x00")
	for _, want := range []string{
		"-p\x00" + matcherPrompt,
		"--output-format\x00json",
		"--json-schema\x00" + matcherSchema,
		"--model\x00" + MatcherModel,
		"--no-session-persistence",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("argv missing %q; got %v", want, cmd.Args)
		}
	}
	if cmd.Stdin == nil {
		t.Error("stdin not wired")
	}
	if cmd.Dir != "/tmp/work" {
		t.Errorf("Dir = %q, want /tmp/work", cmd.Dir)
	}
	lastConfigDir := ""
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") || strings.HasPrefix(kv, "ANTHROPIC_AUTH_TOKEN=") {
			t.Errorf("env not scrubbed: %s", kv)
		}
		if strings.HasPrefix(kv, "CLAUDE_CONFIG_DIR=") {
			lastConfigDir = kv
		}
	}
	if lastConfigDir != "CLAUDE_CONFIG_DIR=/tmp/cfg" {
		t.Errorf("last CLAUDE_CONFIG_DIR = %q, want pinned /tmp/cfg", lastConfigDir)
	}
}

func TestMatcherCodeVersionStableAndNonEmpty(t *testing.T) {
	v1, v2 := MatcherCodeVersion(), MatcherCodeVersion()
	if v1 == "" || v1 != v2 {
		t.Fatalf("matcher code version: %q vs %q", v1, v2)
	}
	if matcherPrompt == "" || matcherSchema == "" {
		t.Fatal("embedded prompt/schema empty")
	}
}
