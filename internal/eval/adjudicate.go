package eval

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// AdjKey identifies one contested decision (spec: target id, normalized item
// statement, session-id set, rubric hash, trigger type — resolving only its
// own trigger type). The id set is stored as a hash: adjudications.json needs
// matching, never raw session ids.
type AdjKey struct {
	TargetID   string `json:"target_id"`
	Statement  string `json:"statement"` // normalized item text; "" for item-less triggers
	IDSetHash  string `json:"id_set_hash"`
	RubricHash string `json:"rubric_hash"`
	Trigger    string `json:"trigger"`
}

func (k AdjKey) Hash() string {
	return cacheKey("adj", k.TargetID, k.Statement, k.IDSetHash, k.RubricHash, k.Trigger)
}

type Adjudication struct {
	Key       AdjKey    `json:"key"`
	KeyHash   string    `json:"key_hash"`
	Decision  string    `json:"decision"` // "accept" | "reject"
	Note      string    `json:"note,omitempty"`
	DecidedAt time.Time `json:"decided_at"`
}

// normalizeStatement case-folds and collapses whitespace (the acted-marker
// normalization) so L2 wording jitter in spacing/case never re-keys a decision.
func normalizeStatement(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// idSetHash hashes the deduped, sorted session-id set.
func idSetHash(ids []string) string {
	return cacheKey(append([]string{"idset"}, sortedSet(ids)...)...)
}

func adjudicationsPath(dataDir string) string {
	return filepath.Join(dataDir, "adjudications.json")
}

// LoadAdjudications reads the data repo's decisions, keyed by KeyHash. Every
// entry's stored hash is re-derived from its key fields — a hand-edited entry
// that would silently never match again is an error instead.
func LoadAdjudications(dataDir string) (map[string]Adjudication, error) {
	raw, err := os.ReadFile(adjudicationsPath(dataDir))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Adjudication{}, nil
	}
	if err != nil {
		return nil, err
	}
	var list []Adjudication
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("adjudications.json: %w", err)
	}
	out := map[string]Adjudication{}
	for _, a := range list {
		if a.Decision != "accept" && a.Decision != "reject" {
			return nil, fmt.Errorf("adjudications.json: %s has invalid decision %q", a.KeyHash, a.Decision)
		}
		if a.Key.Hash() != a.KeyHash {
			return nil, fmt.Errorf("adjudications.json: entry %s key_hash does not match its key fields (hand-edit?)", a.KeyHash)
		}
		out[a.KeyHash] = a
	}
	return out, nil
}

// SaveAdjudication upserts one decision (same key → replaced; corrections are
// legitimate) and rewrites the file sorted by key hash, after the same privacy
// scan committed verdicts get.
func SaveAdjudication(dataDir string, a Adjudication) error {
	if a.Decision != "accept" && a.Decision != "reject" {
		return fmt.Errorf("decision must be accept|reject, got %q", a.Decision)
	}
	a.KeyHash = a.Key.Hash()
	existing, err := LoadAdjudications(dataDir)
	if err != nil {
		return err
	}
	existing[a.KeyHash] = a
	list := make([]Adjudication, 0, len(existing))
	for _, e := range existing {
		list = append(list, e)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].KeyHash < list[j].KeyHash })
	data, err := json.Marshal(list)
	if err != nil {
		return err
	}
	if hits := privacyScan(data); len(hits) > 0 {
		return fmt.Errorf("adjudication failed privacy scan (%v) — not written", hits)
	}
	return writeJSON(adjudicationsPath(dataDir), list)
}
