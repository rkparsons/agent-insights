package insights

import "time"

// ContractVersion is the schema_version stamped on every CLI JSON output
// (StatusJSON, ShowJSON, GlobalSynthesisJSON). This is the cross-repo
// contract: a standalone agent-insights binary will vendor these shapes, so
// field names and json tags here are load-bearing. Bump on a breaking change.
const ContractVersion = 2

// StatusJSON is `insights status --json`'s stdout payload.
type StatusJSON struct {
	SchemaVersion int          `json:"schema_version"`
	StoreRoot     string       `json:"store_root"`
	LogPath       string       `json:"log_path"`
	Running       bool         `json:"running"`
	RunningOp     string       `json:"running_op,omitempty"` // "analyze" | "synthesize" | "enrich"; set only while running
	DueRepos      []string     `json:"due_repos"`
	ActedKeys     []string     `json:"acted_keys"`
	LastRun       *LastRunJSON `json:"last_run,omitempty"`
}

// LastRunJSON mirrors synthesis.RunState's user-relevant fields, timestamps
// rendered as RFC3339 strings.
type LastRunJSON struct {
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at,omitempty"`
	Error      string `json:"error,omitempty"`
}

// BuildStatus assembles StatusJSON from already-computed sources. Due repos,
// acted keys, and last-run state all require the synthesis package, which
// this package cannot import (synthesis already imports insights, so the
// reverse would cycle) — callers (cmd/insights.go) gather those and pass
// them in. nil slices normalize to empty so the JSON output never emits
// `null` for due_repos/acted_keys.
func BuildStatus(storeRoot, logPath string, running bool, runningOp string, dueRepos, actedKeys []string, lastRun *LastRunJSON) StatusJSON {
	if !running {
		runningOp = ""
	}
	if dueRepos == nil {
		dueRepos = []string{}
	}
	if actedKeys == nil {
		actedKeys = []string{}
	}
	return StatusJSON{
		SchemaVersion: ContractVersion,
		StoreRoot:     storeRoot,
		LogPath:       logPath,
		Running:       running,
		RunningOp:     runningOp,
		DueRepos:      dueRepos,
		ActedKeys:     actedKeys,
		LastRun:       lastRun,
	}
}

// ShowJSON is `insights show --json`'s stdout payload.
type ShowJSON struct {
	SchemaVersion int             `json:"schema_version"`
	Syntheses     []SynthesisJSON `json:"syntheses"`
}

// WindowJSON mirrors synthesis.Window field-for-field.
type WindowJSON struct {
	From          string `json:"from"`
	To            string `json:"to"`
	SessionCount  int    `json:"session_count"`
	AnalyzedCount int    `json:"analyzed_count"`
}

// ThemeJSON mirrors synthesis.Theme field-for-field.
type ThemeJSON struct {
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

// RecommendationJSON mirrors synthesis.Recommendation field-for-field, plus
// one addition: ActedKey. The pipeline computes synthesis.ActedKey(rec, repo)
// so the TUI (and any other consumer) never reimplements the hash.
type RecommendationJSON struct {
	Type           string   `json:"type"`
	Title          string   `json:"title,omitempty"`
	Statement      string   `json:"statement"`
	ThemeRefs      []int    `json:"theme_refs"`
	SessionCount   int      `json:"session_count"`
	LastSeen       string   `json:"last_seen,omitempty"`
	Quotes         []string `json:"quotes"`
	AlreadyAdopted string   `json:"already_adopted"`
	Audience       string   `json:"audience,omitempty"`
	ActedKey       string   `json:"acted_key"`
}

// MetaJSON mirrors synthesis.Meta field-for-field.
type MetaJSON struct {
	Model            string      `json:"model"`
	UnthemedFriction int         `json:"unthemed_friction"`
	ValidationErrors []string    `json:"validation_errors,omitempty"`
	PrefCountByRec   map[int]int `json:"pref_count_by_rec,omitempty"`
}

// SynthesisJSON mirrors synthesis.RepoSynthesis field-for-field.
type SynthesisJSON struct {
	Repo            string               `json:"repo"`
	GeneratedAt     time.Time            `json:"generated_at"`
	Window          WindowJSON           `json:"window"`
	Themes          []ThemeJSON          `json:"themes"`
	Recommendations []RecommendationJSON `json:"recommendations"`
	Meta            MetaJSON             `json:"meta"`
}
