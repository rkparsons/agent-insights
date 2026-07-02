package synthesis

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
	Statement   string   `json:"statement"`
	EvidenceIDs []string `json:"evidence_ids"`
	ThemeRefs   []int    `json:"theme_refs"`
	CitedQuotes []string `json:"cited_quotes"`
}
type RawSynthesis struct {
	Themes          []RawTheme `json:"themes"`
	Recommendations []RawRec   `json:"recommendations"`
}
