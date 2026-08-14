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
	// the population is now the global union, so anchors from any repo survive
	if got := EffectiveAnchors(Rubric{ID: "C-78", AnchorSessionIDs: []string{"sA1", "sB1"}},
		[]string{"sA1", "sA2", "sB1"}, nil); !reflect.DeepEqual(got, []string{"sA1", "sB1"}) {
		t.Fatalf("multi-repo anchors: %v", got)
	}
}

// corroItem builds a finding item spanning repos, with n sessions of which the
// named ones are anchor hits.
func corroItem(repos []string, n int, hit ...string) ScoredItem {
	ids := append([]string(nil), hit...)
	for i := len(ids); i < n; i++ {
		ids = append(ids, fmt.Sprintf("extra%d", i))
	}
	return ScoredItem{ID: "finding/1", Repos: repos, Surface: surfaceFinding, SessionIDs: sortedSet(ids)}
}

// A correct cross-repo merge must corroborate on the combined anchor set, with
// no bucket-membership penalty: the v1 non_expected_bucket rejection is gone.
func TestCorroborateAcceptsCrossRepoMerge(t *testing.T) {
	combined := []string{"sA1", "sA2", "sB1", "sB2"} // anchors from both repos
	merged := corroItem([]string{"alpha", "beta"}, 4, "sA1", "sA2", "sB1", "sB2")
	if got := Corroborate(merged, combined, nil); got != CorroborationOK {
		t.Fatalf("a correct alpha+beta merge must corroborate, got %s", got)
	}
	// the same item scored against one repo's anchors alone still corroborates
	// on recall (2 of 2) — nothing about the item's OTHER repo penalizes it
	if got := Corroborate(merged, []string{"sA1", "sA2"}, nil); got != CorroborationOK {
		t.Fatalf("single-repo anchors on a merged finding: %s", got)
	}
	// the cap is derived from the combined anchors, so a two-repo item is not
	// squeezed by a one-repo denominator: 9 sessions ≤ 3×4+2, > 3×2+2
	wide := corroItem([]string{"alpha", "beta"}, 9, "sA1", "sA2", "sB1", "sB2")
	if got := Corroborate(wide, combined, combined); got != CorroborationOK {
		t.Fatalf("combined-anchor cap must admit a genuine merge: %s", got)
	}
	if got := Corroborate(wide, combined, []string{"sA1", "sA2"}); got != CorroborationSizeCap {
		t.Fatalf("a one-repo cap denominator still bites when that is all there is: %s", got)
	}
}

// A dropped entry is carded, never counted: its corroboration outcome is its
// own trigger type so the recall miss is visible and unscorable as a pass.
func TestCorroborateNeverCountsDropped(t *testing.T) {
	anchors := []string{"sA1", "sA2"}
	d := corroItem([]string{"alpha"}, 2, "sA1", "sA2")
	d.ID, d.Dropped, d.DropReason = "dropped/0", true, "one session only"
	if got := Corroborate(d, anchors, nil); got != CorroborationDropped {
		t.Fatalf("a dropped entry must never corroborate, got %s", got)
	}
	// even with no anchors at all, dropped wins: it is a recall miss, not an
	// anchorless pass
	if got := Corroborate(d, nil, nil); got != CorroborationDropped {
		t.Fatalf("dropped precedence over no-anchor: %s", got)
	}
}

func TestCorroborateTwoSided(t *testing.T) {
	anchors := []string{"a1", "a2", "a3", "a4"}
	if got := Corroborate(corroItem([]string{"alpha"}, 4, "a1", "a2"), anchors, nil); got != CorroborationOK {
		t.Fatalf("exact 50%% must corroborate: %s", got)
	}
	if got := Corroborate(corroItem([]string{"alpha"}, 8, "a1"), anchors, nil); got != CorroborationMismatch {
		t.Fatalf("25%% recall at 12.5%% precision must mismatch: %s", got)
	}
	// 14 sessions ≤ 3×4+2 passes; 15 breaches the cap (mega-finding)
	if got := Corroborate(corroItem([]string{"alpha"}, 14, "a1", "a2", "a3", "a4"), anchors, nil); got != CorroborationOK {
		t.Fatalf("cap boundary must pass: %s", got)
	}
	if got := Corroborate(corroItem([]string{"alpha"}, 15, "a1", "a2", "a3", "a4"), anchors, nil); got != CorroborationSizeCap {
		t.Fatalf("mega-finding must fail the cap: %s", got)
	}
	if got := Corroborate(corroItem([]string{"alpha"}, 3), nil, nil); got != CorroborationNoAnchors {
		t.Fatalf("no anchors: %s", got)
	}
}

// Findings' session sets are recovered from their cited evidence, so they take
// the grounding alternative to the recall bar (v1's rec-surface amendment):
// drawn FROM the anchors counts, without having to cover them.
func TestCorroborateFindingGrounding(t *testing.T) {
	anchors := []string{"a1", "a2", "a3", "a4"}
	// 1 hit / 2 sessions = exact 50% precision, under the recall bar
	if got := Corroborate(corroItem([]string{"alpha"}, 2, "a1"), anchors, nil); got != CorroborationGrounded {
		t.Fatalf("exact 50%% precision must ground: %s", got)
	}
	// below the precision bar: 1 hit / 3 sessions
	if got := Corroborate(corroItem([]string{"alpha"}, 3, "a1"), anchors, nil); got != CorroborationMismatch {
		t.Fatalf("33%% precision must mismatch: %s", got)
	}
	// zero-overlap guard: an empty session set must never corroborate
	if got := Corroborate(corroItem([]string{"alpha"}, 0), anchors, nil); got != CorroborationMismatch {
		t.Fatalf("no sessions must mismatch: %s", got)
	}
	// the recall bar still suffices on its own: 2/4 hits, precision 2/9 —
	// and reports plain "corroborated", not "grounded"
	if got := Corroborate(corroItem([]string{"alpha"}, 9, "a1", "a2"), anchors, nil); got != CorroborationOK {
		t.Fatalf("recall bar must corroborate regardless of precision: %s", got)
	}
	// grounding stays behind the size cap
	wide := []string{"a1", "a2", "a3", "a4", "a5", "a6", "a7", "a8", "a9", "a10", "a11", "a12"}
	if got := Corroborate(corroItem([]string{"alpha"}, 6, "a1", "a2", "a3"), wide, []string{"a1"}); got != CorroborationSizeCap {
		t.Fatalf("grounded finding breaching the cap must fail it: %s", got)
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
	// cap denominator = source finding ∩ population (b2 outside population)
	if !reflect.DeepEqual(capAnchors, []string{"a1", "a2", "b1"}) {
		t.Fatalf("cap anchors: %v", capAnchors)
	}
	// pre-strip (as_consumed control) replaces BOTH denominators wholesale
	anchors, capAnchors = AnchorSets(r, pop, []string{"a1", "b1", "m1"})
	if !reflect.DeepEqual(anchors, []string{"a1", "b1"}) || !reflect.DeepEqual(capAnchors, []string{"a1", "b1"}) {
		t.Fatalf("pre-strip: anchors %v cap %v", anchors, capAnchors)
	}
	// no source set recorded → cap falls back to the kept anchors
	anchors, capAnchors = AnchorSets(Rubric{ID: "C-78", AnchorSessionIDs: []string{"a1"}}, pop, nil)
	if !reflect.DeepEqual(anchors, []string{"a1"}) || !reflect.DeepEqual(capAnchors, []string{"a1"}) {
		t.Fatalf("fallback: anchors %v cap %v", anchors, capAnchors)
	}
}

func TestCorroborateCapCountsAgainstSourceSet(t *testing.T) {
	kept := []string{"a1", "a2", "a3", "a4"}
	capAnchors := []string{"a1", "a2", "a3", "a4", "b1", "b2"} // effective pre-QA source set
	// 15 sessions breaches the kept-anchor cap (3×4+2=14) but not the source
	// cap (3×6+2=20): QA removals must never tighten the cap.
	if got := Corroborate(corroItem([]string{"alpha"}, 15, "a1", "a2", "a3", "a4"), kept, capAnchors); got != CorroborationOK {
		t.Fatalf("cap must count against the source set, not kept anchors: %s", got)
	}
	// the source cap still bites: 21 > 3×6+2
	if got := Corroborate(corroItem([]string{"alpha"}, 21, "a1", "a2", "a3", "a4"), kept, capAnchors); got != CorroborationSizeCap {
		t.Fatalf("source cap must still fail mega-findings: %s", got)
	}
	// hits still count against KEPT anchors: 2/4 = exact 50% corroborates
	// even though 2/6 of the source set would not
	if got := Corroborate(corroItem([]string{"alpha"}, 4, "a1", "a2"), kept, capAnchors); got != CorroborationOK {
		t.Fatalf("hits must count against kept anchors: %s", got)
	}
}
