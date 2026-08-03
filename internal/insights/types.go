// Package insights interprets a decoded Claude Code transcript into the
// deterministic half of an AgentSessionAnalysis: a token-budgeted ReducedInput (for the
// LLM analysis skill) and an AgentSessionStats struct (exact numbers an LLM must never
// guess). The decoder/format lives in internal/transcript; this package never parses
// raw JSON.
package insights

import (
	"time"

	"github.com/rkparsons/agent-insights/internal/transcript"
)

// TokenUsage aggregates assistant usage. Input/Output/CacheCreation are summed
// over distinct message.id; CacheReadPeak is the max (summing re-reads the
// growing cached prefix and inflates ~95-238x).
type TokenUsage struct {
	Input         int `json:"input"`
	Output        int `json:"output"`
	CacheCreation int `json:"cache_creation"`
	CacheReadPeak int `json:"cache_read_peak"`
}

// AgentSessionStats is the deterministic half of an AgentSessionAnalysis. The integrator merges
// it with the skill's judged fields.
type AgentSessionStats struct {
	SessionID string `json:"session_id"`
	Repo      string `json:"repo"` // Config.Resolver()(cwd) path; "" if unmatched
	Cwd       string `json:"cwd"`
	GitBranch string `json:"git_branch"`
	Version   string `json:"version"` // last observed transcript version
	AiTitle   string `json:"ai_title"`

	Start     time.Time     `json:"start"`
	End       time.Time     `json:"end"`
	WallClock time.Duration `json:"wall_clock"`

	ModelMix   map[string]int `json:"model_mix"` // model -> distinct-message count
	Tokens     TokenUsage     `json:"tokens"`
	ToolCounts map[string]int `json:"tool_counts"` // tool_use name -> count (per block)
	ToolErrors int            `json:"tool_errors"` // is_error blocks that are NOT rejection/interrupt

	Edits        int      `json:"edits"`         // Edit tool_use attempts
	Writes       int      `json:"writes"`        // Write tool_use attempts
	LinesAdded   int      `json:"lines_added"`   // structuredPatch additions (successes)
	LinesRemoved int      `json:"lines_removed"` // structuredPatch deletions (successes)
	FilesTouched []string `json:"files_touched"`

	SubagentFanout int      `json:"subagent_fanout"` // Agent tool_use count
	Skills         []string `json:"skills"`
	Plugins        []string `json:"plugins"`
	Subagents      []string `json:"subagents"` // distinct Agent input.subagent_type

	UserTurns         int `json:"user_turns"`
	AssistantTurns    int `json:"assistant_turns"` // distinct message.id count
	Interrupts        int `json:"interrupts"`
	Rejections        int `json:"rejections"`
	TaskNotifications int `json:"task_notifications"`

	UserTurnFingerprints []string `json:"user_turn_fingerprints"`

	// Phase-3 deterministic detectors (facts tier).
	MechanicalFriction   map[string]int    `json:"mechanical_friction,omitempty"`
	MechanicalExemplars  map[string]string `json:"mechanical_exemplars,omitempty"`
	OtherErrorSignatures map[string]int    `json:"other_error_signatures,omitempty"`
	DirectiveClauses     []DirectiveClause `json:"directive_clauses,omitempty"`

	Canary transcript.Canary `json:"canary"`
}

// ReducedInput is the stdin payload for the analysis skill.
type ReducedInput struct {
	Text          string
	Chars         int
	KeptEvents    int
	DroppedEvents int
}

// RepoResolver maps a cwd to a repo identity (injected so callers can wire it
// however they resolve repos; see Config.Resolver).
type RepoResolver func(cwd string) string

// SessionExtraction is the output of Extract.
type SessionExtraction struct {
	Reduced  ReducedInput
	Stats    AgentSessionStats
	Verbatim VerbatimIndex
}
