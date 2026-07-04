package insightseval

import (
	"testing"
)

func sample(idx int, gran string) SampleScore {
	return SampleScore{SampleIndex: idx, Granularity: gran, RepeatAgreement: 1,
		Corroboration: CorroborationOK, ItemRef: "client-project/theme/0", ItemText: "Verify claims",
		ItemSessionIDs: []string{"a1", "a2"}, ItemQuotes: []string{"q"}}
}

func hasTrigger(tv TargetVerdict, typ string) *Trigger {
	for i := range tv.Triggers {
		if tv.Triggers[i].Type == typ {
			return &tv.Triggers[i]
		}
	}
	return nil
}

func TestAggregateTargetMustPassMajorityAndSplit(t *testing.T) {
	r := scoreRubric() // Task 6's fixture: HIGH, pass_at full
	samples := []SampleScore{sample(0, "full"), sample(1, "full"), sample(2, "absent")}
	tv, cards := AggregateTarget(r, "must_pass", samples, 2, nil, true)
	if !tv.Pass || tv.Granularity != "full" || tv.HardFail {
		t.Fatalf("majority pass: %+v", tv)
	}
	if tv.SampleAgreement < 0.66 || tv.SampleAgreement > 0.67 {
		t.Fatalf("agreement: %v", tv.SampleAgreement)
	}
	// sample-split: card WITHOUT provisional-fail — the majority stands (spec)
	tr := hasTrigger(tv, "sample_split")
	if tr == nil || tv.ProvisionalFail {
		t.Fatalf("split trigger: %+v", tv)
	}
	found := false
	for _, c := range cards {
		if c.Trigger == "sample_split" {
			found = true
			if c.Adjudicable {
				t.Fatal("sample splits can never re-key; not adjudicable")
			}
		}
	}
	if !found {
		t.Fatal("sample_split card missing")
	}

	miss := []SampleScore{sample(0, "partial"), sample(1, "absent"), sample(2, "absent")}
	tv, _ = AggregateTarget(r, "must_pass", miss, 2, nil, true)
	if tv.Pass || !tv.HardFail { // HIGH must_pass miss = hard fail
		t.Fatalf("HIGH miss: %+v", tv)
	}
}

func TestAggregateTargetFirstPassNoAnchor(t *testing.T) {
	r := scoreRubric()
	r.AnchorSessionIDs = nil
	samples := []SampleScore{sample(0, "full"), sample(1, "full"), sample(2, "full")}
	for i := range samples {
		samples[i].Corroboration = CorroborationNoAnchors
	}
	tv, cards := AggregateTarget(r, "must_pass", samples, 0, nil, false)
	if tv.Pass || !tv.ProvisionalFail {
		t.Fatalf("first-ever no-anchor pass must provisional-fail: %+v", tv)
	}
	if tv.HardFail {
		t.Fatal("a provisional would-pass is not a HIGH miss — it must not hard-fail")
	}
	tr := hasTrigger(tv, "first_pass_no_anchor")
	if tr == nil || tr.KeyHash == "" {
		t.Fatalf("trigger: %+v", tv.Triggers)
	}
	if len(cards) == 0 || !cards[0].Adjudicable {
		t.Fatalf("card: %+v", cards)
	}

	// an accepted adjudication lifts it
	k := AdjKey{TargetID: r.ID, Statement: normalizeStatement("Verify claims"),
		IDSetHash: idSetHash([]string{"a1", "a2"}), RubricHash: r.Hash, Trigger: "first_pass_no_anchor"}
	adj := map[string]Adjudication{k.Hash(): {Key: k, KeyHash: k.Hash(), Decision: "accept"}}
	tv, cards = AggregateTarget(r, "must_pass", samples, 0, adj, false)
	if !tv.Pass || tv.ProvisionalFail || len(cards) != 0 {
		t.Fatalf("adjudicated: %+v cards=%d", tv, len(cards))
	}

	// already effectively passed before → no trigger at all
	tv, _ = AggregateTarget(r, "must_pass", samples, 0, nil, true)
	if !tv.Pass || hasTrigger(tv, "first_pass_no_anchor") != nil {
		t.Fatalf("everPassed: %+v", tv)
	}
}

func TestAggregateTargetStatusSemantics(t *testing.T) {
	r := scoreRubric()
	full := []SampleScore{sample(0, "full"), sample(1, "full"), sample(2, "full")}
	absent := []SampleScore{sample(0, "absent"), sample(1, "absent"), sample(2, "absent")}
	og := []SampleScore{sample(0, "over_generalized"), sample(1, "over_generalized"), sample(2, "over_generalized")}

	// expected_partial: absent = presence regression (hard fail); og = expected;
	// full = ratchet candidate
	tv, _ := AggregateTarget(r, "expected_partial", absent, 2, nil, true)
	if !tv.HardFail || tv.MeetsExpectation {
		t.Fatalf("expected_partial absent: %+v", tv)
	}
	tv, _ = AggregateTarget(r, "expected_partial", og, 2, nil, true)
	if tv.HardFail || !tv.MeetsExpectation || tv.Pass {
		t.Fatalf("expected_partial og: %+v", tv)
	}
	tv, cards := AggregateTarget(r, "expected_partial", full, 2, nil, true)
	if hasTrigger(tv, "ratchet_candidate") == nil || len(cards) == 0 {
		t.Fatalf("expected_partial full must card ratchet: %+v", tv)
	}

	// expected_fail: never a failure; a would-pass is gap progress + ratchet card
	tv, _ = AggregateTarget(r, "expected_fail", absent, 0, nil, false)
	if tv.HardFail || !tv.MeetsExpectation {
		t.Fatalf("expected_fail absent: %+v", tv)
	}
	tv, _ = AggregateTarget(r, "expected_fail", full, 0, nil, false)
	if tv.HardFail || hasTrigger(tv, "ratchet_candidate") == nil {
		t.Fatalf("expected_fail would-pass: %+v", tv)
	}
	if hasTrigger(tv, "first_pass_no_anchor") != nil {
		t.Fatal("gap targets ratchet-card, never first-pass-card")
	}

	// needs_reconfirmation: scores as expected_fail + forced card every run
	tv, cards = AggregateTarget(r, "needs_reconfirmation", absent, 2, nil, true)
	if hasTrigger(tv, "needs_reconfirmation") == nil || len(cards) == 0 {
		t.Fatalf("needs_reconfirmation forced card: %+v", tv)
	}
}

func TestAggregateTargetSideMatchTriggers(t *testing.T) {
	r := scoreRubric()
	s := sample(0, "absent")
	s.ItemRef, s.ItemText, s.ItemSessionIDs, s.ItemQuotes, s.Corroboration = "", "", nil, nil, ""
	s.SideMatches = []SideMatch{{Ref: "client-project/theme/1", Text: "Mega theme", Granularity: "full",
		Corroboration: CorroborationMismatch, SessionIDs: []string{"x1", "x2"}}}
	samples := []SampleScore{s, s, s}
	for i := range samples {
		samples[i].SampleIndex = i
	}
	tv, cards := AggregateTarget(r, "must_pass", samples, 4, nil, true)
	tr := hasTrigger(tv, CorroborationMismatch)
	if tr == nil || tr.KeyHash == "" {
		t.Fatalf("anchor_mismatch trigger: %+v", tv.Triggers)
	}
	var card *PendingCard
	for i := range cards {
		if cards[i].Trigger == CorroborationMismatch {
			card = &cards[i]
		}
	}
	if card == nil || card.ItemText != "Mega theme" || !card.Adjudicable {
		t.Fatalf("membership card: %+v", cards)
	}
	// a rejected adjudication suppresses the card but keeps the trigger visible
	adj := map[string]Adjudication{tr.KeyHash: {KeyHash: tr.KeyHash, Decision: "reject"}}
	tv, cards = AggregateTarget(r, "must_pass", samples, 4, adj, true)
	if tr = hasTrigger(tv, CorroborationMismatch); tr == nil || tr.Adjudicated != "reject" {
		t.Fatalf("adjudicated trigger: %+v", tv.Triggers)
	}
	for _, c := range cards {
		if c.Trigger == CorroborationMismatch {
			t.Fatal("rejected adjudication must suppress the card")
		}
	}
}

func TestAggregateTargetEmptySamplesNeverVacuouslyPasses(t *testing.T) {
	r := scoreRubric()
	tv, cards := AggregateTarget(r, "must_pass", nil, 2, nil, true)
	if tv.Pass || tv.MeetsExpectation || tv.Granularity != "absent" {
		t.Fatalf("empty samples must score absent, never a pass: %+v", tv)
	}
	if !tv.HardFail {
		t.Fatal("an unscoreable HIGH must_pass target must hard-fail, never degrade to a warning")
	}
	if len(tv.Samples) != 0 || len(tv.Triggers) != 0 || len(cards) != 0 {
		t.Fatalf("empty samples must produce no sample entries, triggers, or cards: %+v %+v", tv, cards)
	}
	gap, _ := AggregateTarget(r, "expected_fail", nil, 0, nil, false)
	if gap.HardFail || !gap.MeetsExpectation {
		t.Fatalf("an unscoreable gap target stays informational: %+v", gap)
	}
}
