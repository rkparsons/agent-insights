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
	"time"

	"github.com/rkparsons/agent-insights/skills"
)

// synthesisSchema is the JSON schema passed to `claude -p --json-schema`,
// single-sourced from the embedded synthesizing-workflow-insights skill so the
// schema and the prompt that documents it cannot drift apart.
var synthesisSchema = string(skills.SynthesisSchema())

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
	stdinBundle := b
	stdinBundle.SessionDates = nil // model must never see dates (it cannot emit numbers)
	stdin, err := json.Marshal(stdinBundle)
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
// can exceed the macOS argv cap). workDir is required, not optional: the nested claude
// resolves /synthesizing-workflow-insights from its cwd, where the run materialized the
// skills package — an empty workDir would silently fall back to the caller's cwd and
// whatever skills happen to be ambient there.
func newSynthesizeCommand(ctx context.Context, model, schema string, stdin []byte, configDir, workDir string) (*exec.Cmd, error) {
	if workDir == "" {
		return nil, errors.New("synthesis workDir is empty: the run must materialize the skills into a scratch cwd (skills.TempWorkdir)")
	}
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
	// A context kill only signals the direct child; claude's own subprocesses
	// inherit the stdout pipe and can strand Output() long past the deadline
	// (observed: a 20m kill draining for hours). WaitDelay forcibly closes.
	cmd.WaitDelay = 30 * time.Second
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

// SynthesizerFactory builds a run's Synthesizer once the run has materialized
// the skills package into workDir — the nested claude's cwd. The seam is a
// factory rather than a Synthesizer because only the run owns that directory's
// lifetime; tests pass a factory that ignores workDir and returns a fake.
type SynthesizerFactory func(workDir string) Synthesizer

// NewClaudeSynthesizer returns a Synthesizer that shells out to `claude -p` under
// subscription auth (Opus 4.8, embedded schema), running in workDir. The caller's
// ctx governs the subprocess timeout; a context with no deadline means no timeout.
func NewClaudeSynthesizer(workDir string) Synthesizer {
	return NewClaudeSynthesizerPinned("", workDir)
}

// NewClaudeSynthesizerPinned is NewClaudeSynthesizer with the nested claude's
// config dir pinned too (see NewClaudeJudgePinned). An empty configDir leaves
// that knob inherited; workDir is always required.
func NewClaudeSynthesizerPinned(configDir, workDir string) Synthesizer {
	s := claudeSynthesizer{model: synthesisModel, schema: synthesisSchema}
	s.run = func(ctx context.Context, stdin []byte) ([]byte, error) {
		cmd, err := newSynthesizeCommand(ctx, s.model, s.schema, stdin, configDir, workDir)
		if err != nil {
			return nil, err
		}
		out, err := cmd.Output()
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
