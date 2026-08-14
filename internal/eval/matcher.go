package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	_ "embed"
)

//go:embed matcher_prompt.md
var matcherPrompt string

//go:embed matcher_schema.json
var matcherSchema string

// MatcherModel is the pinned matcher model id (spec: models pinned everywhere;
// moving a pin orphans verdict comparability, never silently).
const MatcherModel = "claude-opus-4-8"

// matcherRevision is bumped on matcher Go changes the prompt/schema hash
// cannot see (payload construction re-keys itself via the payload hash;
// result interpretation is recomputed from cache and needs no bump).
const matcherRevision = 1

// MatcherCodeVersion keys cached matcher calls on exactly what shapes a
// matcher read: prompt, schema, revision. Deliberately narrower than
// CodeVersion("internal/eval") — verdict rendering iterates hot and
// must never re-buy the matcher-call budget.
func MatcherCodeVersion() string {
	return cacheKey("matcher-code", matcherPrompt, matcherSchema, strconv.Itoa(matcherRevision))
}

// granularityRank orders the granularity scale; higher is more faithful.
// "absent" is a target-level outcome (no counted matches), never a per-item
// matcher granularity.
var granularityRank = map[string]int{"absent": 0, "over_generalized": 1, "partial": 2, "full": 3}

type MatchRubric struct {
	ID                       string   `json:"id"`
	Part                     string   `json:"part"`
	Statement                string   `json:"statement"`
	RequiredNuances          []string `json:"required_nuances"`
	ForbiddenGeneralizations []string `json:"forbidden_generalizations"`
}

type MatchItem struct {
	ID      string   `json:"id"`    // "finding/<rank>" | "dropped/<i>" | "probe/<class>"
	Repos   []string `json:"repos"` // the repos the item cites; several for a merged finding
	Surface string   `json:"surface"`
	Text    string   `json:"text"`
}

// MatchPayload is the exact matcher stdin; its JSON hash is the content part
// of the scoring cache key. It carries no session ids and no anchors — the
// matcher stays blind to corroboration, which is deterministic Go.
type MatchPayload struct {
	Rubric MatchRubric `json:"rubric"`
	Items  []MatchItem `json:"items"`
}

type ItemMatch struct {
	ItemID                string `json:"item_id"`
	Granularity           string `json:"granularity"` // full | partial | over_generalized
	NuanceResults         []bool `json:"nuance_results"`
	ForbiddenFormsMatched []int  `json:"forbidden_forms_matched"`
}

type MatchResult struct {
	Matches []ItemMatch `json:"matches"`
}

// Matcher is the LLM scorer boundary (same pattern as Judge/Synthesizer);
// tests always inject fakes.
type Matcher interface {
	Match(ctx context.Context, p MatchPayload) (MatchResult, error)
}

// validateMatchResult rejects matcher output that contradicts its payload —
// unknown item ids, wrong nuance counts, out-of-range forbidden indices. A
// corrupted read must fail the attempt loudly, never shrink silently to
// "absent" or be cached as a lie.
func validateMatchResult(p MatchPayload, res MatchResult) error {
	known := map[string]bool{}
	for _, it := range p.Items {
		known[it.ID] = true
	}
	for _, m := range res.Matches {
		if !known[m.ItemID] {
			return fmt.Errorf("matcher output references unknown item %q", m.ItemID)
		}
		switch m.Granularity {
		case "full", "partial", "over_generalized":
		default:
			return fmt.Errorf("matcher output: item %s has invalid granularity %q", m.ItemID, m.Granularity)
		}
		if len(m.NuanceResults) != len(p.Rubric.RequiredNuances) {
			return fmt.Errorf("matcher output: item %s has %d nuance results, want %d",
				m.ItemID, len(m.NuanceResults), len(p.Rubric.RequiredNuances))
		}
		for _, idx := range m.ForbiddenFormsMatched {
			if idx < 0 || idx >= len(p.Rubric.ForbiddenGeneralizations) {
				return fmt.Errorf("matcher output: item %s forbidden index %d out of range", m.ItemID, idx)
			}
		}
	}
	return nil
}

type matcherRunner func(ctx context.Context, stdin []byte) ([]byte, error)

type claudeMatcher struct {
	run matcherRunner
}

// matcherEnvelope mirrors the `claude -p --output-format json` wrapper. Each
// package owns its copy of this tiny boundary type (codebase precedent:
// insights/judge.go, synthesis/synthesizer.go).
type matcherEnvelope struct {
	IsError          bool            `json:"is_error"`
	Result           string          `json:"result"`
	StructuredOutput json.RawMessage `json:"structured_output"`
}

func (m claudeMatcher) Match(ctx context.Context, p MatchPayload) (MatchResult, error) {
	stdin, err := json.Marshal(p)
	if err != nil {
		return MatchResult{}, err
	}
	out, err := m.run(ctx, stdin)
	if err != nil {
		return MatchResult{}, fmt.Errorf("matcher command: %w", err)
	}
	var env matcherEnvelope
	if err := json.Unmarshal(out, &env); err != nil {
		return MatchResult{}, fmt.Errorf("malformed envelope: %w", err)
	}
	if env.IsError {
		return MatchResult{}, fmt.Errorf("claude reported is_error: %s", env.Result)
	}
	if len(env.StructuredOutput) == 0 || string(env.StructuredOutput) == "null" {
		return MatchResult{}, fmt.Errorf("null/missing structured_output")
	}
	var res MatchResult
	if err := json.Unmarshal(env.StructuredOutput, &res); err != nil {
		return MatchResult{}, fmt.Errorf("structured_output parse: %w", err)
	}
	if err := validateMatchResult(p, res); err != nil {
		return MatchResult{}, err
	}
	return res, nil
}

// newMatchCommand builds the matcher invocation: the embedded prompt as -p
// (the matcher is harness-internal, not a skill), payload on stdin.
func newMatchCommand(ctx context.Context, stdin []byte, configDir, workDir string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "claude", "-p", matcherPrompt,
		"--output-format", "json",
		"--json-schema", matcherSchema,
		"--model", MatcherModel,
		"--no-session-persistence")
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Env = scrubbedMatcherEnv()
	// Appended last so it wins over any inherited CLAUDE_CONFIG_DIR (os/exec
	// keeps the last duplicate).
	if configDir != "" {
		cmd.Env = append(cmd.Env, "CLAUDE_CONFIG_DIR="+configDir)
	}
	if workDir != "" {
		cmd.Dir = workDir
	}
	return cmd
}

// scrubbedMatcherEnv removes API-key vars so the nested claude runs under
// subscription auth (removed, not blanked — an empty ANTHROPIC_API_KEY still
// wins its precedence slot). Package-local copy by codebase precedent.
func scrubbedMatcherEnv() []string {
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

// wrapMatcherExit includes claude's stdout tail when stderr is empty — claude
// -p reports auth errors in the stdout envelope with exit 1 and blank stderr.
func wrapMatcherExit(out []byte, err error) error {
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

// NewClaudeMatcherPinned returns the real matcher, config dir + cwd pinned to
// the EnvPin scratch composition (credential materialization included).
func NewClaudeMatcherPinned(configDir, workDir string) Matcher {
	return claudeMatcher{run: func(ctx context.Context, stdin []byte) ([]byte, error) {
		out, err := newMatchCommand(ctx, stdin, configDir, workDir).Output()
		if err != nil {
			return out, wrapMatcherExit(out, err)
		}
		return out, nil
	}}
}
