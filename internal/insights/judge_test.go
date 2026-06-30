package insights

import (
	"context"
	"strings"
	"testing"
)

func TestNewAnalyzeCommandArgvAndEnv(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-should-be-scrubbed")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "tok-should-be-scrubbed")
	cmd := newAnalyzeCommand(context.Background(), "claude-opus-4-8", `{"x":1}`, []byte("reduced input"))

	args := strings.Join(cmd.Args, "\x00")
	for _, want := range []string{"claude", "-p", "/analyzing-agent-sessions", "--output-format", "json", "--json-schema", `{"x":1}`, "--model", "claude-opus-4-8"} {
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
