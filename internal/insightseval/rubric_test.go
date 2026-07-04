package insightseval

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"tmux-ctrl/internal/synthesis"
)

func TestLoadRubricsEmbeddedValid(t *testing.T) {
	rubrics, err := LoadRubrics()
	if err != nil {
		t.Fatal(err)
	}
	if len(rubrics) < 9 { // M1-M6 + N-01..N-03 land in this task; Part A later
		t.Fatalf("rubrics = %d", len(rubrics))
	}
	byID := map[string]Rubric{}
	for _, r := range rubrics {
		byID[r.ID] = r
	}
	m3 := byID["M3"]
	if m3.Part != "gap" || m3.Tier != "HIGH" || len(m3.AnchorSessionIDs) != 0 {
		t.Fatalf("M3: %+v", m3)
	}
	if m3.PassAt != "full" {
		t.Fatalf("M3 pass_at default: %q", m3.PassAt)
	}
	if byID["N-01"].Part != "negative" {
		t.Fatalf("N-01: %+v", byID["N-01"])
	}
	// sorted by ID
	for i := 1; i < len(rubrics); i++ {
		if rubrics[i-1].ID >= rubrics[i].ID {
			t.Fatalf("not sorted: %s >= %s", rubrics[i-1].ID, rubrics[i].ID)
		}
	}
	h1, err := RubricSetHash()
	if err != nil || len(h1) != 64 {
		t.Fatalf("hash: %q %v", h1, err)
	}
}

func TestSeedStatusesFillsOnlyMissing(t *testing.T) {
	data := t.TempDir()
	b := Benchmark{
		FrozenAt: time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC),
		Buckets:  map[string]BucketPopulations{"myrepo": {Resolved: true}},
		Statuses: map[string]string{"M1": "must_pass"}, // pre-ratcheted: must survive
	}
	if err := writeJSON(filepath.Join(data, "benchmark.json"), b); err != nil {
		t.Fatal(err)
	}
	added, err := SeedStatuses(data)
	if err != nil {
		t.Fatal(err)
	}
	if added < 5 {
		t.Fatalf("added = %d", added)
	}
	got, ok, err := loadBenchmark(data)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if got.Statuses["M1"] != "must_pass" {
		t.Fatalf("existing status overwritten: %q", got.Statuses["M1"])
	}
	if got.Statuses["M2"] != "expected_fail" {
		t.Fatalf("M2 seed: %q", got.Statuses["M2"])
	}
	if _, present := got.Statuses["N-01"]; present {
		t.Fatal("negative rubrics must not get statuses")
	}
	if !got.Buckets["myrepo"].Resolved || !got.FrozenAt.Equal(b.FrozenAt) {
		t.Fatal("seeding must leave the rest of benchmark.json intact")
	}
	// idempotent
	added, err = SeedStatuses(data)
	if err != nil || added != 0 {
		t.Fatalf("re-seed: added=%d err=%v", added, err)
	}
}

func TestRubricValidationRejectsBadFiles(t *testing.T) {
	bad := []byte("id: X\npart: nonsense\nstatement: s\n")
	if _, err := parseRubric("X.yaml", bad); err == nil || !strings.Contains(err.Error(), "X.yaml") {
		t.Fatalf("want file-named validation error, got %v", err)
	}
	dupAnchor := []byte("id: G1\npart: gap\ntier: HIGH\nsurface: either\nrepos: [tmux-ctrl]\nstatement: s\nanchor_session_ids: [abc]\n")
	if _, err := parseRubric("G1.yaml", dupAnchor); err == nil || !strings.Contains(err.Error(), "anchor") {
		t.Fatalf("gap rubric with anchors must fail, got %v", err)
	}
}

func TestRubricAnchorThemeValidation(t *testing.T) {
	base := "id: C-77\npart: regression\ntier: HIGH\nsurface: theme\nrepos: [client-project]\nstatement: s\n"
	anchored := base + "anchor_session_ids: [abc]\n"
	if _, err := parseRubric("C-77.yaml", []byte(anchored)); err == nil || !strings.Contains(err.Error(), "anchor_theme") {
		t.Fatalf("anchored rubric without anchor_theme must fail: %v", err)
	}
	wrongBucket := anchored + "anchor_theme: tmux-ctrl/3\n"
	if _, err := parseRubric("C-77.yaml", []byte(wrongBucket)); err == nil || !strings.Contains(err.Error(), "repos[0]") {
		t.Fatalf("anchor_theme bucket must be repos[0]: %v", err)
	}
	ok := anchored + "anchor_theme: client-project/3\n"
	r, err := parseRubric("C-77.yaml", []byte(ok))
	if err != nil {
		t.Fatal(err)
	}
	if r.AnchorTheme != "client-project/3" || r.Hash == "" || len(r.Hash) != 64 {
		t.Fatalf("rubric: %+v", r)
	}
	orphan := base + "anchor_theme: client-project/3\n"
	if _, err := parseRubric("C-77.yaml", []byte(orphan)); err == nil {
		t.Fatal("anchor_theme without anchors must fail")
	}
	if _, _, err := parseAnchorTheme("nonsense"); err == nil {
		t.Fatal("parseAnchorTheme must reject un-slashed input")
	}
	if b, i, err := parseAnchorTheme("tmux-ctrl/10"); err != nil || b != "tmux-ctrl" || i != 10 {
		t.Fatalf("parseAnchorTheme: %q %d %v", b, i, err)
	}
}

func TestPreStripAnchorsFromGroundTruth(t *testing.T) {
	truths := map[string]synthesis.RepoSynthesis{
		"client-project": {Themes: []synthesis.Theme{
			{}, {SessionIDs: []string{"m1", "a1", "a2", "a1"}},
		}},
	}
	r := Rubric{ID: "C-77", Repos: []string{"client-project"}, AnchorTheme: "client-project/1",
		AnchorSessionIDs: []string{"a1", "a2"}} // m1 was meta-stripped
	pre, err := PreStripAnchors(truths, r)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pre, []string{"a1", "a2", "m1"}) {
		t.Fatalf("pre-strip anchors: %v", pre)
	}
	bad := r
	bad.AnchorSessionIDs = []string{"a1", "not-in-theme"}
	if _, err := PreStripAnchors(truths, bad); err == nil {
		t.Fatal("anchors outside the named theme must fail (wrong anchor_theme)")
	}
	outOfRange := r
	outOfRange.AnchorTheme = "client-project/9"
	if _, err := PreStripAnchors(truths, outOfRange); err == nil {
		t.Fatal("out-of-range theme index must fail")
	}
	noAnchors := Rubric{ID: "C-E1"}
	if pre, err := PreStripAnchors(truths, noAnchors); err != nil || pre != nil {
		t.Fatalf("no-anchor rubric: %v %v", pre, err)
	}
}
