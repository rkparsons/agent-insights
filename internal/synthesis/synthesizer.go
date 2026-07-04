package synthesis

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	_ "embed"
)

//go:embed schema.json
var synthesisSchema string

const (
	synthesisModel        = "claude-opus-4-8"
	synthesisSkillCommand = "/synthesizing-workflow-insights"
)

// SynthesisModel is the pinned L2 model id, exported for eval cache keys and
// reproducibility records.
const SynthesisModel = synthesisModel

// SchemaHash returns the sha256 (hex) of the embedded L2 schema, for eval
// cache keys and reproducibility records.
func SchemaHash() string {
	sum := sha256.Sum256([]byte(synthesisSchema))
	return hex.EncodeToString(sum[:])
}

// Synthesizer produces the qualitative themes/recommendations half of a repo's
// insights from its EvidenceBundle. Injected so the merge/ranking logic is
// testable with a fake and no real LLM.
type Synthesizer interface {
	Synthesize(ctx context.Context, b EvidenceBundle) (RawSynthesis, error)
}

// commandRunner runs the prepared claude command, feeding stdin and returning
// stdout. Injected so the envelope parsing + error handling are unit-testable.
type commandRunner func(ctx context.Context, stdin []byte) (stdout []byte, err error)

type claudeSynthesizer struct {
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

func (s claudeSynthesizer) Synthesize(ctx context.Context, b EvidenceBundle) (RawSynthesis, error) {
	stdin, err := json.Marshal(b)
	if err != nil {
		return RawSynthesis{}, err
	}
	out, err := s.run(ctx, stdin)
	if err != nil {
		return RawSynthesis{}, fmt.Errorf("synthesis command: %w", err)
	}
	var env claudeEnvelope
	if err := json.Unmarshal(out, &env); err != nil {
		return RawSynthesis{}, fmt.Errorf("malformed envelope: %w", err)
	}
	if env.IsError {
		return RawSynthesis{}, fmt.Errorf("claude reported is_error: %s", env.Result)
	}
	if len(env.StructuredOutput) == 0 || string(env.StructuredOutput) == "null" {
		return RawSynthesis{}, fmt.Errorf("null/missing structured_output")
	}
	var raw RawSynthesis
	if err := json.Unmarshal(env.StructuredOutput, &raw); err != nil {
		return RawSynthesis{}, fmt.Errorf("structured_output parse: %w", err)
	}
	return raw, nil
}

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

// newSynthesizeCommand builds the claude invocation that runs the synthesis skill with
// structured output. The bundle is fed on stdin (argv is never used for it — bundles
// can exceed the macOS argv cap).
func newSynthesizeCommand(ctx context.Context, model, schema string, stdin []byte, configDir, workDir string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "claude", "-p", synthesisSkillCommand,
		"--output-format", "json",
		"--json-schema", schema,
		"--model", model,
		// Nested synthesis calls must NOT persist their own session transcripts —
		// otherwise a backfill re-run litters ~/.claude/projects with synthesizer
		// exhaust that then re-enters the scan as gated noise. The synthesis still
		// returns structured output on stdout; only the on-disk session record is
		// suppressed.
		"--no-session-persistence")
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Env = scrubbedEnv()
	// Appended last so it wins over any inherited CLAUDE_CONFIG_DIR (os/exec
	// keeps the last duplicate). Pinning both knobs keeps a nested claude from
	// reading live global config or a project CLAUDE.md from the caller's cwd.
	if configDir != "" {
		cmd.Env = append(cmd.Env, "CLAUDE_CONFIG_DIR="+configDir)
	}
	if workDir != "" {
		cmd.Dir = workDir
	}
	return cmd
}

// NewClaudeSynthesizer returns a Synthesizer that shells out to `claude -p` under
// subscription auth (Opus 4.8, embedded schema). The caller's ctx governs the
// subprocess timeout; a context with no deadline means no timeout.
func NewClaudeSynthesizer() Synthesizer { return NewClaudeSynthesizerPinned("", "") }

// NewClaudeSynthesizerPinned is NewClaudeSynthesizer with the nested claude's
// config dir and working directory pinned (see NewClaudeJudgePinned).
func NewClaudeSynthesizerPinned(configDir, workDir string) Synthesizer {
	s := claudeSynthesizer{model: synthesisModel, schema: synthesisSchema}
	s.run = func(ctx context.Context, stdin []byte) ([]byte, error) {
		out, err := newSynthesizeCommand(ctx, s.model, s.schema, stdin, configDir, workDir).Output()
		if err != nil {
			return out, wrapClaudeExit(out, err)
		}
		return out, nil
	}
	return s
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
