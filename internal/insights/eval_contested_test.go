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
