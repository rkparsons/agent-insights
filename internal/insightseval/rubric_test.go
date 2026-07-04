package insightseval

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
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
