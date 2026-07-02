package synthesis

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"tmux-ctrl/internal/insights"
)

func actedPath() string { return filepath.Join(insights.InsightsDir(), "insights-acted.json") }

func ActedKey(rec Recommendation, sourceRepo string) string {
	norm := strings.Join(strings.Fields(strings.ToLower(rec.Statement)), " ")
	sum := sha256.Sum256([]byte(sourceRepo + "\x00" + norm))
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

func MarkActed(key string) error {
	m, err := LoadActedKeys()
	if err != nil {
		return err
	}
	if m[key] {
		return nil
	}
	m[key] = true
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
