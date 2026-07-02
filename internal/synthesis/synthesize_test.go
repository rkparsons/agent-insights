package synthesis

import (
	"context"
	"testing"

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
			EvidenceIDs: []string{"F1"}, ThemeRefs: []int{0}, CitedQuotes: []string{"investigate the existing pattern first"}}},
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
		Recommendations: []RawRec{{Type: "claude_md_rule", Statement: "Avoid unnecessary bloat", EvidenceIDs: []string{"P1"}}},
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
