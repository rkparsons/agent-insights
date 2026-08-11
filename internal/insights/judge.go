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

// AnalysisModel is the pinned L1 model id, exported for eval cache keys and
// reproducibility records.
const AnalysisModel = analysisModel

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
// it — transcripts exceed the macOS argv cap). workDir is required, not optional:
// the nested claude resolves /analyzing-agent-sessions from its cwd, where the run
// materialized the skills package — an empty workDir would silently fall back to the
// caller's cwd and whatever skills happen to be ambient there.
func newAnalyzeCommand(ctx context.Context, model, schema string, stdin []byte, configDir, workDir string) (*exec.Cmd, error) {
	if workDir == "" {
		return nil, errors.New("analysis workDir is empty: the run must materialize the skills into a scratch cwd (skills.TempWorkdir)")
	}
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
	// Appended last so it wins over any inherited CLAUDE_CONFIG_DIR (os/exec
	// keeps the last duplicate). Pinning both knobs keeps a nested claude from
	// reading live global config or a project CLAUDE.md from the caller's cwd.
	if configDir != "" {
		cmd.Env = append(cmd.Env, "CLAUDE_CONFIG_DIR="+configDir)
	}
	cmd.Dir = workDir
	return cmd, nil
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
	trimmed, err := ParseClaudeEnvelope(out)
	if err != nil {
		return JudgedFields{}, err
	}
	var jf JudgedFields
	if err := json.Unmarshal(trimmed, &jf); err != nil {
		return JudgedFields{}, fmt.Errorf("parse structured_output: %w", err)
	}
	return jf, nil
}

// ParseClaudeEnvelope decodes a `claude -p --output-format json` stdout
// envelope and returns its trimmed structured_output payload. Errors on empty
// output, a malformed envelope, is_error, or null/missing structured_output.
func ParseClaudeEnvelope(out []byte) (json.RawMessage, error) {
	if len(bytes.TrimSpace(out)) == 0 {
		return nil, errors.New("claude returned empty output")
	}
	var env claudeEnvelope
	if err := json.Unmarshal(out, &env); err != nil {
		return nil, fmt.Errorf("parse claude envelope: %w", err)
	}
	if env.IsError {
		return nil, fmt.Errorf("claude reported error: %s", env.Result)
	}
	trimmed := bytes.TrimSpace(env.StructuredOutput)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, errors.New("claude envelope missing structured_output")
	}
	return trimmed, nil
}

// JudgeFactory builds a run's Judge once the run has materialized the skills
// package into workDir — the nested claude's cwd. The seam is a factory rather
// than a Judge because only the run owns that directory's lifetime; tests pass a
// factory that ignores workDir and returns a fake.
type JudgeFactory func(workDir string) Judge

// NewClaudeJudge returns a Judge that shells out to `claude -p` under subscription
// auth (Opus 4.8, embedded schema), running in workDir. The caller's ctx governs the
// subprocess timeout; a context with no deadline means no timeout — the step-6 caller
// must set one.
func NewClaudeJudge(workDir string) Judge { return NewClaudeJudgePinned("", workDir) }

// NewClaudeJudgePinned is NewClaudeJudge with the nested claude's config dir pinned
// too — the eval harness points it at an ephemeral copy of the frozen config
// snapshot. An empty configDir leaves that knob inherited; workDir is always required.
func NewClaudeJudgePinned(configDir, workDir string) Judge {
	j := claudeJudge{model: analysisModel, schema: analysisSchema}
	j.run = func(ctx context.Context, stdin []byte) ([]byte, error) {
		cmd, err := newAnalyzeCommand(ctx, j.model, j.schema, stdin, configDir, workDir)
		if err != nil {
			return nil, err
		}
		out, err := cmd.Output()
		if err != nil {
			return out, wrapClaudeExit(out, err)
		}
		return out, nil
	}
	return j
}

// wrapClaudeExit formats a claude subprocess failure. claude -p reports many
// errors (e.g. "Not logged in") in its stdout JSON envelope with an empty
// stderr, so stdout's tail is included whenever stderr alone would be blank.
func wrapClaudeExit(out []byte, err error) error {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return err
	}
	detail := strings.TrimSpace(string(ee.Stderr))
	if detail == "" {
		detail = "stdout: " + strings.TrimSpace(string(out))
	}
	if r := []rune(detail); len(r) > 2000 {
		detail = string(r[:2000]) + "…"
	}
	return fmt.Errorf("claude exit %d: %s", ee.ExitCode(), detail)
}
