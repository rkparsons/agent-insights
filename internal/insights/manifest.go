package insights

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// ManifestEntry records one file-less outcome (gated or errored). Analyzed sessions
// are tracked by their stamped analysis file, not here, so the manifest stays small.
type ManifestEntry struct {
	SessionID       string    `json:"session_id"`
	TranscriptMtime time.Time `json:"transcript_mtime"`
	Outcome         string    `json:"outcome"`
	Threshold       int       `json:"threshold,omitempty"`
	Error           string    `json:"error,omitempty"`
	At              time.Time `json:"at"`
}

func manifestPath() string { return filepath.Join(InsightsDir(), "manifest.jsonl") }

// loadManifest reads the append-only ledger, last entry per session-id winning.
func loadManifest() (map[string]ManifestEntry, error) {
	f, err := os.Open(manifestPath())
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]ManifestEntry{}, nil
		}
		return nil, err
	}
	defer f.Close()
	out := map[string]ManifestEntry{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var e ManifestEntry
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		out[e.SessionID] = e
	}
	return out, sc.Err()
}

// appendManifest appends one entry to the ledger.
func appendManifest(e ManifestEntry) error {
	if err := os.MkdirAll(InsightsDir(), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(e)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(manifestPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(data, '\n'))
	return err
}
