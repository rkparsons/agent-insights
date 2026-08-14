package eval

import "time"

// The v1 per-repo synthesis shapes, moved here when the pipeline dropped them
// (plan Task 7). What survives is exactly what frozen v1 ground truth is
// written in: the eval harness still reads those reports for historical
// records and for the as_consumed control's pre-strip anchors (rubric.go's
// PreStripAnchors). The v1 model-output shapes died with the v1 L2 eval stage
// (plan Task 8) — scoring reads insights.GlobalSynthesisJSON now. These are
// data definitions only; no v1 pipeline behavior survives here.

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
	SignalRefs      []string       `json:"signal_refs,omitempty"`
	OverGeneralized bool           `json:"over_generalized,omitempty"`
}

type Recommendation struct {
	Type           string   `json:"type"`
	Title          string   `json:"title,omitempty"`
	Statement      string   `json:"statement"`
	ThemeRefs      []int    `json:"theme_refs"`
	SessionCount   int      `json:"session_count"`
	LastSeen       string   `json:"last_seen,omitempty"`
	Quotes         []string `json:"quotes"`
	AlreadyAdopted string   `json:"already_adopted"`
	Audience       string   `json:"audience,omitempty"`
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
