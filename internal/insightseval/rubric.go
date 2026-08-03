package insightseval

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
	"tmux-ctrl/internal/synthesis"
)

// Rubric encodes one eval target from docs/insights-eval/insights-eval-spec.md. Rubrics
// name real session ids and employer-specific repos, so they live in the
// private data repo (<dataDir>/rubrics), not embedded in this public tree.
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
	// SourceThemeSessionIDs is the effective pre-QA source-theme id set (the
	// freeze-time, post-meta-strip anchors, before any anchor-QA removal): the
	// size-cap denominator, immutable across QA passes so removals never
	// tighten the cap.
	SourceThemeSessionIDs []string `yaml:"source_theme_session_ids,omitempty"`
	AnchorTheme           string   `yaml:"anchor_theme,omitempty"` // "<bucket>/<theme-index>" in frozen ground truth (pre-strip anchor source)
	Surface               string   `yaml:"surface"`
	PassAt                string   `yaml:"pass_at"`
	SeedStatus            string   `yaml:"seed_status,omitempty"`
	Notes                 string   `yaml:"notes,omitempty"`
	// Hash is the sha256 of the rubric file bytes: adjudication keys and
	// per-rubric matcher payloads re-key when the file changes.
	Hash string `yaml:"-"`
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
	r.Hash = sha256hex(raw)
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
		if len(r.AnchorSessionIDs) > 0 {
			bucket, _, err := parseAnchorTheme(r.AnchorTheme)
			if err != nil {
				return fail("anchor_theme: " + err.Error())
			}
			if bucket != r.Repos[0] {
				return fail("anchor_theme bucket must be repos[0] (the anchor source bucket)")
			}
			if len(r.SourceThemeSessionIDs) == 0 {
				return fail("source_theme_session_ids required with anchors (anchor-QA size-cap denominator)")
			}
			src := stringSet(r.SourceThemeSessionIDs)
			for _, id := range r.AnchorSessionIDs {
				if !src[id] {
					return fail(fmt.Sprintf("anchor %s not in source_theme_session_ids (kept anchors must be a subset)", id))
				}
			}
		} else {
			if r.AnchorTheme != "" {
				return fail("anchor_theme requires anchor_session_ids")
			}
			if len(r.SourceThemeSessionIDs) > 0 {
				return fail("source_theme_session_ids requires anchor_session_ids")
			}
		}
	case "negative":
		if len(r.AnchorSessionIDs) > 0 {
			return fail("negative rubrics carry no anchors (no corroboration channel)")
		}
		if r.AnchorTheme != "" {
			return fail("negative rubrics carry no anchor_theme")
		}
		if len(r.SourceThemeSessionIDs) > 0 {
			return fail("negative rubrics carry no source_theme_session_ids")
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

// rubricEntries lists <dataDir>/rubrics's *.yaml files, sorted by name (as
// os.ReadDir already returns them). Fails closed: a missing rubrics/ dir
// names the expected path rather than silently yielding zero rubrics — a
// wrong --data or an unchecked-out data repo must not look like "no rubrics".
func rubricEntries(dataDir string) (string, []os.DirEntry, error) {
	dir := filepath.Join(dataDir, "rubrics")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return dir, nil, fmt.Errorf("no rubrics directory at %s — expected the eval data repo's rubrics/ (check --data / dataDir points at a checked-out insights-eval-data)", dir)
		}
		return dir, nil, err
	}
	var out []os.DirEntry
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".yaml") {
			out = append(out, e)
		}
	}
	return dir, out, nil
}

// LoadRubrics parses and validates every rubric under <dataDir>/rubrics,
// sorted by id.
func LoadRubrics(dataDir string) ([]Rubric, error) {
	dir, entries, err := rubricEntries(dataDir)
	if err != nil {
		return nil, err
	}
	var out []Rubric
	seen := map[string]string{}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
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
func RubricSetHash(dataDir string) (string, error) {
	dir, entries, err := rubricEntries(dataDir)
	if err != nil {
		return "", err
	}
	parts := []string{"rubric-set"}
	for _, e := range entries { // sorted
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
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
	rubrics, err := LoadRubrics(dataDir)
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

// NuanceWatermarks returns benchmark.json's per-target nuance watermarks
// (empty when none are recorded — only recalibrated targets carry one).
func NuanceWatermarks(dataDir string) (map[string]int, error) {
	b, ok, err := loadBenchmark(dataDir)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("no benchmark.json in %s", dataDir)
	}
	return b.NuanceWatermarks, nil
}

// parseAnchorTheme splits "<bucket>/<theme-index>" — the frozen ground-truth
// theme the rubric's anchors were copied from.
func parseAnchorTheme(s string) (string, int, error) {
	i := strings.LastIndex(s, "/")
	if i <= 0 || i == len(s)-1 {
		return "", 0, fmt.Errorf("want <bucket>/<theme-index>, got %q", s)
	}
	idx, err := strconv.Atoi(s[i+1:])
	if err != nil || idx < 0 {
		return "", 0, fmt.Errorf("theme index invalid in %q", s)
	}
	return s[:i], idx, nil
}

// PreStripAnchors returns the deduped session ids of the frozen ground-truth
// theme named by anchor_theme — the anchors BEFORE meta-stripping, straight
// from ground-truth/ (never re-derived, never the rubric file). The run-0
// as_consumed control scores against these. Errors when the rubric's stripped
// anchors are not a subset — that means anchor_theme names the wrong theme.
func PreStripAnchors(truths map[string]synthesis.RepoSynthesis, r Rubric) ([]string, error) {
	if r.AnchorTheme == "" {
		return nil, nil
	}
	bucket, idx, err := parseAnchorTheme(r.AnchorTheme)
	if err != nil {
		return nil, fmt.Errorf("rubric %s: %w", r.ID, err)
	}
	gt, ok := truths[bucket]
	if !ok {
		return nil, fmt.Errorf("rubric %s: no ground truth for bucket %s", r.ID, bucket)
	}
	if idx >= len(gt.Themes) {
		return nil, fmt.Errorf("rubric %s: anchor_theme index %d out of range (%d themes)", r.ID, idx, len(gt.Themes))
	}
	pre := sortedSet(gt.Themes[idx].SessionIDs)
	preSet := stringSet(pre)
	for _, id := range r.AnchorSessionIDs {
		if !preSet[id] {
			return nil, fmt.Errorf("rubric %s: anchor %s not in ground-truth theme %s (anchor_theme wrong?)", r.ID, id, r.AnchorTheme)
		}
	}
	for _, id := range r.SourceThemeSessionIDs {
		if !preSet[id] {
			return nil, fmt.Errorf("rubric %s: source-theme id %s not in ground-truth theme %s (anchor_theme wrong?)", r.ID, id, r.AnchorTheme)
		}
	}
	return pre, nil
}
