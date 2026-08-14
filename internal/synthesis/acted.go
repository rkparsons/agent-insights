package synthesis

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rkparsons/agent-insights/internal/insights"
)

func actedPath() string { return filepath.Join(insights.InsightsDir(), "insights-acted.json") }

// ActedKey is the acted-store key for a finding: a hash over its asset type
// and normalized statement, with no source repo — a cross-repo finding has
// none. Reworded findings get a new key and resurface; that is the accepted
// limitation, unchanged from v1.
func ActedKey(assetType, statement string) string {
	norm := strings.Join(strings.Fields(strings.ToLower(statement)), " ")
	sum := sha256.Sum256([]byte(assetType + "\x00" + norm))
	return hex.EncodeToString(sum[:])[:16]
}

func LoadActedKeys() (map[string]bool, error) {
	data, err := os.ReadFile(actedPath())
	if os.IsNotExist(err) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, err
	}
	var keys []string
	if err := json.Unmarshal(data, &keys); err != nil {
		return map[string]bool{}, nil // corrupt file is non-fatal; treat as empty
	}
	m := make(map[string]bool, len(keys))
	for _, k := range keys {
		m[k] = true
	}
	return m, nil
}

// MarkActed adds key to the acted-keys store. The read-modify-write is
// guarded by insights.AcquireActedLock so concurrent callers (two TUI
// instances, or the CLI racing itself) never lose one write to the other.
func MarkActed(key string) error {
	lock, err := insights.AcquireActedLock()
	if err != nil {
		return err
	}
	defer lock.Release()

	m, err := LoadActedKeys()
	if err != nil {
		return err
	}
	if m[key] {
		return nil
	}
	m[key] = true
	return writeActedKeys(m)
}

// UnmarkActed removes key from the acted-keys store, resurfacing the
// recommendation in future curations. Used to roll back an acted mark when the
// launch it recorded fails before anything lands (see the insight launch
// failure handler in internal/app). A no-op when the key isn't recorded.
// Guarded by the same acted-file lock as MarkActed.
func UnmarkActed(key string) error {
	lock, err := insights.AcquireActedLock()
	if err != nil {
		return err
	}
	defer lock.Release()

	m, err := LoadActedKeys()
	if err != nil {
		return err
	}
	if !m[key] {
		return nil
	}
	delete(m, key)
	return writeActedKeys(m)
}

func writeActedKeys(m map[string]bool) error {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	data, err := json.Marshal(keys)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(insights.InsightsDir(), 0o755); err != nil {
		return err
	}
	return atomicWrite(actedPath(), data)
}
