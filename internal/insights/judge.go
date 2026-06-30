package insights

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
)

const (
	analysisModel        = "claude-opus-4-8"
	analysisSkillCommand = "/analyzing-agent-sessions"
)

// scrubbedEnv returns the current environment with the API-key vars removed so the
// nested claude runs under subscription auth, never API billing. Removed, not
// blanked — an empty ANTHROPIC_API_KEY still wins its precedence slot.
func scrubbedEnv() []string {
	src := os.Environ()
	out := make([]string, 0, len(src))
	for _, kv := range src {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") || strings.HasPrefix(kv, "ANTHROPIC_AUTH_TOKEN=") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// newAnalyzeCommand builds the claude invocation that runs the analysis skill with
// structured output. The reduced transcript is fed on stdin (argv is never used for
// it — transcripts exceed the macOS argv cap).
func newAnalyzeCommand(ctx context.Context, model, schema string, stdin []byte) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "claude", "-p", analysisSkillCommand,
		"--output-format", "json",
		"--json-schema", schema,
		"--model", model)
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Env = scrubbedEnv()
	return cmd
}
