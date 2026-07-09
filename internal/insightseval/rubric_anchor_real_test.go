package insightseval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"tmux-ctrl/internal/synthesis"
)

// TestPartARubricsAnchorsResolveInFrozenData verifies, against the private
// data repo: 24 regression rubrics exist; every anchor id appears in some
// frozen ground-truth theme's session_ids for the expected bucket AND in that
// bucket's scoring population (i.e. meta ids were stripped at authoring).
func TestPartARubricsAnchorsResolveInFrozenData(t *testing.T) {
	home, _ := os.UserHomeDir()
	dataDir := filepath.Join(home, "Developer", "insights-eval-data")
	if _, err := os.Stat(filepath.Join(dataDir, "manifest.json")); err != nil {
		t.Skip("insights-eval-data not present")
	}
	rubrics, err := LoadRubrics()
	if err != nil {
		t.Fatal(err)
	}
	b, ok, err := loadBenchmark(dataDir)
	if err != nil || !ok {
		t.Fatal(err)
	}
	themeIDs := map[string]map[string]bool{} // bucket → id set across all themes
	for bucket := range b.Buckets {
		raw, err := os.ReadFile(filepath.Join(dataDir, "ground-truth", bucket, "2026-07-02.json"))
		if err != nil {
			t.Fatal(err)
		}
		var gt synthesis.RepoSynthesis
		if err := json.Unmarshal(raw, &gt); err != nil {
			t.Fatal(err)
		}
		set := map[string]bool{}
		for _, th := range gt.Themes {
			for _, id := range th.SessionIDs {
				set[id] = true
			}
		}
		themeIDs[bucket] = set
	}
	scoring := map[string]map[string]bool{}
	for bucket, bp := range b.Buckets {
		set := map[string]bool{}
		for _, id := range bp.Scoring {
			set[id] = true
		}
		scoring[bucket] = set
	}
	regressions := 0
	for _, r := range rubrics {
		if r.Part != "regression" {
			continue
		}
		regressions++
		expected := r.Repos[0]
		if _, known := b.Buckets[expected]; !known {
			t.Errorf("%s: expected bucket %q not in benchmark", r.ID, expected)
			continue
		}
		for _, id := range r.AnchorSessionIDs {
			if !themeIDs[expected][id] {
				t.Errorf("%s: anchor %s not in any frozen %s theme", r.ID, id, expected)
			}
			if !scoring[expected][id] {
				t.Errorf("%s: anchor %s not in %s scoring population (meta id not stripped?)", r.ID, id, expected)
			}
		}
		for _, id := range r.SourceThemeSessionIDs {
			if !themeIDs[expected][id] {
				t.Errorf("%s: source-theme id %s not in any frozen %s theme", r.ID, id, expected)
			}
			if !scoring[expected][id] {
				t.Errorf("%s: source-theme id %s not in %s scoring population (meta id not stripped?)", r.ID, id, expected)
			}
		}
	}
	if regressions != 24 {
		t.Fatalf("regression rubrics = %d, want 24", regressions)
	}
}

// TestAnchorThemeResolvesPreStripAnchors verifies, against the private data
// repo, that every anchored rubric's anchor_theme names a frozen ground-truth
// theme whose id set contains the rubric's (meta-stripped) anchors — i.e. the
// as_consumed control's pre-strip anchors are resolvable and consistent.
func TestAnchorThemeResolvesPreStripAnchors(t *testing.T) {
	home, _ := os.UserHomeDir()
	dataDir := filepath.Join(home, "Developer", "insights-eval-data")
	if _, err := os.Stat(filepath.Join(dataDir, "manifest.json")); err != nil {
		t.Skip("insights-eval-data not present")
	}
	rubrics, err := LoadRubrics()
	if err != nil {
		t.Fatal(err)
	}
	truths, err := loadGroundTruth(filepath.Join(dataDir, "ground-truth"))
	if err != nil {
		t.Fatal(err)
	}
	anchored := 0
	for _, r := range rubrics {
		if len(r.AnchorSessionIDs) == 0 {
			continue
		}
		anchored++
		pre, err := PreStripAnchors(truths, r)
		if err != nil {
			t.Errorf("%s: %v", r.ID, err)
			continue
		}
		if len(pre) < len(sortedSet(r.AnchorSessionIDs)) {
			t.Errorf("%s: pre-strip set (%d) smaller than stripped anchors (%d)", r.ID, len(pre), len(r.AnchorSessionIDs))
		}
	}
	// 21 at freeze; anchor-QA pass 1 (2026-07-07) degraded C-A and C-D1 to
	// no-anchor targets (< 50% of raw source theme kept); the rec-surface
	// corroboration amendment (2026-07-09) dropped C-E2's anchors as a
	// freeze-time category error (P-corroborated practice target inherited
	// friction-theme anchors); anchor-QA pass 2 (2026-07-09) degraded C-D2
	// (kept 1 of raw 6).
	if anchored != 17 {
		t.Fatalf("anchored rubrics = %d, want 17", anchored)
	}
}
