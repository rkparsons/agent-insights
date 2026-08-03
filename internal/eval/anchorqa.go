package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	_ "embed"

	"github.com/rkparsons/agent-insights/internal/insights"
)

//go:embed anchor_qa_prompt.md
var anchorQAPrompt string

//go:embed anchor_qa_schema.json
var anchorQASchema string

// AnchorQAJudgeModel is the pinned anchor-QA judge model (spec "Anchor-QA
// pass": mechanized pinned-LLM judge; every removal reproducible from the
// committed judge inputs alone).
const AnchorQAJudgeModel = "claude-opus-4-8"

// qaRubric is the only rubric surface the judge may see: statement and
// required nuances — never anchors, match results, cards, or verdicts.
type qaRubric struct {
	ID              string   `json:"id"`
	Statement       string   `json:"statement"`
	RequiredNuances []string `json:"required_nuances"`
}

// qaSession is one candidate anchor's full pool-side record. Only judged
// (LLM) pool fields appear — deterministic stats (cwd, branch, counts) are
// structurally excluded, keeping the judge blind to everything but the
// pool's own account of the session.
type qaSession struct {
	SessionID           string                        `json:"session_id"`
	UnderlyingGoal      string                        `json:"underlying_goal"`
	SessionType         string                        `json:"session_type"`
	Outcome             string                        `json:"outcome"`
	BriefSummary        string                        `json:"brief_summary"`
	FrictionIncidents   []insights.FrictionIncident   `json:"friction_incidents"`
	StandingPreferences []insights.StandingPreference `json:"standing_preferences"`
}

type qaInput struct {
	Rubric   qaRubric    `json:"rubric"`
	Sessions []qaSession `json:"sessions"`
}

type qaVerdict struct {
	SessionID string `json:"session_id"`
	Verdict   string `json:"verdict"` // keep | remove
	Rationale string `json:"rationale"`
}

type qaResult struct {
	Verdicts []qaVerdict `json:"verdicts"`
}

// buildJudgeInput assembles one rubric's judge payload from the frozen pool.
// Candidates are the full pre-QA source theme (re-selection from scratch, not
// an edit of the kept set). A candidate without a pool entry is a hard error:
// silently skipping it would shrink the audit.
func buildJudgeInput(r Rubric, entries map[string]insights.AgentSessionAnalysis) (qaInput, error) {
	in := qaInput{Rubric: qaRubric{ID: r.ID, Statement: r.Statement, RequiredNuances: r.RequiredNuances}}
	for _, id := range sortedSet(r.SourceThemeSessionIDs) {
		a, ok := entries[id]
		if !ok {
			return qaInput{}, fmt.Errorf("rubric %s: source-theme session %s has no pool entry", r.ID, id)
		}
		in.Sessions = append(in.Sessions, qaSession{
			SessionID:           id,
			UnderlyingGoal:      a.UnderlyingGoal,
			SessionType:         a.SessionType,
			Outcome:             a.Outcome,
			BriefSummary:        a.BriefSummary,
			FrictionIncidents:   a.FrictionIncidents,
			StandingPreferences: a.StandingPreferences,
		})
	}
	return in, nil
}

// validateQAVerdicts rejects judge output that does not cover exactly the
// input's session set with valid, justified verdicts.
func validateQAVerdicts(in qaInput, res qaResult) error {
	want := map[string]bool{}
	for _, s := range in.Sessions {
		want[s.SessionID] = true
	}
	seen := map[string]bool{}
	for _, v := range res.Verdicts {
		if !want[v.SessionID] {
			return fmt.Errorf("verdict for unknown session %q", v.SessionID)
		}
		if seen[v.SessionID] {
			return fmt.Errorf("duplicate verdict for session %q", v.SessionID)
		}
		seen[v.SessionID] = true
		if v.Verdict != "keep" && v.Verdict != "remove" {
			return fmt.Errorf("session %s: invalid verdict %q", v.SessionID, v.Verdict)
		}
		if v.Rationale == "" {
			return fmt.Errorf("session %s: empty rationale", v.SessionID)
		}
	}
	if len(seen) != len(want) {
		return fmt.Errorf("verdicts cover %d of %d sessions", len(seen), len(want))
	}
	return nil
}

// loadPoolEntries reads the frozen baseline-pool entries for the given ids.
func loadPoolEntries(poolDir string, ids []string) (map[string]insights.AgentSessionAnalysis, error) {
	out := map[string]insights.AgentSessionAnalysis{}
	for _, id := range sortedSet(ids) {
		raw, err := os.ReadFile(filepath.Join(poolDir, id+".json"))
		if err != nil {
			return nil, err
		}
		var a insights.AgentSessionAnalysis
		if err := json.Unmarshal(raw, &a); err != nil {
			return nil, fmt.Errorf("pool entry %s: %w", id, err)
		}
		out[id] = a
	}
	return out, nil
}

// newAnchorQACommand builds the judge invocation: embedded prompt as -p,
// payload on stdin, pinned model, scrubbed env (same discipline as the
// matcher).
func newAnchorQACommand(ctx context.Context, stdin []byte, configDir, workDir string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "claude", "-p", anchorQAPrompt,
		"--output-format", "json",
		"--json-schema", anchorQASchema,
		"--model", AnchorQAJudgeModel,
		"--no-session-persistence")
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.Env = scrubbedMatcherEnv()
	if configDir != "" {
		cmd.Env = append(cmd.Env, "CLAUDE_CONFIG_DIR="+configDir)
	}
	if workDir != "" {
		cmd.Dir = workDir
	}
	return cmd
}

// judgeRubricAnchors runs the pinned judge once for one rubric, with the
// matcher's retry-and-validate discipline.
func judgeRubricAnchors(ctx context.Context, in qaInput, configDir, workDir string) (qaResult, error) {
	stdin, err := json.Marshal(in)
	if err != nil {
		return qaResult{}, err
	}
	var lastErr error
	for attempt := 0; attempt < matcherAttempts; attempt++ {
		jctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		out, runErr := newAnchorQACommand(jctx, stdin, configDir, workDir).Output()
		cancel()
		if runErr != nil {
			lastErr = wrapMatcherExit(out, runErr)
			continue
		}
		var env matcherEnvelope
		if lastErr = json.Unmarshal(out, &env); lastErr != nil {
			continue
		}
		if env.IsError {
			lastErr = fmt.Errorf("claude reported is_error: %s", env.Result)
			continue
		}
		if len(env.StructuredOutput) == 0 || string(env.StructuredOutput) == "null" {
			lastErr = fmt.Errorf("null/missing structured_output")
			continue
		}
		var res qaResult
		if lastErr = json.Unmarshal(env.StructuredOutput, &res); lastErr != nil {
			continue
		}
		if lastErr = validateQAVerdicts(in, res); lastErr != nil {
			continue
		}
		return res, nil
	}
	return qaResult{}, fmt.Errorf("anchor-QA judge failed after %d attempts: %w", matcherAttempts, lastErr)
}
