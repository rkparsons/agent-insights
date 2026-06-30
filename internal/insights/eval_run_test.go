package insights

import (
	"context"
	"testing"

	"tmux-ctrl/internal/sources/claude"
)

// scriptedJudge returns a different JudgedFields per call (cycling), so multi-repeat
// behavior is testable. Distinct from the package's fixed-output fakeJudge.
type scriptedJudge struct {
	outputs []JudgedFields
	calls   int
}

func (s *scriptedJudge) Judge(ctx context.Context, in ReducedInput) (JudgedFields, error) {
	o := s.outputs[s.calls%len(s.outputs)]
	s.calls++
	return o, nil
}

func userEvent(text string) claude.TranscriptEvent {
	return claude.TranscriptEvent{Type: "user", Message: &claude.Message{Content: []claude.ContentBlock{{Type: "text", Text: text}}}}
}

func TestRunRepeatCapturesRawAndValidated(t *testing.T) {
	// Transcript where the user literally said a long verbatim phrase.
	events := []claude.TranscriptEvent{userEvent("please follow the existing conventions and do not add comments")}
	ext := Extract(events, claude.Canary{}, "sid", noRepo)

	raw := JudgedFields{
		UnderlyingGoal: "g", SessionType: "single_task", Outcome: "fully_achieved", BriefSummary: "s",
		FrictionIncidents: []FrictionIncident{
			{Type: "wrong_approach", OneLine: "real", EvidenceQuote: "follow the existing conventions"}, // verbatim
			{Type: "buggy_code", OneLine: "fake", EvidenceQuote: "this was never said by anyone here"},  // fabricated
		},
		StandingPreferences: []StandingPreference{
			{Rule: "no comments", EvidenceQuote: "do not add comments"}, // verbatim user words
		},
	}
	judge := &scriptedJudge{outputs: []JudgedFields{raw}}

	rr, err := runRepeat(context.Background(), ext, judge)
	if err != nil {
		t.Fatal(err)
	}
	// Raw is untouched.
	if len(rr.Raw.FrictionIncidents) != 2 {
		t.Fatalf("raw friction=%d want 2", len(rr.Raw.FrictionIncidents))
	}
	// Validated: fabricated friction quote cleared+flagged; verbatim one kept.
	if rr.Validated.FrictionIncidents[0].EvidenceQuote == "" || rr.Validated.FrictionIncidents[0].QuoteUnverified {
		t.Error("verbatim friction quote should survive")
	}
	if !rr.Validated.FrictionIncidents[1].QuoteUnverified {
		t.Error("fabricated friction quote should be flagged")
	}
	// Verbatim preference survives; report counts no drop.
	if len(rr.Validated.StandingPreferences) != 1 || rr.Report.DroppedPreferences != 0 {
		t.Errorf("verbatim pref should survive: prefs=%d dropped=%d", len(rr.Validated.StandingPreferences), rr.Report.DroppedPreferences)
	}
	// Raw-quote fabrication checks: friction[0] verbatim, friction[1] fabricated, pref verbatim.
	wantVerbatim := map[string]bool{
		"follow the existing conventions":    true,
		"this was never said by anyone here": false,
		"do not add comments":                true,
	}
	if len(rr.RawQuotes) != 3 {
		t.Fatalf("raw quotes=%d want 3", len(rr.RawQuotes))
	}
	for _, qc := range rr.RawQuotes {
		if want, ok := wantVerbatim[qc.Quote]; !ok || qc.Verbatim != want {
			t.Errorf("quote %q verbatim=%v want %v", qc.Quote, qc.Verbatim, want)
		}
	}
}

func TestFirstGenuineUserTurnSkipsNoise(t *testing.T) {
	events := []claude.TranscriptEvent{
		userEvent("[Request interrupted by user]"),
		userEvent("<task-notification>done</task-notification>"),
		userEvent("Base directory for this skill: /x"),
		userEvent("here is my actual request"),
		userEvent("a later turn"),
	}
	if got := firstGenuineUserTurn(events); got != "here is my actual request" {
		t.Errorf("got %q", got)
	}
}

func TestFirstGenuineUserTurnTruncates(t *testing.T) {
	long := ""
	for i := 0; i < 400; i++ {
		long += "x"
	}
	got := firstGenuineUserTurn([]claude.TranscriptEvent{userEvent(long)})
	if r := []rune(got); len(r) != openingMaxRunes+1 || r[openingMaxRunes] != '…' {
		t.Errorf("expected %d runes + ellipsis, got %d", openingMaxRunes, len([]rune(got)))
	}
}
