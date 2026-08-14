package insights

import "time"

// FrictionIncident is one discrete friction incident judged by the skill, after
// quote validation. QuoteUnverified is set when the LLM supplied an evidence quote
// that did not validate verbatim and was therefore cleared.
type FrictionIncident struct {
	Type            string `json:"type"`
	OneLine         string `json:"one_line"`
	EvidenceQuote   string `json:"evidence_quote,omitempty"`
	File            string `json:"file,omitempty"`
	QuoteUnverified bool   `json:"quote_unverified,omitempty"`
}

// StandingPreference is one durable user-stated working rule. EvidenceQuote is the
// user's own verbatim words; a preference whose quote fails validation is dropped,
// so every surviving preference carries a verified quote.
type StandingPreference struct {
	Rule          string `json:"rule"`
	EvidenceQuote string `json:"evidence_quote"`
	Scope         string `json:"scope,omitempty"`
}

// JudgedFields mirrors the analyzing-agent-sessions skill schema. The tags are
// load-bearing: they unmarshal the skill's snake_case structured_output and drive
// the artifact output.
type JudgedFields struct {
	UnderlyingGoal      string               `json:"underlying_goal"`
	SessionType         string               `json:"session_type"`
	Outcome             string               `json:"outcome"`
	BriefSummary        string               `json:"brief_summary"`
	FrictionIncidents   []FrictionIncident   `json:"friction_incidents"`
	StandingPreferences []StandingPreference `json:"standing_preferences"`
}

// AgentSessionAnalysis is the complete per-session artifact: deterministic stats
// merged with the validated judged fields. JudgedFields is embedded so its fields
// flatten to the artifact's top level while Stats nests under "stats".
type AgentSessionAnalysis struct {
	Stats        AgentSessionStats `json:"stats"`
	JudgedFields                   // embedded

	// TranscriptMtime is the decode-time transcript mtime, stamped by the
	// orchestrator (not the producer) so incremental detection is race-free.
	TranscriptMtime time.Time `json:"transcript_mtime"`

	// AnalyzedAt is when this analysis was written, taken from its store
	// file's mtime at load (synthesis.LoadAnalyses). Deliberately not
	// persisted: the file's own mtime is the fact, and a stored copy would
	// disagree with it after any rewrite. Due-ness reads this rather than
	// TranscriptMtime so a backfill of old transcripts counts as new work.
	AnalyzedAt time.Time `json:"-"`
}
