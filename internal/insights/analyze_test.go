package insights

import (
	"context"
	"strings"
	"testing"

	"tmux-ctrl/internal/transcript"
)

func TestAnalyzeMergesValidatesDropsAndFlags(t *testing.T) {
	raw := `{"type":"user","sessionId":"sess1","cwd":"/repo","message":{"role":"user","content":[{"type":"text","text":"the approach was wrong; please follow the existing conventions and keep the diff small"}]}}`
	ev, c := transcript.DecodeTranscript(strings.NewReader(raw))

	judge := fakeJudge{fields: JudgedFields{
		UnderlyingGoal:    "fix it",
		SessionType:       "single_task",
		Outcome:           "mostly_achieved",
		BriefSummary:      "did the thing",
		FrictionIncidents: []FrictionIncident{{Type: "wrong_approach", OneLine: "bad approach", EvidenceQuote: "the approach was wrong"}},
		StandingPreferences: []StandingPreference{
			{Rule: "follow conventions", EvidenceQuote: "follow the existing conventions"}, // verbatim user → kept
			{Rule: "invented", EvidenceQuote: "a rule the user never actually said here"},  // not in corpus → dropped
		},
	}}

	a, _, err := Analyze(context.Background(), ev, c, "sess1", noRepo, judge)
	if err != nil {
		t.Fatal(err)
	}
	if a.Stats.SessionID != "sess1" {
		t.Errorf("stats not merged: %q", a.Stats.SessionID)
	}
	if a.UnderlyingGoal != "fix it" || a.Outcome != "mostly_achieved" {
		t.Errorf("judged fields not merged: %+v", a.JudgedFields)
	}
	if len(a.FrictionIncidents) != 1 || a.FrictionIncidents[0].QuoteUnverified {
		t.Errorf("verbatim friction should survive intact: %+v", a.FrictionIncidents)
	}
	if len(a.StandingPreferences) != 1 || a.StandingPreferences[0].Rule != "follow conventions" {
		t.Errorf("only the verbatim preference should survive: %+v", a.StandingPreferences)
	}
}

func TestAnalyzePropagatesJudgeError(t *testing.T) {
	ev, c := transcript.DecodeTranscript(strings.NewReader(`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"hi there friend"}]}}`))
	_, _, err := Analyze(context.Background(), ev, c, "s", noRepo, fakeJudge{err: context.DeadlineExceeded})
	if err == nil {
		t.Error("expected judge error to propagate")
	}
}
