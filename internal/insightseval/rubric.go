package insightseval

import (
	"embed"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed rubrics/*.yaml
var rubricFS embed.FS

// Rubric encodes one eval target from insights-eval-spec.md. Rubrics co-evolve
// with the harness and scoring code, so they live embedded here, not in the
// append-only data repo.
type Rubric struct {
	ID                       string   `yaml:"id"`
	Part                     string   `yaml:"part"`
	Tier                     string   `yaml:"tier"`
	Corroboration            string   `yaml:"corroboration"`
	Repos                    []string `yaml:"repos"`
	Statement                string   `yaml:"statement"`
	RequiredNuances          []string `yaml:"required_nuances"`
	ForbiddenGeneralizations []string `yaml:"forbidden_generalizations"`
	AnchorSessionIDs         []string `yaml:"anchor_session_ids"`
	Surface                  string   `yaml:"surface"`
	PassAt                   string   `yaml:"pass_at"`
	SeedStatus               string   `yaml:"seed_status,omitempty"`
	Notes                    string   `yaml:"notes,omitempty"`
}

var validStatuses = map[string]bool{
	"must_pass": true, "expected_fail": true, "expected_partial": true,
	"needs_reconfirmation": true, "invalidated": true,
}

var validTiers = map[string]bool{"HIGH": true, "MODERATE-HIGH": true, "MODERATE": true, "MEDIUM": true}

func parseRubric(name string, raw []byte) (Rubric, error) {
	var r Rubric
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&r); err != nil {
		return r, fmt.Errorf("rubric %s: %w", name, err)
	}
	fail := func(msg string) (Rubric, error) { return r, fmt.Errorf("rubric %s: %s", name, msg) }
	if r.ID == "" || r.Statement == "" {
		return fail("id and statement are required")
	}
	switch r.Part {
	case "regression", "gap":
		if !validTiers[r.Tier] {
			return fail("tier must be HIGH|MODERATE-HIGH|MODERATE|MEDIUM")
		}
		if r.Surface != "theme" && r.Surface != "recommendation" && r.Surface != "either" {
			return fail("surface must be theme|recommendation|either")
		}
		if len(r.Repos) == 0 {
			return fail("repos must name at least the expected bucket")
		}
		if r.Part == "gap" && len(r.AnchorSessionIDs) > 0 {
			return fail("gap rubrics carry no anchors (nothing persisted for misses)")
		}
	case "negative":
		if len(r.AnchorSessionIDs) > 0 {
			return fail("negative rubrics carry no anchors (no corroboration channel)")
		}
	default:
		return fail("part must be regression|gap|negative")
	}
	switch r.PassAt {
	case "full", "partial":
	case "":
		if r.Tier == "HIGH" {
			r.PassAt = "full"
		} else {
			r.PassAt = "partial"
		}
	default:
		return fail("pass_at must be full|partial")
	}
	if r.SeedStatus != "" && !validStatuses[r.SeedStatus] {
		return fail("seed_status must be a valid status")
	}
	return r, nil
}

// LoadRubrics parses and validates every embedded rubric, sorted by id.
func LoadRubrics() ([]Rubric, error) {
	entries, err := rubricFS.ReadDir("rubrics")
	if err != nil {
		return nil, err
	}
	var out []Rubric
	seen := map[string]string{}
	for _, e := range entries {
		raw, err := rubricFS.ReadFile(filepath.ToSlash(filepath.Join("rubrics", e.Name())))
		if err != nil {
			return nil, err
		}
		r, err := parseRubric(e.Name(), raw)
		if err != nil {
			return nil, err
		}
		if prev, dup := seen[r.ID]; dup {
			return nil, fmt.Errorf("rubric id %s duplicated in %s and %s", r.ID, prev, e.Name())
		}
		seen[r.ID] = e.Name()
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// RubricSetHash covers file names and bytes, so a rubric edit re-keys the
// scoring stage without touching pipeline caches.
func RubricSetHash() (string, error) {
	entries, err := rubricFS.ReadDir("rubrics")
	if err != nil {
		return "", err
	}
	parts := []string{"rubric-set"}
	for _, e := range entries { // ReadDir is sorted
		raw, err := rubricFS.ReadFile("rubrics/" + e.Name())
		if err != nil {
			return "", err
		}
		parts = append(parts, e.Name(), string(raw))
	}
	return cacheKey(parts...), nil
}

func defaultStatus(r Rubric) string {
	if r.SeedStatus != "" {
		return r.SeedStatus
	}
	if r.Part == "gap" {
		return "expected_fail"
	}
	return "must_pass"
}

// SeedStatuses fills benchmark.json's statuses map with each non-negative
// rubric's default, never overwriting an existing entry (ratchet flips are
// deliberate manual edits). Everything else in the file is preserved.
func SeedStatuses(dataDir string) (int, error) {
	rubrics, err := LoadRubrics()
	if err != nil {
		return 0, err
	}
	b, ok, err := loadBenchmark(dataDir)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, fmt.Errorf("no benchmark.json in %s — run the freeze first", dataDir)
	}
	if b.Statuses == nil {
		b.Statuses = map[string]string{}
	}
	added := 0
	for _, r := range rubrics {
		if r.Part == "negative" {
			continue
		}
		if _, present := b.Statuses[r.ID]; present {
			continue
		}
		b.Statuses[r.ID] = defaultStatus(r)
		added++
	}
	if added == 0 {
		return 0, nil
	}
	return added, writeJSON(filepath.Join(dataDir, "benchmark.json"), b)
}

// Statuses returns benchmark.json's per-target status map.
func Statuses(dataDir string) (map[string]string, error) {
	b, ok, err := loadBenchmark(dataDir)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("no benchmark.json in %s", dataDir)
	}
	return b.Statuses, nil
}
