package eval

import (
	"fmt"
	"reflect"
	"testing"
)

func TestEffectiveAnchorsIntersectsPopulation(t *testing.T) {
	r := Rubric{ID: "C-77", AnchorSessionIDs: []string{"a", "b", "gap1", "b"}}
	pop := []string{"a", "b", "gap1", "x", "y"}
	if got := EffectiveAnchors(r, pop, nil); !reflect.DeepEqual(got, []string{"a", "b", "gap1"}) {
		t.Fatalf("l2-scope anchors: %v", got)
	}
	// full scope stripped gap1 from the population → anchor drops out
	if got := EffectiveAnchors(r, []string{"a", "b", "x"}, nil); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("full-scope anchors: %v", got)
	}
	// pre-strip mode replaces the anchor set entirely (as_consumed control)
	if got := EffectiveAnchors(r, pop, []string{"a", "m"}); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("pre-strip anchors: %v", got)
	}
	if got := EffectiveAnchors(Rubric{ID: "M1"}, pop, nil); len(got) != 0 {
		t.Fatalf("no-anchor rubric: %v", got)
	}
}

func corroItem(bucket string, n int, hit ...string) ScoredItem {
	ids := append([]string(nil), hit...)
	for i := len(ids); i < n; i++ {
		ids = append(ids, fmt.Sprintf("extra%d", i))
	}
	return ScoredItem{ID: bucket + "/theme/0", Bucket: bucket, SessionIDs: sortedSet(ids)}
}

func TestCorroborateTwoSided(t *testing.T) {
	anchors := []string{"a1", "a2", "a3", "a4"}
	if got := Corroborate(corroItem("alpha", 4, "a1", "a2"), "alpha", anchors, nil); got != CorroborationOK {
		t.Fatalf("exact 50%% must corroborate: %s", got)
	}
	if got := Corroborate(corroItem("alpha", 4, "a1"), "alpha", anchors, nil); got != CorroborationMismatch {
		t.Fatalf("25%% must mismatch: %s", got)
	}
	// 14 sessions ≤ 3×4+2 passes; 15 breaches the cap (mega-theme)
	if got := Corroborate(corroItem("alpha", 14, "a1", "a2", "a3", "a4"), "alpha", anchors, nil); got != CorroborationOK {
		t.Fatalf("cap boundary must pass: %s", got)
	}
	if got := Corroborate(corroItem("alpha", 15, "a1", "a2", "a3", "a4"), "alpha", anchors, nil); got != CorroborationSizeCap {
		t.Fatalf("mega-theme must fail the cap: %s", got)
	}
	if got := Corroborate(corroItem("tmux-ctrl", 4, "a1", "a2"), "alpha", anchors, nil); got != CorroborationCrossBucket {
		t.Fatalf("non-expected bucket skips corroboration: %s", got)
	}
	if got := Corroborate(corroItem("alpha", 3), "alpha", nil, nil); got != CorroborationNoAnchors {
		t.Fatalf("no anchors: %s", got)
	}
}

func recCorroItem(bucket string, n int, hit ...string) ScoredItem {
	it := corroItem(bucket, n, hit...)
	it.ID = bucket + "/rec/0"
	it.Surface = "recommendation"
	return it
}

func TestCorroborateRecGrounding(t *testing.T) {
	anchors := []string{"a1", "a2", "a3", "a4"}
	// grounding path: 1 hit / 2 sessions = exact 50% precision corroborates a
	// rec that the recall bar (≥2.0 hits) would reject — reported as the
	// distinct "grounded" outcome so verdicts and the HIGH oversight card can
	// see which path counted
	if got := Corroborate(recCorroItem("alpha", 2, "a1"), "alpha", anchors, nil); got != CorroborationGrounded {
		t.Fatalf("rec at exact 50%% precision must ground: %s", got)
	}
	// below the precision bar: 1 hit / 3 sessions
	if got := Corroborate(recCorroItem("alpha", 3, "a1"), "alpha", anchors, nil); got != CorroborationMismatch {
		t.Fatalf("rec at 33%% precision must mismatch: %s", got)
	}
	// zero-overlap guard: an empty session set must never corroborate
	if got := Corroborate(recCorroItem("alpha", 0), "alpha", anchors, nil); got != CorroborationMismatch {
		t.Fatalf("rec with no sessions must mismatch: %s", got)
	}
	// the recall bar still suffices on its own: 2/4 hits, precision 2/9 —
	// and reports plain "corroborated", not "grounded"
	if got := Corroborate(recCorroItem("alpha", 9, "a1", "a2"), "alpha", anchors, nil); got != CorroborationOK {
		t.Fatalf("rec clearing the recall bar must corroborate regardless of precision: %s", got)
	}
	// grounding path stays behind the size cap
	wide := []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8", "a9", "a10", "a11", "a12"}
	if got := Corroborate(recCorroItem("alpha", 6, "a1", "a2", "a3"), "alpha", wide, []string{"a1"}); got != CorroborationSizeCap {
		t.Fatalf("grounded rec breaching the cap must fail it: %s", got)
	}
	// surface isolation: identical arithmetic on a theme keeps the recall bar
	if got := Corroborate(corroItem("alpha", 2, "a1"), "alpha", anchors, nil); got != CorroborationMismatch {
		t.Fatalf("theme at 25%% recall must mismatch regardless of precision: %s", got)
	}
	// no-anchor precedence unchanged for recs
	if got := Corroborate(recCorroItem("alpha", 2, "a1"), "alpha", nil, nil); got != CorroborationNoAnchors {
		t.Fatalf("no anchors: %s", got)
	}
}

func TestAnchorSetsSelectsCapDenominator(t *testing.T) {
	r := Rubric{ID: "C-77",
		AnchorSessionIDs:      []string{"a1", "a2"},
		SourceThemeSessionIDs: []string{"a1", "a2", "b1", "b2"}}
	pop := []string{"a1", "a2", "b1", "x"}
	anchors, capAnchors := AnchorSets(r, pop, nil)
	if !reflect.DeepEqual(anchors, []string{"a1", "a2"}) {
		t.Fatalf("kept anchors: %v", anchors)
	}
	// cap denominator = source theme ∩ population (b2 outside population)
	if !reflect.DeepEqual(capAnchors, []string{"a1", "a2", "b1"}) {
		t.Fatalf("cap anchors: %v", capAnchors)
	}
	// pre-strip (as_consumed control) replaces BOTH denominators wholesale
	anchors, capAnchors = AnchorSets(r, pop, []string{"a1", "b1", "m1"})
	if !reflect.DeepEqual(anchors, []string{"a1", "b1"}) || !reflect.DeepEqual(capAnchors, []string{"a1", "b1"}) {
		t.Fatalf("pre-strip: anchors %v cap %v", anchors, capAnchors)
	}
	// no source theme recorded → cap falls back to the kept anchors
	anchors, capAnchors = AnchorSets(Rubric{ID: "C-78", AnchorSessionIDs: []string{"a1"}}, pop, nil)
	if !reflect.DeepEqual(anchors, []string{"a1"}) || !reflect.DeepEqual(capAnchors, []string{"a1"}) {
		t.Fatalf("fallback: anchors %v cap %v", anchors, capAnchors)
	}
}

func TestCorroborateCapCountsAgainstSourceTheme(t *testing.T) {
	kept := []string{"a1", "a2", "a3", "a4"}
	capAnchors := []string{"a1", "a2", "a3", "a4", "b1", "b2"} // effective pre-QA source theme
	// 15 sessions breaches the kept-anchor cap (3×4+2=14) but not the source-theme
	// cap (3×6+2=20): QA removals must never tighten the cap.
	if got := Corroborate(corroItem("alpha", 15, "a1", "a2", "a3", "a4"), "alpha", kept, capAnchors); got != CorroborationOK {
		t.Fatalf("cap must count against the source theme, not kept anchors: %s", got)
	}
	// the source-theme cap still bites: 21 > 3×6+2
	if got := Corroborate(corroItem("alpha", 21, "a1", "a2", "a3", "a4"), "alpha", kept, capAnchors); got != CorroborationSizeCap {
		t.Fatalf("source-theme cap must still fail mega-themes: %s", got)
	}
	// hits still count against KEPT anchors: 2/4 = exact 50% corroborates
	// even though 2/6 of the source theme would not
	if got := Corroborate(corroItem("alpha", 4, "a1", "a2"), "alpha", kept, capAnchors); got != CorroborationOK {
		t.Fatalf("hits must count against kept anchors: %s", got)
	}
}
