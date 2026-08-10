package synthesis

import (
	"strings"
	"testing"
	"time"
)

func sampleSynthesis() RepoSynthesis {
	return RepoSynthesis{
		Repo: "alpha", GeneratedAt: time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
		Window: Window{From: "2026-06-01", To: "2026-06-30", AnalyzedCount: 159},
		Themes: []Theme{{Title: "Investigate first", Kind: "friction", Rank: 1, IncidentCount: 18, SessionCount: 15,
			TypeBreakdown: map[string]int{"wrong_approach": 12}, Quotes: []string{"investigate the existing pattern first"}}},
		Recommendations: []Recommendation{{Type: "claude_md_rule", Statement: "Investigate existing patterns before writing new code",
			SessionCount: 15, AlreadyAdopted: "no"}},
		Meta: Meta{Model: "claude-opus-4-8", UnthemedFriction: 3},
	}
}

func TestRenderAndScan(t *testing.T) {
	md := Render(sampleSynthesis())
	if !strings.Contains(md, "# alpha") {
		t.Error("missing repo header")
	}
	if !strings.Contains(md, "Investigate first") || !strings.Contains(md, "18") {
		t.Error("missing theme title or Go-computed count")
	}
	if !strings.Contains(md, "unthemed") {
		t.Error("must surface unthemed-friction residual")
	}
	if leaks := scanReport(md); len(leaks) != 0 {
		t.Errorf("privacy scan found leaks: %v", leaks)
	}
}

func TestScanReportCatchesLeak(t *testing.T) {
	if leaks := scanReport("see /Users/dev/secret"); len(leaks) == 0 {
		t.Error("scanReport must flag a /Users/ path")
	}
}

func TestRenderUnthemedFrictionWithoutFrictionThemes(t *testing.T) {
	s := sampleSynthesis()
	s.Themes = []Theme{{Title: "Batching opportunity", Kind: "opportunity", SessionCount: 4}}
	s.Meta.UnthemedFriction = 5
	md := Render(s)
	if strings.Contains(md, "## Top friction themes") {
		t.Error("no friction themes exist; heading must not render")
	}
	if !strings.Contains(md, "5 friction incidents are unthemed") {
		t.Error("unthemed-friction residual must surface even with zero friction themes")
	}
}

func TestRenderRedactsQuantitativeClaims(t *testing.T) {
	s := sampleSynthesis()
	s.Recommendations = []Recommendation{{Type: "claude_md_rule", Statement: "Do this because it worked in 40% of sessions",
		SessionCount: 15, AlreadyAdopted: "no"}}
	s.Meta.ValidationErrors = []string{"recommendation statement contains a number: Do this because it worked in 40% of sessions"}
	md := Render(s)
	if strings.Contains(md, "40%") {
		t.Error("LLM-authored quantitative claim leaked into rendered recommendation/footer")
	}
	if !strings.Contains(md, "[redacted]") {
		t.Error("expected [redacted] placeholder in place of the quantitative claim")
	}
}

func TestRenderAlreadyAdoptedBranch(t *testing.T) {
	s := sampleSynthesis()
	s.Recommendations = []Recommendation{
		{Type: "claude_md_rule", Statement: "Fresh rec", SessionCount: 15, AlreadyAdopted: "no"},
		{Type: "workflow_tip", Statement: "Adopted rec", SessionCount: 8, AlreadyAdopted: "yes"},
	}
	md := Render(s)
	if !strings.Contains(md, "## Already in place (reinforce?)") {
		t.Error("missing already-adopted heading")
	}
	adoptedIdx := strings.Index(md, "## Already in place (reinforce?)")
	recsIdx := strings.Index(md, "## Recommendations")
	if recsIdx < 0 || adoptedIdx < recsIdx {
		t.Fatal("expected Recommendations section before Already-in-place section")
	}
	if strings.Contains(md[:adoptedIdx], "Adopted rec") {
		t.Error("already-adopted recommendation must not render under ## Recommendations")
	}
	if !strings.Contains(md[adoptedIdx:], "Adopted rec") {
		t.Error("already-adopted recommendation must render under ## Already in place (reinforce?)")
	}
	if strings.Contains(md[adoptedIdx:], "Fresh rec") {
		t.Error("fresh recommendation must not render under ## Already in place (reinforce?)")
	}
}

func TestRenderRecommendationTitles(t *testing.T) {
	s := RepoSynthesis{Repo: "r", Recommendations: []Recommendation{
		{Type: "habit", Title: "Verify before claiming done", Statement: "Always verify.", SessionCount: 2},
		{Type: "hook", Statement: "Untitled legacy rec", SessionCount: 1},
		{Type: "habit", Title: "Adopted handle", Statement: "Adopted one.", AlreadyAdopted: "yes"},
	}}
	md := Render(s)
	for _, want := range []string{
		"- `[habit]` **Verify before claiming done** — Always verify. (evidence: 2 sessions)",
		"- `[hook]` Untitled legacy rec (evidence: 1 sessions)",
		"- `[habit]` **Adopted handle** — Adopted one.",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("render missing %q in:\n%s", want, md)
		}
	}
}
