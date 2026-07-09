package synthesis

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"tmux-ctrl/internal/insights"
)

type fakeSynth struct {
	raw RawSynthesis
	err error
}

func (f fakeSynth) Synthesize(ctx context.Context, b EvidenceBundle) (RawSynthesis, error) {
	return f.raw, f.err
}

func TestSynthesizeEndToEnd(t *testing.T) {
	group := []insights.AgentSessionAnalysis{
		frictionAnalysis("s1", "investigate the existing pattern first", "apps/api/a.ts"),
		frictionAnalysis("s2", "investigate the existing pattern first", "apps/api/b.ts"),
	}
	fake := fakeSynth{raw: RawSynthesis{
		Themes: []RawTheme{{Title: "Investigate first", Kind: "friction", EvidenceIDs: []string{"F1", "F2"},
			Summary: "asks what the codebase answers", CitedQuotes: []string{"investigate the existing pattern first"}}},
		Recommendations: []RawRec{{Type: "claude_md_rule", Statement: "Investigate existing patterns before writing new code",
			EvidenceIDs: []string{"F1"}, ThemeRefs: []int{0}, CitedQuotes: []string{"investigate the existing pattern first"}, Audience: "orchestrator"}},
	}}
	adopt := func(r Recommendation) string { return "no" }

	rs, report, err := Synthesize(context.Background(), "client-project", group, fake, adopt)
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if len(rs.Themes) != 1 || rs.Themes[0].IncidentCount != 2 {
		t.Fatalf("themes = %+v", rs.Themes)
	}
	if rs.Themes[0].Rank != 1 {
		t.Errorf("rank = %d, want 1", rs.Themes[0].Rank)
	}
	if len(rs.Recommendations) != 1 || rs.Recommendations[0].AlreadyAdopted != "no" {
		t.Errorf("recs = %+v", rs.Recommendations)
	}
	if report.RawQuoteDropRate != 0 {
		t.Errorf("drop rate = %v, want 0 (all quotes verbatim)", report.RawQuoteDropRate)
	}
	if len(report.HardErrors) != 0 {
		t.Errorf("hard errors = %v", report.HardErrors)
	}
}

func prefAnalysis(id, rule, quote string) insights.AgentSessionAnalysis {
	a := analysisWith("/Users/dev/Developer/client-project", "/Users/dev/Developer/client-project")
	a.Stats.SessionID = id
	a.Outcome = "fully_achieved"
	a.StandingPreferences = []insights.StandingPreference{{Rule: rule, EvidenceQuote: quote}}
	return a
}

func TestSynthesizeDropsNonPoolQuotes(t *testing.T) {
	group := []insights.AgentSessionAnalysis{frictionAnalysis("s1", "investigate the existing pattern first", "apps/api/a.ts")}
	fake := fakeSynth{raw: RawSynthesis{
		Themes: []RawTheme{{Title: "Investigate first", Kind: "friction", EvidenceIDs: []string{"F1"},
			CitedQuotes: []string{"investigate the existing pattern first", "this quote was never said at all"}}},
	}}
	adopt := func(r Recommendation) string { return "unknown" }

	rs, report, err := Synthesize(context.Background(), "client-project", group, fake, adopt)
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if len(rs.Themes[0].Quotes) != 1 {
		t.Errorf("kept quotes = %v, want the one verbatim quote to survive", rs.Themes[0].Quotes)
	}
	if report.RawQuoteDropRate != 0.5 {
		t.Errorf("drop rate = %v, want 0.5 (1 of 2 cited quotes dropped)", report.RawQuoteDropRate)
	}
}

func TestSynthesizeClaudeMdRuleRejectsSuccessOnlyEvidence(t *testing.T) {
	group := []insights.AgentSessionAnalysis{frictionAnalysis("s1", "investigate the existing pattern first", "apps/api/a.ts")}
	fake := fakeSynth{raw: RawSynthesis{
		Recommendations: []RawRec{{Type: "claude_md_rule", Statement: "Always write tests first", EvidenceIDs: []string{"S1"}}},
	}}
	adopt := func(r Recommendation) string { return "unknown" }

	_, report, err := Synthesize(context.Background(), "client-project", group, fake, adopt)
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if len(report.HardErrors) == 0 {
		t.Error("expected hard error: claude_md_rule citing a success-only id (S ⊄ P∪F)")
	}
}

func TestSynthesizeClaudeMdRuleAcceptsPrefEvidence(t *testing.T) {
	group := []insights.AgentSessionAnalysis{prefAnalysis("s1", "no bloat", "please avoid bloat in this codebase")}
	fake := fakeSynth{raw: RawSynthesis{
		Recommendations: []RawRec{{Type: "claude_md_rule", Statement: "Avoid unnecessary bloat", EvidenceIDs: []string{"P1"}, Audience: "both"}},
	}}
	adopt := func(r Recommendation) string { return "unknown" }

	_, report, err := Synthesize(context.Background(), "client-project", group, fake, adopt)
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if len(report.HardErrors) != 0 {
		t.Errorf("unexpected hard errors for pref-derived claude_md_rule: %v", report.HardErrors)
	}
}

func TestPrefCountDistinct(t *testing.T) {
	group := []insights.AgentSessionAnalysis{prefAnalysis("s1", "no bloat", "please avoid bloat in this codebase")}
	fake := fakeSynth{raw: RawSynthesis{
		Recommendations: []RawRec{{Type: "claude_md_rule", Statement: "Avoid unnecessary bloat", EvidenceIDs: []string{"P1", "P1"}}},
	}}
	adopt := func(r Recommendation) string { return "unknown" }

	rs, _, err := Synthesize(context.Background(), "client-project", group, fake, adopt)
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if got := rs.Meta.PrefCountByRec[0]; got != 1 {
		t.Errorf("PrefCountByRec[0] = %d, want 1 (P1 cited twice must count once)", got)
	}
}

// TestRankThemesWithinKind is a regression for a bug where all themes were
// ranked in one global ordering: friction scores as a fraction (~0.11) while
// opportunity scores as a raw session count (~15), so an opportunity theme
// stole Rank 1 and "## Top friction themes" started at "2.". Each kind must
// rank independently starting at Rank 1.
func TestRankThemesWithinKind(t *testing.T) {
	themes := []Theme{
		{Kind: "friction", Title: "minor friction", IncidentCount: 5},
		{Kind: "opportunity", Title: "big opportunity", SessionCount: 15},
		{Kind: "friction", Title: "major friction", IncidentCount: 18},
	}
	rankThemes(themes, 100)

	byTitle := map[string]Theme{}
	for _, t := range themes {
		byTitle[t.Title] = t
	}
	if got := byTitle["major friction"].Rank; got != 1 {
		t.Errorf("major friction rank = %d, want 1 (highest incident count within friction)", got)
	}
	if got := byTitle["minor friction"].Rank; got != 2 {
		t.Errorf("minor friction rank = %d, want 2", got)
	}
	if got := byTitle["big opportunity"].Rank; got != 1 {
		t.Errorf("big opportunity rank = %d, want 1 (only opportunity theme, ranked within its own kind)", got)
	}
}

func TestSynthesizeQuantitativeClaimInRecommendation(t *testing.T) {
	group := []insights.AgentSessionAnalysis{frictionAnalysis("s1", "investigate the existing pattern first", "apps/api/a.ts")}
	fake := fakeSynth{raw: RawSynthesis{
		Recommendations: []RawRec{{Type: "claude_md_rule", Statement: "Do this because it happened in 12 sessions", EvidenceIDs: []string{"F1"}}},
	}}
	adopt := func(r Recommendation) string { return "unknown" }

	_, report, err := Synthesize(context.Background(), "client-project", group, fake, adopt)
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if len(report.HardErrors) == 0 {
		t.Error("expected hard error: recommendation statement contains a number")
	}
}

func TestFinalizeIsDeterministicAndUsesProvidedTime(t *testing.T) {
	group := []insights.AgentSessionAnalysis{
		{Stats: insights.AgentSessionStats{SessionID: "s1", Repo: "/Users/x/Developer/myrepo",
			Start: time.Date(2026, 6, 20, 9, 0, 0, 0, time.UTC)},
			JudgedFields: insights.JudgedFields{
				Outcome: "fully_achieved", SessionType: "single_task",
				FrictionIncidents: []insights.FrictionIncident{{Type: "wrong_approach", OneLine: "took a detour"}},
			}},
	}
	b := BuildBundle("myrepo", group)
	raw := RawSynthesis{
		Themes: []RawTheme{{Title: "Detours", Kind: "friction", Summary: "detours happen",
			EvidenceIDs: []string{"F1"}}},
	}
	adopt := func(Recommendation) string { return "no" }
	gen := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)

	rs1, rep1 := Finalize("myrepo", b, raw, adopt, gen)
	rs2, rep2 := Finalize("myrepo", b, raw, adopt, gen)
	if !rs1.GeneratedAt.Equal(gen) {
		t.Fatalf("GeneratedAt = %v, want %v", rs1.GeneratedAt, gen)
	}
	if !reflect.DeepEqual(rs1, rs2) || !reflect.DeepEqual(rep1, rep2) {
		t.Fatal("Finalize is not deterministic for identical inputs")
	}
	if rs1.Repo != "myrepo" || len(rs1.Themes) != 1 {
		t.Fatalf("unexpected synthesis: %+v", rs1)
	}
}

func TestAudienceValidation(t *testing.T) {
	b := EvidenceBundle{Prefs: []PrefItem{{ID: "P1", Rule: "r", Quote: "q", SessionID: "s1"}}}
	adopt := func(Recommendation) string { return "unknown" }
	cases := []struct {
		name     string
		rec      RawRec
		wantHard bool
	}{
		{"claude_md_rule with valid audience", RawRec{Type: "claude_md_rule", Statement: "s", EvidenceIDs: []string{"P1"}, Audience: "subagents"}, false},
		{"claude_md_rule missing audience", RawRec{Type: "claude_md_rule", Statement: "s", EvidenceIDs: []string{"P1"}}, true},
		{"invalid audience value", RawRec{Type: "habit", Statement: "s", Audience: "everyone"}, true},
		{"non-rule without audience is fine", RawRec{Type: "habit", Statement: "s"}, false},
		{"user audience valid", RawRec{Type: "new_skill", Statement: "s", Audience: "user"}, false},
	}
	for _, c := range cases {
		rs, rep := Finalize("r", b, RawSynthesis{Recommendations: []RawRec{c.rec}}, adopt, time.Unix(0, 0).UTC())
		if got := len(rep.HardErrors) > 0; got != c.wantHard {
			t.Errorf("%s: hard=%v (%v), want %v", c.name, got, rep.HardErrors, c.wantHard)
		}
		if !c.wantHard && rs.Recommendations[0].Audience != c.rec.Audience {
			t.Errorf("%s: audience not carried: %+v", c.name, rs.Recommendations[0])
		}
	}
}

func TestAudienceSurvivesRoundTrip(t *testing.T) {
	rec := Recommendation{Type: "claude_md_rule", Statement: "s", Audience: "both"}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	var back Recommendation
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.Audience != "both" {
		t.Errorf("audience lost in round-trip (the SignalRefs json:\"-\" lesson): %+v", back)
	}
}

func TestDetailExemplarsQuotable(t *testing.T) {
	b := EvidenceBundle{
		Signals: []OppSignal{{ID: "G1", Kind: "retyped_directives", Magnitude: 3,
			MemberSessions: []string{"s1", "s2", "s3"},
			Detail:         []string{"please assign an opus subagent to do a critical review"}}},
	}
	raw := RawSynthesis{Themes: []RawTheme{{
		Title: "Recurring review ritual", Kind: "opportunity", Summary: "promote the ritual to a skill",
		SignalRefs:  []string{"G1"},
		CitedQuotes: []string{"please assign an opus subagent to do a critical review"},
	}}}
	rs, rep := Finalize("r", b, raw, func(Recommendation) string { return "unknown" }, time.Unix(0, 0).UTC())
	if len(rep.HardErrors) != 0 {
		t.Fatalf("hard errors: %v", rep.HardErrors)
	}
	if len(rs.Themes[0].Quotes) != 1 {
		t.Errorf("detail exemplar quote was dropped: %+v", rs.Themes[0])
	}
}
