package insights

import "time"

// GlobalSynthesisJSON is `insights show --json`'s stdout payload under the v2
// (schema_version 2) contract: one cross-repo synthesis pass, replacing v1's
// per-repo payload wholesale. It is the model's RawGlobalSynthesis
// output plus the fields Go always computes/overwrites (window, repos,
// generated_at, and each Finding's Go-owned fields).
type GlobalSynthesisJSON struct {
	SchemaVersion int              `json:"schema_version"` // always 2
	GeneratedAt   time.Time        `json:"generated_at"`
	Window        WindowBoundsJSON `json:"window"`
	Repos         []RepoStatsJSON  `json:"repos"`
	Findings      []FindingJSON    `json:"findings"`
	Dropped       []DroppedJSON    `json:"dropped"`
	Meta          GlobalMetaJSON   `json:"meta"`
}

// WindowBoundsJSON is a date range, reused for the global envelope and each
// per-repo entry in RepoStatsJSON.
type WindowBoundsJSON struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// RepoStatsJSON is one bundled repo's contribution to the global run.
type RepoStatsJSON struct {
	Key           string           `json:"key"`
	Window        WindowBoundsJSON `json:"window"`
	SessionCount  int              `json:"session_count"`
	AnalyzedCount int              `json:"analyzed_count"`
}

// AssetJSON is a Finding's proposed deliverable. Target/Content are optional
// only for asset.type "habit", whose deliverable is its statement rather than
// a file, skill, or setting.
type AssetJSON struct {
	Type    string `json:"type"`              // claude_md_rule|repo_doc|new_skill|hook|setting|habit
	Target  string `json:"target,omitempty"`  // empty allowed for habit
	Content string `json:"content,omitempty"` // empty allowed for habit
}

// AdoptedJSON records whether the proposed asset already exists at its
// target. For an escalated finding this is about the fix itself, not the
// pre-existing rule being escalated — that one lives in EscalatedFromJSON.
type AdoptedJSON struct {
	Verdict    string `json:"verdict"` // yes|no|unknown
	SourcePath string `json:"source_path,omitempty"`
	Excerpt    string `json:"excerpt,omitempty"`
}

// EscalatedFromJSON cites the existing rule an escalated finding
// strengthens; its presence is what marks a finding as an escalation.
// Recency (has a cited violation happened since this rule was written) is
// arbitrated by Go, never the model — see the verifier.
type EscalatedFromJSON struct {
	SourcePath string `json:"source_path"`
	Excerpt    string `json:"excerpt"`
}

// FindingJSON is one ranked, asset-oriented output of the global synthesis.
// The Go-owned fields (Repos, SessionCount, LastSeen, ActedKey) are always
// overwritten by the verifier from the cited evidence_ids, never trusted from
// the model — see RawFinding for the shape the model actually emits.
type FindingJSON struct {
	Rank           int                `json:"rank"`
	Title          string             `json:"title"`
	Statement      string             `json:"statement"`
	RankRationale  string             `json:"rank_rationale"`
	Asset          AssetJSON          `json:"asset"`
	Audience       string             `json:"audience,omitempty"` // required by verifier on claude_md_rule/repo_doc
	EvidenceIDs    []string           `json:"evidence_ids"`       // "repo/F3" form
	Quotes         []string           `json:"quotes,omitempty"`   // ≤3
	AlreadyAdopted AdoptedJSON        `json:"already_adopted"`
	EscalatedFrom  *EscalatedFromJSON `json:"escalated_from,omitempty"` // set iff the finding escalates an existing rule; never on habit
	// Go-owned:
	Repos        []string `json:"repos"`
	SessionCount int      `json:"session_count"`
	LastSeen     string   `json:"last_seen"`
	ActedKey     string   `json:"acted_key"`
}

// DroppedJSON is evidence the model judged not actionable, kept for eval
// recall scoring and human review rather than silently discarded.
type DroppedJSON struct {
	Summary     string   `json:"summary"`
	Reason      string   `json:"reason"`
	EvidenceIDs []string `json:"evidence_ids"`
}

// GlobalMetaJSON carries the model identity and every soft correction the
// verifier made, preserved as an eval signal (quote drops, adopted-verdict
// downgrades, dropped-finding recency notes, and similar).
type GlobalMetaJSON struct {
	Model           string   `json:"model"`
	ValidationNotes []string `json:"validation_notes,omitempty"`
}

// RawFinding is FindingJSON as the model emits it: the same shape minus the
// four Go-owned fields the verifier always overwrites.
type RawFinding struct {
	Rank           int                `json:"rank"`
	Title          string             `json:"title"`
	Statement      string             `json:"statement"`
	RankRationale  string             `json:"rank_rationale"`
	Asset          AssetJSON          `json:"asset"`
	Audience       string             `json:"audience,omitempty"`
	EvidenceIDs    []string           `json:"evidence_ids"`
	Quotes         []string           `json:"quotes,omitempty"`
	AlreadyAdopted AdoptedJSON        `json:"already_adopted"`
	EscalatedFrom  *EscalatedFromJSON `json:"escalated_from,omitempty"`
}

// RawGlobalSynthesis is GlobalSynthesisJSON as the model emits it: no
// generated_at/window/repos (Go computes the envelope from the bundles it
// already holds) and Findings uses RawFinding.
type RawGlobalSynthesis struct {
	SchemaVersion int            `json:"schema_version"`
	Findings      []RawFinding   `json:"findings"`
	Dropped       []DroppedJSON  `json:"dropped"`
	Meta          GlobalMetaJSON `json:"meta"`
}
