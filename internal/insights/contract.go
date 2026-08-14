package insights

// ContractVersion is the schema_version stamped on every CLI JSON output
// (StatusJSON and GlobalSynthesisJSON, `show --json`'s payload). This is the cross-repo
// contract: a standalone agent-insights binary will vendor these shapes, so
// field names and json tags here are load-bearing. Bump on a breaking change.
const ContractVersion = 2

// StatusJSON is `insights status --json`'s stdout payload.
type StatusJSON struct {
	SchemaVersion int          `json:"schema_version"`
	StoreRoot     string       `json:"store_root"`
	LogPath       string       `json:"log_path"`
	Running       bool         `json:"running"`
	RunningOp     string       `json:"running_op,omitempty"` // one of insights.LockOps; set only while running
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
