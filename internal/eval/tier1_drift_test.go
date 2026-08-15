package eval

import (
	"context"
	"testing"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
	"github.com/rkparsons/agent-insights/internal/synthesis"
)

// Two tier-1 signals are parsed out of the verifier's own note PROSE — the
// quote-drop rate and the adopted-verdict downgrades — so a reworded note in
// verify2.go would silently zero them while every unit test on either side
// still passed. A hand-copied note string in tier1_test.go cannot catch that:
// it drifts with the parser, not with the verifier. These tests run the REAL
// verifier over a fixture that trips each correction and assert the tier-1
// parser counts it.

var driftGeneratedAt = time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC)

// driftPoolQuote is verbatim in the fixture's quote pool; driftInventedQuote is
// in no pool at all. Both clear the quote index's minimum length, so only
// provenance decides which survives.
const (
	driftPoolQuote     = "always run the smoke test before calling it done"
	driftInventedQuote = "the model made this line up out of nothing"
)

// driftBundles is the evidence the real verifier checks against: synthetic
// repo keys and session ids only (the committed-fixture privacy constraint).
func driftBundles() map[string]synthesis.EvidenceBundle {
	return map[string]synthesis.EvidenceBundle{
		"alpha": {
			Repo: "alpha", SessionCount: 2, AnalyzedCount: 2,
			From: "2026-06-01", To: "2026-06-09",
			Prefs: []synthesis.PrefItem{
				{ID: "P1", Rule: "smoke test first", Quote: driftPoolQuote, SessionID: "sess-a1"},
			},
			Friction: []synthesis.FrictionItem{
				{ID: "F1", Type: "rework", OneLine: "shipped unrun", Quote: "the build passed but nobody ran it", SessionID: "sess-a2"},
			},
			SessionDates: map[string]string{"sess-a1": "2026-06-01", "sess-a2": "2026-06-09"},
		},
	}
}

// driftFinding verifies clean: every soft correction under test is added by
// the caller.
func driftFinding() insights.RawFinding {
	return insights.RawFinding{
		Rank:          1,
		Title:         "Run the smoke test first",
		Statement:     "Run the smoke test before calling a task done.",
		RankRationale: "Cheap to codify and it catches regressions before they ship.",
		Asset: insights.AssetJSON{Type: "claude_md_rule", Target: "~/.claude/CLAUDE.md",
			Content: "Run the smoke test before calling a task done."},
		Audience:       "user",
		EvidenceIDs:    []string{"alpha/P1", "alpha/F1"},
		AlreadyAdopted: insights.AdoptedJSON{Verdict: "no"},
	}
}

// verifyForDrift runs the production verifier and returns the sample shape the
// tier-1 gates read. Corrections must be SOFT: a hard failure means the
// fixture drifted, not the note wording.
func verifyForDrift(t *testing.T, f insights.RawFinding) VerifiedOutput {
	t.Helper()
	raw := insights.RawGlobalSynthesis{SchemaVersion: 3, Findings: []insights.RawFinding{f}}
	snap, err := synthesis.VerifyGlobal(context.Background(), raw, driftBundles(),
		insights.Config{SynthesisModel: "test-model"}, driftGeneratedAt)
	if err != nil {
		t.Fatalf("the fixture must verify apart from its soft correction: %v", err)
	}
	return VerifiedOutput{Snapshot: snap, Raw: raw}
}

func driftGates(t *testing.T, vo VerifiedOutput) Tier1Gates {
	t.Helper()
	rec, cache := tier1Case(t, driftBundles(), []VerifiedOutput{vo}, 0)
	t1, _, _, _, err := ComputeTier1(rec, cache, nil)
	if err != nil {
		t.Fatal(err)
	}
	return t1
}

func TestTier1QuoteDropParserTracksTheVerifier(t *testing.T) {
	f := driftFinding()
	f.Quotes = []string{driftPoolQuote, driftInventedQuote}
	vo := verifyForDrift(t, f)

	if len(vo.Snapshot.Findings) != 1 || len(vo.Snapshot.Findings[0].Quotes) != 1 {
		t.Fatalf("the verifier must drop the invented quote and keep the pool one: %+v", vo.Snapshot.Findings)
	}
	t1 := driftGates(t, vo)
	if got := t1.MaxRawFabricationRate; got < 0.49 || got > 0.51 {
		t.Fatalf("fabrication rate = %v, want one of the two checked quotes — the tier-1 quote-drop parser no longer matches the verifier's note wording (verify2.go filterQuotes): %v",
			got, vo.Snapshot.Meta.ValidationNotes)
	}
	if t1.ValidationNoteCount != 1 {
		t.Fatalf("validation notes = %d, want the verifier's one note: %v", t1.ValidationNoteCount, vo.Snapshot.Meta.ValidationNotes)
	}
}

func TestTier1AdoptedDowngradeParserTracksTheVerifier(t *testing.T) {
	f := driftFinding()
	f.AlreadyAdopted = insights.AdoptedJSON{Verdict: "yes",
		SourcePath: "~/.claude/no-such-file-for-the-tier1-drift-test.md",
		Excerpt:    "Run the smoke test before calling a task done."}
	vo := verifyForDrift(t, f)

	if len(vo.Snapshot.Findings) != 1 || vo.Snapshot.Findings[0].AlreadyAdopted.Verdict != "unknown" {
		t.Fatalf("the verifier must downgrade an unverifiable adopted verdict: %+v", vo.Snapshot.Findings)
	}
	t1 := driftGates(t, vo)
	if t1.AdoptedDowngradeCount != 1 {
		t.Fatalf("adopted downgrades = %d, want the verifier's one downgrade — the tier-1 parser no longer matches verify2.go's downgradeAdopted note wording: %v",
			t1.AdoptedDowngradeCount, vo.Snapshot.Meta.ValidationNotes)
	}
}
