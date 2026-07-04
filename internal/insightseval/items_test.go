package insightseval

import (
	"encoding/json"
	"reflect"
	"testing"

	"tmux-ctrl/internal/synthesis"
)

func scoredFixture() (VerifiedOutput, synthesis.EvidenceBundle) {
	bundle := synthesis.EvidenceBundle{
		Repo: "client-project",
		Friction: []synthesis.FrictionItem{
			{ID: "F1", OneLine: "took a detour", SessionID: "sA"},
			{ID: "F2", OneLine: "diffed stale base", SessionID: "sB"},
		},
		Prefs:   []synthesis.PrefItem{{ID: "P1", Rule: "no comments", SessionID: "sC"}},
		Success: []synthesis.SuccessItem{{ID: "S1", Summary: "clean landing", SessionID: "sD"}},
		Signals: []synthesis.OppSignal{{ID: "G1", Kind: "high_read", Magnitude: 3, MemberSessions: []string{"sA", "sD", "sE"}}},
	}
	vo := VerifiedOutput{
		Synthesis: synthesis.RepoSynthesis{
			Repo: "client-project",
			Themes: []synthesis.Theme{
				{Title: "Detours", Summary: "detours happen", Kind: "friction",
					SessionIDs: []string{"sA", "sB", "sA"}, Quotes: []string{"q1", "q2", "q3"}},
			},
			Recommendations: []synthesis.Recommendation{
				{Type: "claude_md_rule", Statement: "verify first", Quotes: []string{"rq1"}},
			},
		},
		Raw: synthesis.RawSynthesis{
			Themes:          []synthesis.RawTheme{{Title: "Detours"}},
			Recommendations: []synthesis.RawRec{{Statement: "verify first", EvidenceIDs: []string{"F1", "P1", "G1", "F1", "F9"}}},
		},
	}
	return vo, bundle
}

func TestBuildScoredItemsThemesAndRecs(t *testing.T) {
	vo, bundle := scoredFixture()
	items := BuildScoredItems("client-project", vo, bundle)
	if len(items) != 2 {
		t.Fatalf("items = %d", len(items))
	}
	th := items[0]
	if th.ID != "client-project/theme/0" || th.Surface != "theme" || th.Text != "Detours. detours happen" {
		t.Fatalf("theme item: %+v", th)
	}
	if !reflect.DeepEqual(th.SessionIDs, []string{"sA", "sB"}) { // deduped, sorted
		t.Fatalf("theme sessions: %v", th.SessionIDs)
	}
	if len(th.Quotes) != 2 { // capped at 2 like synthesis.Cards
		t.Fatalf("theme quotes: %v", th.Quotes)
	}
	rec := items[1]
	if rec.ID != "client-project/rec/0" || rec.Surface != "recommendation" || rec.Text != "verify first" {
		t.Fatalf("rec item: %+v", rec)
	}
	// F1→sA, P1→sC, G1→{sA,sD,sE}; F9 unknown dropped; dedup+sort
	if !reflect.DeepEqual(rec.SessionIDs, []string{"sA", "sC", "sD", "sE"}) {
		t.Fatalf("rec sessions: %v", rec.SessionIDs)
	}
}

func TestBuildMatchPayloadFiltersSurfaceAndIsDeterministic(t *testing.T) {
	vo, bundle := scoredFixture()
	items := BuildScoredItems("client-project", vo, bundle)
	themeOnly := Rubric{ID: "X", Part: "regression", Surface: "theme", Repos: []string{"client-project"},
		Statement: "s", RequiredNuances: []string{"n1"}}
	p := BuildMatchPayload(themeOnly, items)
	if len(p.Items) != 1 || p.Items[0].Surface != "theme" {
		t.Fatalf("surface filter: %+v", p.Items)
	}
	if p.Rubric.ForbiddenGeneralizations == nil || p.Rubric.RequiredNuances == nil {
		t.Fatal("nil slices must be normalized for stable payload hashes")
	}
	either := Rubric{ID: "Y", Part: "regression", Surface: "either", Repos: []string{"client-project"}, Statement: "s"}
	if p2 := BuildMatchPayload(either, items); len(p2.Items) != 2 {
		t.Fatalf("either surface: %+v", p2.Items)
	}
	negative := Rubric{ID: "N", Part: "negative", Statement: "s"} // no surface → both
	if p3 := BuildMatchPayload(negative, items); len(p3.Items) != 2 {
		t.Fatalf("negative surface: %+v", p3.Items)
	}
	j1, _ := json.Marshal(BuildMatchPayload(themeOnly, items))
	j2, _ := json.Marshal(BuildMatchPayload(themeOnly, items))
	if string(j1) != string(j2) {
		t.Fatal("payload marshal must be byte-stable")
	}
}

func TestSmallSetHelpers(t *testing.T) {
	if got := sortedSet([]string{"b", "a", "b"}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("sortedSet: %v", got)
	}
	if bucketOf("client-project/theme/3") != "client-project" || bucketOf("probe") != "probe" {
		t.Fatal("bucketOf")
	}
	if allTrue([]bool{true, false}) || !allTrue(nil) {
		t.Fatal("allTrue")
	}
}
