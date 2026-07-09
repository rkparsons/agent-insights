package synthesis

import "time"

type Theme struct {
	Title           string         `json:"title"`
	Kind            string         `json:"kind"`
	Summary         string         `json:"summary"`
	Rank            int            `json:"rank"`
	IncidentCount   int            `json:"incident_count,omitempty"`
	SessionCount    int            `json:"session_count"`
	TypeBreakdown   map[string]int `json:"type_breakdown,omitempty"`
	Quotes          []string       `json:"quotes"`
	SessionIDs      []string       `json:"session_ids"`
	SignalRefs      []string       `json:"signal_refs,omitempty"` // G ids the opportunity theme anchored on; must survive the verify-cache round-trip for the opp-recall probe
	OverGeneralized bool           `json:"over_generalized,omitempty"`
}
type Recommendation struct {
	Type           string   `json:"type"`
	Statement      string   `json:"statement"`
	ThemeRefs      []int    `json:"theme_refs"`
	SessionCount   int      `json:"session_count"`
	Quotes         []string `json:"quotes"`
	AlreadyAdopted string   `json:"already_adopted"`
	Audience       string   `json:"audience,omitempty"` // must survive the verify-cache round-trip
}
type Window struct {
	From          string `json:"from"`
	To            string `json:"to"`
	SessionCount  int    `json:"session_count"`
	AnalyzedCount int    `json:"analyzed_count"`
}
type Meta struct {
	Model            string      `json:"model"`
	UnthemedFriction int         `json:"unthemed_friction"`
	ValidationErrors []string    `json:"validation_errors,omitempty"`
	PrefCountByRec   map[int]int `json:"pref_count_by_rec,omitempty"`
}
type RepoSynthesis struct {
	Repo            string           `json:"repo"`
	GeneratedAt     time.Time        `json:"generated_at"`
	Window          Window           `json:"window"`
	Themes          []Theme          `json:"themes"`
	Recommendations []Recommendation `json:"recommendations"`
	Meta            Meta             `json:"meta"`
}

// AdoptChecker classifies whether a recommendation's rule is already adopted.
type AdoptChecker func(rec Recommendation) string // "yes" | "no" | "unknown"
