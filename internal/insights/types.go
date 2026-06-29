// Package insights interprets a decoded Claude Code transcript into the
// deterministic half of a SessionFacet: a token-budgeted ReducedInput (for the
// LLM facet skill) and a FacetStats struct (exact numbers an LLM must never
// guess). The decoder/format lives in sources/claude; this package never parses
// raw JSON.
package insights

import (
	"time"

	"tmux-ctrl/internal/sources/claude"
)

// TokenUsage aggregates assistant usage. Input/Output/CacheCreation are summed
// over distinct message.id; CacheReadPeak is the max (summing re-reads the
// growing cached prefix and inflates ~95-238x).
type TokenUsage struct {
	Input         int
	Output        int
	CacheCreation int
	CacheReadPeak int
}

// FacetStats is the deterministic half of a SessionFacet. The integrator merges
// it with the skill's judged fields.
type FacetStats struct {
	SessionID string
	Repo      string // userconfig.LookupRepo(cwd) path; "" if unmatched
	Cwd       string
	GitBranch string
	Version   string // last observed transcript version
	AiTitle   string

	Start     time.Time
	End       time.Time
	WallClock time.Duration

	ModelMix   map[string]int // model -> distinct-message count
	Tokens     TokenUsage
	ToolCounts map[string]int // tool_use name -> count (per block)
	ToolErrors int            // is_error blocks that are NOT rejection/interrupt

	Edits        int // Edit tool_use attempts
	Writes       int // Write tool_use attempts
	LinesAdded   int // structuredPatch additions (successes)
	LinesRemoved int // structuredPatch deletions (successes)
	FilesTouched []string

	SubagentFanout int // Agent tool_use count
	Skills         []string
	Plugins        []string
	Subagents      []string // distinct Agent input.subagent_type

	UserTurns         int
	AssistantTurns    int // distinct message.id count
	Interrupts        int
	Rejections        int
	TaskNotifications int

	UserTurnFingerprints []string

	Canary claude.Canary
}

// ReducedInput is the stdin payload for the facet skill.
type ReducedInput struct {
	Text          string
	Chars         int
	KeptEvents    int
	DroppedEvents int
}

// RepoResolver maps a cwd to a repo identity (injected so insights stays
// decoupled from userconfig; the caller wires LookupRepo).
type RepoResolver func(cwd string) string

// Result is the output of Extract.
type Result struct {
	Reduced  ReducedInput
	Stats    FacetStats
	Verbatim VerbatimIndex
}
