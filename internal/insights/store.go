package insights

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// InsightsDir is the durable insights state root. Honors TMUX_CTRL_INSIGHTS_DIR for
// tests, mirroring worktreestate.RootDir.
func InsightsDir() string {
	if d := os.Getenv("TMUX_CTRL_INSIGHTS_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "tmux-ctrl", "insights")
}

// AnalysesDir is the flat global pool of per-session analyses, keyed by session-id.
func AnalysesDir() string { return filepath.Join(InsightsDir(), "analyses") }

func analysisPath(sessionID string) string {
	return filepath.Join(AnalysesDir(), sessionID+".json")
}

// WriteAnalysis atomically persists one analysis to analyses/<session-id>.json.
func WriteAnalysis(a AgentSessionAnalysis) error {
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(AnalysesDir(), 0o755); err != nil {
		return err
	}
	return atomicWriteFile(analysisPath(a.Stats.SessionID), data)
}

// ReadAnalysisMtime returns the decode-time transcript mtime stamped into a stored
// analysis, or (zero, false) if the file is absent or unreadable. Used by
// incremental detection.
func ReadAnalysisMtime(sessionID string) (time.Time, bool) {
	data, err := os.ReadFile(analysisPath(sessionID))
	if err != nil {
		return time.Time{}, false
	}
	var stamp struct {
		TranscriptMtime time.Time `json:"transcript_mtime"`
	}
	if err := json.Unmarshal(data, &stamp); err != nil {
		return time.Time{}, false
	}
	return stamp.TranscriptMtime, true
}

// atomicWriteFile writes via a unique temp file in the same dir, fsynced, then
// renamed — so a half-written file never appears at the destination.
func atomicWriteFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
