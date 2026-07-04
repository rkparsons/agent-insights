package insightseval

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

func TestCorroborateTwoSided(t *testing.T) {
	anchors := []string{"a1", "a2", "a3", "a4"}
	item := func(bucket string, n int, hit ...string) ScoredItem {
		ids := append([]string(nil), hit...)
		for i := len(ids); i < n; i++ {
			ids = append(ids, fmt.Sprintf("extra%d", i))
		}
		return ScoredItem{ID: bucket + "/theme/0", Bucket: bucket, SessionIDs: sortedSet(ids)}
	}
	if got := Corroborate(item("client-project", 4, "a1", "a2"), "client-project", anchors); got != CorroborationOK {
		t.Fatalf("exact 50%% must corroborate: %s", got)
	}
	if got := Corroborate(item("client-project", 4, "a1"), "client-project", anchors); got != CorroborationMismatch {
		t.Fatalf("25%% must mismatch: %s", got)
	}
	// 14 sessions ≤ 3×4+2 passes; 15 breaches the cap (mega-theme)
	if got := Corroborate(item("client-project", 14, "a1", "a2", "a3", "a4"), "client-project", anchors); got != CorroborationOK {
		t.Fatalf("cap boundary must pass: %s", got)
	}
	if got := Corroborate(item("client-project", 15, "a1", "a2", "a3", "a4"), "client-project", anchors); got != CorroborationSizeCap {
		t.Fatalf("mega-theme must fail the cap: %s", got)
	}
	if got := Corroborate(item("tmux-ctrl", 4, "a1", "a2"), "client-project", anchors); got != CorroborationCrossBucket {
		t.Fatalf("non-expected bucket skips corroboration: %s", got)
	}
	if got := Corroborate(item("client-project", 3), "client-project", nil); got != CorroborationNoAnchors {
		t.Fatalf("no anchors: %s", got)
	}
}
