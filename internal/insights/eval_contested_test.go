package insights

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestContestedPredicates(t *testing.T) {
	inc := FrictionIncident{Type: "wrong_approach", OneLine: "x"}

	// (a) outcome disagreement.
	a := sessionRun{Repeats: []RepeatResult{repeat(jf("fully_achieved")), repeat(jf("mostly_achieved"))}}
	if ok, _ := contested(a, scoreSession(a)); !ok {
		t.Error("(a) outcome disagreement should be contested")
	}
	// (b) borderline: unclear present.
	b := sessionRun{Repeats: []RepeatResult{repeat(jf("unclear"))}}
	if ok, _ := contested(b, scoreSession(b)); !ok {
		t.Error("(b) unclear should be contested")
	}
	// (d) false-friction.
	d := sessionRun{ZeroFriction: true, Repeats: []RepeatResult{repeat(jf("fully_achieved", inc))}}
	if ok, _ := contested(d, scoreSession(d)); !ok {
		t.Error("(d) false-friction should be contested")
	}
	// (e) recall.
	e := sessionRun{Frictionful: true, Repeats: []RepeatResult{repeat(jf("fully_achieved")), repeat(jf("fully_achieved"))}}
	if ok, _ := contested(e, scoreSession(e)); !ok {
		t.Error("(e) recall miss should be contested")
	}
	// clean stable zero-friction session → not contested.
	clean := sessionRun{ZeroFriction: true, Repeats: []RepeatResult{repeat(jf("fully_achieved")), repeat(jf("fully_achieved"))}}
	if ok, _ := contested(clean, scoreSession(clean)); ok {
		t.Error("clean stable session should not be contested")
	}
}

func TestBuildCardsDedupsFrictionByType(t *testing.T) {
	// The model rephrases one_line each repeat, so exact (type, one_line) dedup lets
	// every rephrasing of the same incident become its own card. Dedup at type level:
	// three rephrased wrong_approach incidents + one buggy_code → two friction cards.
	wa1 := FrictionIncident{Type: "wrong_approach", OneLine: "under-explored the header component", EvidenceQuote: "q1"}
	wa2 := FrictionIncident{Type: "wrong_approach", OneLine: "did not explore header variants enough", EvidenceQuote: "q2"}
	wa3 := FrictionIncident{Type: "wrong_approach", OneLine: "header exploration was too shallow", EvidenceQuote: "q3"}
	bug := FrictionIncident{Type: "buggy_code", OneLine: "mockups rendered squashed", EvidenceQuote: "q4"}
	sr := sessionRun{
		Stats:         AgentSessionStats{AiTitle: "Design system"},
		FirstUserTurn: "build the design system",
		ZeroFriction:  true, // false-friction on a clean session → contested, so cards render
		Repeats: []RepeatResult{
			repeat(jf("fully_achieved", wa1, bug)),
			repeat(jf("fully_achieved", wa2)),
			repeat(jf("fully_achieved", wa3)),
		},
	}
	cards := buildCards(sr, scoreSession(sr))
	frictionCards := 0
	for _, c := range cards {
		if strings.HasPrefix(c.Claim, "Friction [") {
			frictionCards++
		}
	}
	if frictionCards != 2 {
		t.Errorf("friction cards = %d, want 2 (one per type: wrong_approach, buggy_code)", frictionCards)
	}
}

func TestBuildCardsDropsMetaSession(t *testing.T) {
	// The meta/insights-dev session folds into the metrics (F3) but its cards are
	// withheld from the human pass — empty title + structured-output-as-friction are
	// low recognition value. Display-only: buildCards returns nil for a meta session.
	inc := FrictionIncident{Type: "wrong_approach", OneLine: "x", EvidenceQuote: "q"}
	sr := sessionRun{
		Stats:         AgentSessionStats{Cwd: "/home/rp/insights-dev", AiTitle: ""},
		FirstUserTurn: "analyze this session",
		ZeroFriction:  true,
		Repeats:       []RepeatResult{repeat(jf("unclear", inc))},
	}
	sc := scoreSession(sr)
	if !sc.IsMeta {
		t.Fatal("fixture should be a meta session")
	}
	if ok, _ := contested(sr, sc); !ok {
		t.Fatal("fixture should be contested (else the drop is untested)")
	}
	if cards := buildCards(sr, sc); cards != nil {
		t.Errorf("meta session cards = %d, want 0 (withheld from human pass)", len(cards))
	}
}

func TestCardsNoIdentifierLeak(t *testing.T) {
	const secretID = "9f8e7d6c-dead-beef-0000-111122223333"
	inc := FrictionIncident{Type: "wrong_approach", OneLine: "did the wrong thing", EvidenceQuote: "this is the evidence"}
	sr := sessionRun{
		Stats:         AgentSessionStats{SessionID: secretID, Cwd: "/secret/path/client-project", AiTitle: "Fixing the parser"},
		FirstUserTurn: "please fix the parser bug",
		ZeroFriction:  true,
		Repeats:       []RepeatResult{repeat(jf("fully_achieved", inc))},
	}
	cards := buildCards(sr, scoreSession(sr))
	if len(cards) == 0 {
		t.Fatal("expected cards for a contested session")
	}
	blob, _ := json.Marshal(cards)
	if strings.Contains(string(blob), secretID) {
		t.Error("card leaks the session-id")
	}
	if strings.Contains(string(blob), "/secret/path") {
		t.Error("card leaks the cwd")
	}
	if !strings.Contains(string(blob), "Fixing the parser") || !strings.Contains(string(blob), "fix the parser bug") {
		t.Error("card should carry title + opening for recognition")
	}
}
