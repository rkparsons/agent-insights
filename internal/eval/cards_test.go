package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rkparsons/agent-insights/internal/synthesis"
)

func cardResults() []TargetResult {
	r := scoreRubric() // Task 6 fixture: ID C-77
	return []TargetResult{{
		Rubric:  r,
		Verdict: TargetVerdict{ID: r.ID},
		Pending: []PendingCard{
			{TargetID: r.ID, Trigger: CorroborationMismatch, Adjudicable: true,
				Key: AdjKey{TargetID: r.ID, Statement: "mega finding", IDSetHash: idSetHash([]string{"sA", "sX"}), RubricHash: r.Hash, Trigger: CorroborationMismatch},
				Ref: "finding/2", ItemText: "Mega finding", Granularity: "full",
				SessionIDs: []string{"sA", "sX"}},
			{TargetID: r.ID, Trigger: "sample_split", Adjudicable: false,
				ItemText: "Verify claims", Quotes: []string{"vq"}, Note: "samples [2] disagree"},
		},
	}}
}

func TestBuildCardsMembershipOneLines(t *testing.T) {
	anchors := map[string][]string{"C-77": {"sA", "sB"}}
	oneLines := map[string]string{
		"sA": "took a detour", "sB": "diffed stale base", "sX": "unrelated session",
	}
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
	if mem.Statement == "" || mem.ItemText != "Mega finding" {
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
	oneLines := map[string]string{"sX": "mentions 0abc1234-de56-4f78-9abc-def012345678 verbatim"}
	if _, err := BuildCards(results, map[string][]string{"C-77": {"sA", "sB"}}, oneLines); err == nil {
		t.Fatal("cards containing a session id must be rejected")
	}
}

// A dropped entry that matched a rubric cards with the same membership
// vocabulary as a corroboration failure: what it cited, what it missed.
func TestBuildCardsDroppedEntry(t *testing.T) {
	r := scoreRubric()
	results := []TargetResult{{Rubric: r, Verdict: TargetVerdict{ID: r.ID},
		Pending: []PendingCard{{TargetID: r.ID, Trigger: CorroborationDropped, Adjudicable: true,
			Key: AdjKey{TargetID: r.ID, Statement: "nit", IDSetHash: idSetHash([]string{"sA"}),
				RubricHash: r.Hash, Trigger: CorroborationDropped},
			Ref: "dropped/0", ItemText: "comment-style nit", Granularity: "full",
			SessionIDs: []string{"sA"},
			Note:       "the model dropped this evidence — one session only — but it matches the rubric"}}}}
	cards, err := BuildCards(results, map[string][]string{"C-77": {"sA", "sB"}},
		map[string]string{"sA": "took a detour", "sB": "diffed stale base"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 1 || cards[0].Trigger != CorroborationDropped {
		t.Fatalf("dropped card: %+v", cards)
	}
	if len(cards[0].MissingOneLines) != 1 || cards[0].MissingOneLines[0] != "diffed stale base" {
		t.Fatalf("a dropped card must show the anchors it missed: %+v", cards[0])
	}
	md := RenderCardsMarkdown(cards)
	if !strings.Contains(md, "dropped this evidence") {
		t.Fatalf("drop reason must reach the recognition surface: %s", md)
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
		map[string]string{"sB": "diffed stale base", "sX": "unrelated"})
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
