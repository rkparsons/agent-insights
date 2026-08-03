package insightseval

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"tmux-ctrl/internal/synthesis"
)

// testdataDir is the loader's dataDir for the synthetic testdata/rubrics
// fixture (T-01.yaml only) — no real session ids, no embedded copies.
const testdataDir = "testdata"

// t01Sha256 locks parseRubric's hash algorithm to sha256hex over the raw
// file bytes: precomputed via `shasum -a 256 testdata/rubrics/T-01.yaml`.
// Every human adjudication in the data repo is keyed by this hash, so a
// silent algorithm change here would silently orphan every adjudication.
const t01Sha256 = "80a4e919a0cc707f8ab814e165350867ec4100024a8200b0161598e857c7c4f6"

func TestLoadRubricsFromTestdata(t *testing.T) {
	rubrics, err := LoadRubrics(testdataDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rubrics) != 1 {
		t.Fatalf("rubrics = %d, want 1 (testdata/rubrics/T-01.yaml only)", len(rubrics))
	}
	r := rubrics[0]
	if r.ID != "T-01" || r.Part != "regression" || r.Repos[0] != "alpha" {
		t.Fatalf("T-01: %+v", r)
	}
	if r.Hash != t01Sha256 {
		t.Fatalf("T-01 hash = %q, want %q (sha256 over raw file bytes — adjudication keys depend on this)", r.Hash, t01Sha256)
	}
	h, err := RubricSetHash(testdataDir)
	if err != nil || len(h) != 64 {
		t.Fatalf("hash: %q %v", h, err)
	}
}

// TestLoadRubricsMissingDirFailsClosed: a dataDir with no rubrics/ subdir
// (wrong --data, or an unchecked-out data repo) must error naming the
// expected path, not silently return zero rubrics.
func TestLoadRubricsMissingDirFailsClosed(t *testing.T) {
	empty := t.TempDir()
	want := filepath.Join(empty, "rubrics")
	if _, err := LoadRubrics(empty); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("LoadRubrics(%s): err = %v, want error naming %s", empty, err, want)
	}
	if _, err := RubricSetHash(empty); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("RubricSetHash(%s): err = %v, want error naming %s", empty, err, want)
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
	writeGapHeavyRubricSet(t, data)
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
	ok := anchored + "anchor_theme: client-project/3\nsource_theme_session_ids: [abc]\n"
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

func TestRubricSourceThemeValidation(t *testing.T) {
	base := "id: C-77\npart: regression\ntier: HIGH\nsurface: theme\nrepos: [client-project]\nstatement: s\nanchor_theme: client-project/3\n"
	missing := base + "anchor_session_ids: [abc]\n"
	if _, err := parseRubric("C-77.yaml", []byte(missing)); err == nil || !strings.Contains(err.Error(), "source_theme_session_ids") {
		t.Fatalf("anchored rubric without source_theme_session_ids must fail: %v", err)
	}
	notSubset := base + "anchor_session_ids: [abc, def]\nsource_theme_session_ids: [abc]\n"
	if _, err := parseRubric("C-77.yaml", []byte(notSubset)); err == nil || !strings.Contains(err.Error(), "def") {
		t.Fatalf("kept anchors must be a subset of the source theme: %v", err)
	}
	orphan := "id: C-77\npart: regression\ntier: HIGH\nsurface: theme\nrepos: [client-project]\nstatement: s\nsource_theme_session_ids: [abc]\n"
	if _, err := parseRubric("C-77.yaml", []byte(orphan)); err == nil {
		t.Fatal("source_theme_session_ids without anchors must fail")
	}
	negative := "id: N-77\npart: negative\nstatement: s\nsource_theme_session_ids: [abc]\n"
	if _, err := parseRubric("N-77.yaml", []byte(negative)); err == nil {
		t.Fatal("negative rubric with source_theme_session_ids must fail")
	}
	ok := base + "anchor_session_ids: [abc]\nsource_theme_session_ids: [abc, def]\n"
	r, err := parseRubric("C-77.yaml", []byte(ok))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.SourceThemeSessionIDs) != 2 {
		t.Fatalf("rubric: %+v", r)
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
	badSource := r
	badSource.SourceThemeSessionIDs = []string{"a1", "a2", "not-in-theme"}
	if _, err := PreStripAnchors(truths, badSource); err == nil {
		t.Fatal("source-theme ids outside the named theme must fail")
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
