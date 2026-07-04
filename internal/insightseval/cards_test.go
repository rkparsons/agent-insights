package insightseval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tmux-ctrl/internal/synthesis"
)

func cardResults() []TargetResult {
	r := scoreRubric() // Task 6 fixture: ID C-77, expected bucket client-project
	return []TargetResult{{
		Rubric: r,
		Verdict: TargetVerdict{ID: r.ID},
		Pending: []PendingCard{
			{TargetID: r.ID, Trigger: CorroborationMismatch, Adjudicable: true,
				Key: AdjKey{TargetID: r.ID, Statement: "mega theme", IDSetHash: idSetHash([]string{"sA", "sX"}), RubricHash: r.Hash, Trigger: CorroborationMismatch},
				Ref: "client-project/theme/1", ItemText: "Mega theme", Granularity: "full",
				SessionIDs: []string{"sA", "sX"}},
			{TargetID: r.ID, Trigger: "sample_split", Adjudicable: false,
				ItemText: "Verify claims", Quotes: []string{"vq"}, Note: "samples [2] disagree"},
		},
	}}
}

func TestBuildCardsMembershipOneLines(t *testing.T) {
	anchors := map[string][]string{"C-77": {"sA", "sB"}}
	oneLines := map[string]map[string]string{"client-project": {
		"sA": "took a detour", "sB": "diffed stale base", "sX": "unrelated session",
	}}
	cards, err := BuildCards(cardResults(), anchors, oneLines)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 2 {
		t.Fatalf("cards = %d", len(cards))
	}
	var mem *Card
	for i := range cards {
		if cards[i].Trigger == CorroborationMismatch {
			mem = &cards[i]
		}
	}
	if mem == nil || mem.KeyHash == "" || !mem.Adjudicable {
		t.Fatalf("membership card: %+v", cards)
	}
	if mem.Statement == "" || mem.ItemText != "Mega theme" {
		t.Fatalf("recognition surface: %+v", mem)
	}
	if len(mem.AddedOneLines) != 1 || mem.AddedOneLines[0] != "unrelated session" {
		t.Fatalf("added one-lines: %v", mem.AddedOneLines)
	}
	if len(mem.MissingOneLines) != 1 || mem.MissingOneLines[0] != "diffed stale base" {
		t.Fatalf("missing one-lines: %v", mem.MissingOneLines)
	}
}

func TestBuildCardsRejectsSessionIDLeak(t *testing.T) {
	results := cardResults()
	// a one_line that itself contains a session uuid must be caught
	oneLines := map[string]map[string]string{"client-project": {
		"sX": "mentions 00000000-0000-4000-8000-00000000dead verbatim",
	}}
	if _, err := BuildCards(results, map[string][]string{"C-77": {"sA", "sB"}}, oneLines); err == nil {
		t.Fatal("cards containing a session id must be rejected")
	}
}

func TestSessionOneLinesPreference(t *testing.T) {
	b := synthesis.EvidenceBundle{
		Friction: []synthesis.FrictionItem{{ID: "F1", OneLine: "friction line", SessionID: "s1"}},
		Prefs:    []synthesis.PrefItem{{ID: "P1", Rule: "pref rule", SessionID: "s1"}, {ID: "P2", Rule: "pref only", SessionID: "s2"}},
		Success:  []synthesis.SuccessItem{{ID: "S1", Summary: "success summary", SessionID: "s3"}},
	}
	got := sessionOneLines(b)
	if got["s1"] != "friction line" || got["s2"] != "pref only" || got["s3"] != "success summary" {
		t.Fatalf("one lines: %v", got)
	}
}

func TestWriteCardsAndMarkdown(t *testing.T) {
	cards, err := BuildCards(cardResults(),
		map[string][]string{"C-77": {"sA", "sB"}},
		map[string]map[string]string{"client-project": {"sB": "diffed stale base", "sX": "unrelated"}})
	if err != nil {
		t.Fatal(err)
	}
	cacheDir := t.TempDir()
	dir, err := WriteCards(cacheDir, "2026-07-05T10-00-00Z", cards)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 { // 2 card jsons + cards.md
		t.Fatalf("entries: %d", len(entries))
	}
	md, err := os.ReadFile(filepath.Join(dir, "cards.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(md)
	if !strings.Contains(s, "C-77") || !strings.Contains(s, "insights eval adjudicate") {
		t.Fatalf("markdown: %s", s)
	}
	if strings.Contains(s, "sA") || strings.Contains(s, "sX") {
		t.Fatal("markdown must not contain session ids")
	}
}
