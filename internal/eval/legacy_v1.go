package eval

import "time"

// Task 8-10: the v1 per-repo synthesis shapes, moved here when the pipeline
// dropped them (plan Task 7). The eval harness still reads them from its own
// frozen ground truth and its verify-stage cache, and its rubric anchors are
// still theme-indexed — all of which plan Tasks 8-10 re-source against the v2
// GlobalSynthesis. Nothing outside internal/eval produces these any more, and
// they die with that rework; they are data definitions only, so no v1 pipeline
// behavior survives here.

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

type RawTheme struct {
	Title       string   `json:"title"`
	Kind        string   `json:"kind"`
	Summary     string   `json:"summary"`
	EvidenceIDs []string `json:"evidence_ids"`
	SignalRefs  []string `json:"signal_refs,omitempty"`
	CitedQuotes []string `json:"cited_quotes"`
}

type RawRec struct {
	Type        string   `json:"type"`
	Title       string   `json:"title"`
	Statement   string   `json:"statement"`
	EvidenceIDs []string `json:"evidence_ids"`
	ThemeRefs   []int    `json:"theme_refs"`
	CitedQuotes []string `json:"cited_quotes"`
	Audience    string   `json:"audience,omitempty"`
}

type RawSynthesis struct {
	Themes          []RawTheme `json:"themes"`
	Recommendations []RawRec   `json:"recommendations"`
}

type ValidationReport struct {
	RawQuoteDropRate float64
	HardErrors       []string
}
