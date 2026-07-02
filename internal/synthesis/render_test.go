package synthesis

import (
	"strings"
	"testing"
	"time"
)

func sampleSynthesis() RepoSynthesis {
	return RepoSynthesis{
		Repo: "client-project", GeneratedAt: time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
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
	if !strings.Contains(md, "# client-project") {
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
