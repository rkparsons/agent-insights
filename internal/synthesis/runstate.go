package synthesis

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
)

// RunState is the last-run record for `insights synthesize`, read by the TUI
// to show due/running/error state.
type RunState struct {
	Status     string     `json:"status"` // "running" | "ok" | "failed"
	PID        int        `json:"pid"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"` // pointer: omitempty is a no-op on time.Time itself
	Written    int        `json:"written"`               // 0 or 1: a global run stores one snapshot or none
	Reason     string     `json:"reason,omitempty"`
	LogPath    string     `json:"log_path,omitempty"`
}

func runStatePath() string { return filepath.Join(insights.InsightsDir(), "synthesis-run.json") }

func writeRunState(rs RunState) {
	data, err := json.MarshalIndent(rs, "", "  ")
	if err != nil {
		return
	}
	if err := atomicWrite(runStatePath(), data); err != nil {
		fmt.Fprintf(os.Stderr, "synthesis: run state: %v\n", err)
	}
}

// ReadRunState loads the last synthesis run record; ok=false when absent or
// unparseable.
func ReadRunState() (RunState, bool) {
	data, err := os.ReadFile(runStatePath())
	if err != nil {
		return RunState{}, false
	}
	var rs RunState
	if err := json.Unmarshal(data, &rs); err != nil {
		return RunState{}, false
	}
	return rs, true
}
