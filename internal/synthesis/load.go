package synthesis

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rkparsons/agent-insights/internal/insights"
)

// LoadLatestGlobal returns the newest stored global synthesis, newest-wins by
// filename (snapshotTimeLayout makes lexical order chronological). A malformed
// file is skipped rather than fatal — one bad snapshot must not blank the
// section — so the result is the newest PARSEABLE snapshot. ok is false when
// the store holds none (never run), which is a valid empty state, not an error.
func LoadLatestGlobal() (insights.GlobalSynthesisJSON, bool, error) {
	dir := globalDir()
	names, err := snapshotJSONNames(dir)
	if os.IsNotExist(err) {
		return insights.GlobalSynthesisJSON{}, false, nil
	}
	if err != nil {
		return insights.GlobalSynthesisJSON{}, false, err
	}
	for i := len(names) - 1; i >= 0; i-- {
		data, err := os.ReadFile(filepath.Join(dir, names[i]))
		if err != nil {
			log.Printf("synthesis: read global snapshot %q: %v", names[i], err)
			continue
		}
		var s insights.GlobalSynthesisJSON
		if err := json.Unmarshal(data, &s); err != nil {
			log.Printf("synthesis: skipping malformed global snapshot %q: %v", names[i], err)
			continue
		}
		return s, true, nil
	}
	return insights.GlobalSynthesisJSON{}, false, nil
}

// snapshotJSONNames lists a snapshot dir's .json filenames in ascending
// (chronological) order — see snapshotTimeLayout.
func snapshotJSONNames(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}
