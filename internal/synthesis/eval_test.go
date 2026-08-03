package synthesis

import (
	"context"
	"encoding/json"
	"os"
	"testing"
)

func TestOpportunityRecallMiss(t *testing.T) {
	b := EvidenceBundle{Signals: []OppSignal{{ID: "G1", Kind: "high_read", Magnitude: 12}}}
	// A synthesis that references no opportunity themes at all:
	s := RepoSynthesis{Themes: []Theme{{Kind: "friction", Title: "x"}}}
	res := EvaluateRun(s, s, ValidationReport{}, b)
	if len(res.OpportunityRecallMisses) != 1 {
		t.Errorf("misses = %v, want 1 (G1 unreferenced)", res.OpportunityRecallMisses)
	}
}

func TestOpportunityRecallReferencedIsNotAMiss(t *testing.T) {
	b := EvidenceBundle{Signals: []OppSignal{{ID: "G1", Kind: "high_read", Magnitude: 12}}}
	s := RepoSynthesis{Themes: []Theme{{Kind: "opportunity", Title: "Read-heavy", SignalRefs: []string{"G1"}}}}
	res := EvaluateRun(s, s, ValidationReport{}, b)
	if len(res.OpportunityRecallMisses) != 0 {
		t.Errorf("misses = %v, want 0 (G1 referenced by an opportunity theme)", res.OpportunityRecallMisses)
	}
}

func TestOpportunityRecallSurvivesJSONRoundTrip(t *testing.T) {
	// The verdict probe reads verified outputs back from the JSON cache; a
	// serialization tag that strips SignalRefs makes every G signal an
	// eternal miss regardless of what L2 produced.
	b := EvidenceBundle{Signals: []OppSignal{{ID: "G1", Kind: "high_read", Magnitude: 12}}}
	s := RepoSynthesis{Themes: []Theme{{Kind: "opportunity", Title: "Read-heavy", SignalRefs: []string{"G1"}}}}
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var back RepoSynthesis
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	res := EvaluateRun(back, back, ValidationReport{}, b)
	if len(res.OpportunityRecallMisses) != 0 {
		t.Errorf("misses = %v, want 0: SignalRefs must survive the cache round-trip", res.OpportunityRecallMisses)
	}
}

func TestOpportunityRecallBelowFloorNotAMiss(t *testing.T) {
	b := EvidenceBundle{Signals: []OppSignal{{ID: "G1", Kind: "high_read", Magnitude: signalFloor - 1}}}
	s := RepoSynthesis{}
	res := EvaluateRun(s, s, ValidationReport{}, b)
	if len(res.OpportunityRecallMisses) != 0 {
		t.Errorf("misses = %v, want 0 (magnitude below signalFloor is not a directional under-detection)", res.OpportunityRecallMisses)
	}
}

func TestPrefRecallMiss(t *testing.T) {
	b := EvidenceBundle{Prefs: []PrefItem{{ID: "P1"}, {ID: "P2"}, {ID: "P3"}}}
	s := RepoSynthesis{Recommendations: []Recommendation{{Type: "workflow_tip", Statement: "x"}}}
	res := EvaluateRun(s, s, ValidationReport{}, b)
	if len(res.PrefRecallMisses) != 1 {
		t.Errorf("PrefRecallMisses = %v, want 1 (prefs present, no claude_md_rule)", res.PrefRecallMisses)
	}
}

func TestPrefRecallSatisfiedWhenRuleSurfaced(t *testing.T) {
	b := EvidenceBundle{Prefs: []PrefItem{{ID: "P1"}, {ID: "P2"}, {ID: "P3"}}}
	s := RepoSynthesis{Recommendations: []Recommendation{{Type: "claude_md_rule", Statement: "x"}}}
	res := EvaluateRun(s, s, ValidationReport{}, b)
	if len(res.PrefRecallMisses) != 0 {
		t.Errorf("PrefRecallMisses = %v, want 0 (claude_md_rule present)", res.PrefRecallMisses)
	}
}

func TestDominantTypeSoftFloorBreach(t *testing.T) {
	b := EvidenceBundle{Friction: []FrictionItem{{ID: "F1"}}}
	s := RepoSynthesis{Themes: []Theme{{Kind: "opportunity", Title: "x"}}}
	res := EvaluateRun(s, s, ValidationReport{}, b)
	if res.DominantTypePresent {
		t.Error("DominantTypePresent = true, want false: friction items exist but no friction theme formed")
	}
}

func TestDominantTypePresentWithFrictionTheme(t *testing.T) {
	b := EvidenceBundle{Friction: []FrictionItem{{ID: "F1"}}}
	s := RepoSynthesis{Themes: []Theme{{Kind: "friction", Title: "x"}}}
	res := EvaluateRun(s, s, ValidationReport{}, b)
	if !res.DominantTypePresent {
		t.Error("DominantTypePresent = false, want true: a friction theme was formed")
	}
}

func TestDominantTypeVacuousWithNoFriction(t *testing.T) {
	b := EvidenceBundle{}
	s := RepoSynthesis{}
	res := EvaluateRun(s, s, ValidationReport{}, b)
	if !res.DominantTypePresent {
		t.Error("DominantTypePresent = false, want true: no friction items exist, so the soft floor doesn't apply")
	}
}

func TestMembershipChurnStableAcrossRuns(t *testing.T) {
	a := RepoSynthesis{Themes: []Theme{{Title: "A", SessionIDs: []string{"s1", "s2"}}}}
	b := RepoSynthesis{Themes: []Theme{{Title: "A-renamed", SessionIDs: []string{"s1", "s2"}}}}
	res := EvaluateRun(a, b, ValidationReport{}, EvidenceBundle{})
	if res.MembershipChurn != 0 {
		t.Errorf("MembershipChurn = %v, want 0: identical session-id sets are stable regardless of title", res.MembershipChurn)
	}
}

func TestMembershipChurnFullTurnover(t *testing.T) {
	a := RepoSynthesis{Themes: []Theme{{Title: "A", SessionIDs: []string{"s1", "s2"}}}}
	b := RepoSynthesis{Themes: []Theme{{Title: "A", SessionIDs: []string{"s3", "s4"}}}}
	res := EvaluateRun(a, b, ValidationReport{}, EvidenceBundle{})
	if res.MembershipChurn != 1 {
		t.Errorf("MembershipChurn = %v, want 1: disjoint session-id sets", res.MembershipChurn)
	}
}

// Regression: a session contributing multiple evidence items to the same theme repeats
// its id in SessionIDs; jaccard must treat both sides as sets, not multisets, or churn
// gets understated (duplicates in b inflate the intersection count).
func TestMembershipChurnIgnoresDuplicateSessionIDs(t *testing.T) {
	a := RepoSynthesis{Themes: []Theme{{Title: "A", SessionIDs: []string{"s1", "s2"}}}}
	b := RepoSynthesis{Themes: []Theme{{Title: "A", SessionIDs: []string{"s1", "s1", "s3"}}}}
	res := EvaluateRun(a, b, ValidationReport{}, EvidenceBundle{})
	want := 1 - 1.0/3.0 // intersection {s1}=1, union {s1,s2,s3}=3
	if diff := res.MembershipChurn - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("MembershipChurn = %v, want %v (duplicate session id in b must not inflate overlap)", res.MembershipChurn, want)
	}
}

func TestSchemaOKReflectsHardErrors(t *testing.T) {
	s := RepoSynthesis{}
	clean := EvaluateRun(s, s, ValidationReport{}, EvidenceBundle{})
	if !clean.SchemaOK {
		t.Error("SchemaOK = false, want true with no hard errors")
	}
	dirty := EvaluateRun(s, s, ValidationReport{HardErrors: []string{"bad id"}}, EvidenceBundle{})
	if dirty.SchemaOK {
		t.Error("SchemaOK = true, want false with a hard error present")
	}
}

func TestRawFabricationRatePassesThroughReport(t *testing.T) {
	s := RepoSynthesis{}
	res := EvaluateRun(s, s, ValidationReport{RawQuoteDropRate: 0.42}, EvidenceBundle{})
	if res.RawFabricationRate != 0.42 {
		t.Errorf("RawFabricationRate = %v, want 0.42 (raw pre-guard rate, not post-guard)", res.RawFabricationRate)
	}
}

func TestPrivacyLeaksSurfacedFromRenderedRun(t *testing.T) {
	s := RepoSynthesis{Themes: []Theme{{Kind: "friction", Title: "leak 4b1f6c58-3c9e-4a1d-9c2e-2b6f8e0a1234", Rank: 1}}}
	res := EvaluateRun(s, s, ValidationReport{}, EvidenceBundle{})
	if len(res.PrivacyLeaks) == 0 {
		t.Error("expected PrivacyLeaks to surface a UUID embedded in a rendered theme title")
	}
}

func TestCardsCarryNoSessionIDsOrTranscripts(t *testing.T) {
	s := sampleSynthesis()
	bundle := EvidenceBundle{AnalyzedCount: s.Window.AnalyzedCount}
	cards := Cards(s, bundle)
	if len(cards) == 0 {
		t.Fatal("expected at least one card from sampleSynthesis")
	}
	blob, err := json.Marshal(cards)
	if err != nil {
		t.Fatal(err)
	}
	if leaks := scanReport(string(blob)); len(leaks) != 0 {
		t.Errorf("marshaled cards leaked: %v", leaks)
	}
}

func TestCardsDetectPlantedLeak(t *testing.T) {
	s := sampleSynthesis()
	s.Themes[0].Quotes = []string{"see /Users/dev/secret for the full trace"}
	bundle := EvidenceBundle{AnalyzedCount: s.Window.AnalyzedCount}
	cards := Cards(s, bundle)
	blob, err := json.Marshal(cards)
	if err != nil {
		t.Fatal(err)
	}
	if leaks := scanReport(string(blob)); len(leaks) == 0 {
		t.Error("expected scanReport to catch a /Users/ path planted in a card quote")
	}
}

// TestCardsMatchByThemeIndexNotTitle is a regression for a bug where a
// recommendation was attached to a card by comparing theme titles: two themes
// sharing a title would cross-attach a recommendation meant for the other
// one. Matching must use the theme's own slice index.
func TestCardsMatchByThemeIndexNotTitle(t *testing.T) {
	s := RepoSynthesis{
		Repo: "client-project",
		Themes: []Theme{
			{Title: "Duplicate name", Kind: "friction", SessionCount: 3},
			{Title: "Duplicate name", Kind: "friction", SessionCount: 5},
		},
		Recommendations: []Recommendation{
			{Type: "workflow_tip", Statement: "applies only to theme index 1", ThemeRefs: []int{1}},
		},
	}
	cards := Cards(s, EvidenceBundle{})
	if len(cards) != 2 {
		t.Fatalf("cards = %v, want 2", cards)
	}
	if cards[0].ProposedRec != "" {
		t.Errorf("card for theme index 0 got ProposedRec %q, want empty (rec targets index 1 only)", cards[0].ProposedRec)
	}
	if cards[1].ProposedRec != "applies only to theme index 1" {
		t.Errorf("card for theme index 1 got ProposedRec %q, want the targeted recommendation", cards[1].ProposedRec)
	}
}

func TestCardsSkipZeroSessionThemes(t *testing.T) {
	s := RepoSynthesis{Repo: "client-project", Themes: []Theme{{Title: "empty", SessionCount: 0}}}
	if cards := Cards(s, EvidenceBundle{}); len(cards) != 0 {
		t.Errorf("cards = %v, want none for a zero-session-count theme", cards)
	}
}

func TestGateRealclient-project(t *testing.T) {
	if os.Getenv("SYNTHESIS_REAL") == "" {
		t.Skip("set SYNTHESIS_REAL=1 to run the real gate (spends subscription)")
	}
	analyses, err := LoadAnalyses()
	if err != nil {
		t.Fatal(err)
	}
	groups := GroupByRepo(analyses, DefaultMinSessions, nil)
	group := groups["client-project"]
	if len(group) == 0 {
		t.Skip("no client-project analyses present")
	}
	syn := NewClaudeSynthesizer()
	adopt := NewAdoptChecker("/Users/dev/Developer/client-project")
	a, ra, err := Synthesize(context.Background(), "client-project", group, syn, adopt)
	if err != nil {
		t.Fatal(err)
	}
	res := EvaluateRun(a, a, ra, BuildBundle("client-project", group))
	t.Logf("fabrication=%.3f hardErrors=%d oppMisses=%v leaks=%v",
		res.RawFabricationRate, len(res.HardErrors), res.OpportunityRecallMisses, res.PrivacyLeaks)
	if res.RawFabricationRate > 0.15 {
		t.Errorf("raw fabrication rate too high: %.3f", res.RawFabricationRate)
	}
	if len(res.PrivacyLeaks) != 0 {
		t.Errorf("privacy leaks in real render: %v", res.PrivacyLeaks)
	}
}
