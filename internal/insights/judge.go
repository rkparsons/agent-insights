package insights

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
		"--model", model,
		// Nested analysis calls must NOT persist their own session transcripts —
		// otherwise the backfill litters ~/.claude/projects with analyzer exhaust
		// (one 1-turn structured-output session per analyzed session), which then
		// re-enters the scan as gated noise. The analysis still returns structured
		// output on stdout; only the on-disk session record is suppressed.
		"--no-session-persistence")
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Env = scrubbedEnv()
	return cmd
}

// Judge produces the model-judged fields from a reduced transcript. Injected so
// the merge/validation logic is testable with a fake and no real LLM.
type Judge interface {
	Judge(ctx context.Context, in ReducedInput) (JudgedFields, error)
}

// commandRunner runs the prepared claude command, feeding stdin and returning
// stdout. Injected so the envelope parsing + error handling are unit-testable.
type commandRunner func(ctx context.Context, stdin []byte) (stdout []byte, err error)

type claudeJudge struct {
	run    commandRunner
	model  string
	schema string
}

// claudeEnvelope is the `claude -p --output-format json` wrapper. structured_output
// holds the schema object when --json-schema is used.
type claudeEnvelope struct {
	IsError          bool            `json:"is_error"`
	Result           string          `json:"result"`
	StructuredOutput json.RawMessage `json:"structured_output"`
}

func (j claudeJudge) Judge(ctx context.Context, in ReducedInput) (JudgedFields, error) {
	out, err := j.run(ctx, []byte(in.Text))
	if err != nil {
		return JudgedFields{}, fmt.Errorf("claude run: %w", err)
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return JudgedFields{}, errors.New("claude returned empty output")
	}
	var env claudeEnvelope
	if err := json.Unmarshal(out, &env); err != nil {
		return JudgedFields{}, fmt.Errorf("parse claude envelope: %w", err)
	}
	if env.IsError {
		return JudgedFields{}, fmt.Errorf("claude reported error: %s", env.Result)
	}
	trimmed := bytes.TrimSpace(env.StructuredOutput)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return JudgedFields{}, errors.New("claude envelope missing structured_output")
	}
	var jf JudgedFields
	if err := json.Unmarshal(trimmed, &jf); err != nil {
		return JudgedFields{}, fmt.Errorf("parse structured_output: %w", err)
	}
	return jf, nil
}

// NewClaudeJudge returns a Judge that shells out to `claude -p` under subscription
// auth (Opus 4.8, embedded schema). The caller's ctx governs the subprocess timeout;
// a context with no deadline means no timeout — the step-6 caller must set one.
func NewClaudeJudge() Judge {
	j := claudeJudge{model: analysisModel, schema: analysisSchema}
	j.run = func(ctx context.Context, stdin []byte) ([]byte, error) {
		out, err := newAnalyzeCommand(ctx, j.model, j.schema, stdin).Output()
		if err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				stderr := string(ee.Stderr)
				if r := []rune(stderr); len(r) > 2000 {
					stderr = string(r[:2000]) + "…"
				}
				return out, fmt.Errorf("claude exit %d: %s", ee.ExitCode(), stderr)
			}
			return out, err
		}
		return out, nil
	}
	return j
}
