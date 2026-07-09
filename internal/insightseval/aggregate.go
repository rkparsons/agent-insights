package insightseval

import (
	"fmt"
	"sort"
)

// tierWeights drive Part-A recall (plan decision 7).
var tierWeights = map[string]float64{"HIGH": 3, "MODERATE-HIGH": 2, "MODERATE": 1, "MEDIUM": 1}

// Trigger is the lean, committed form of a card trigger: type + adjudication
// key hash only — item text and session material stay in PendingCard, which
// never leaves the local cache.
type Trigger struct {
	Type          string `json:"type"`
	KeyHash       string `json:"key_hash,omitempty"`
	Adjudicated   string `json:"adjudicated,omitempty"`
	SampleIndexes []int  `json:"sample_indexes,omitempty"`
}

type TargetSampleVerdict struct {
	SampleIndex     int     `json:"sample_index"`
	Granularity     string  `json:"granularity"`
	RepeatAgreement float64 `json:"repeat_agreement"`
	RepeatsTaken    int     `json:"repeats_taken,omitempty"` // 0 = no reads (empty payload) or pre-early-exit verdict
	Corroboration   string  `json:"corroboration,omitempty"`
	ItemRef         string  `json:"item_ref,omitempty"`
}

type TargetVerdict struct {
	ID               string                `json:"id"`
	Part             string                `json:"part"`
	Tier             string                `json:"tier,omitempty"`
	Status           string                `json:"status"`
	PassAt           string                `json:"pass_at"`
	Granularity      string                `json:"granularity"`
	Pass             bool                  `json:"pass"`
	MeetsExpectation bool                  `json:"meets_expectation"`
	ProvisionalFail  bool                  `json:"provisional_fail,omitempty"`
	HardFail         bool                  `json:"hard_fail,omitempty"`
	SampleAgreement  float64               `json:"sample_agreement"`
	EffectiveAnchors int                   `json:"effective_anchors"`
	NuancePassMedian int                   `json:"nuance_pass_median"`
	Samples          []TargetSampleVerdict `json:"samples"`
	Triggers         []Trigger             `json:"triggers,omitempty"`
}

// PendingCard is the local-only source a trigger generates; Task 9 renders it
// into recognition cards. Session ids here feed one-line lookups and never
// render into the card itself.
type PendingCard struct {
	TargetID    string
	Trigger     string
	Key         AdjKey
	Adjudicable bool
	Ref         string // item ref ("<bucket>/theme/<i>"), for one-line bucket resolution
	ItemText    string
	Granularity string
	Note        string
	Quotes      []string
	SessionIDs  []string
}

// TargetResult pairs a committed TargetVerdict with its local-only detail.
type TargetResult struct {
	Rubric  Rubric
	Verdict TargetVerdict
	Samples []SampleScore
	Pending []PendingCard
}

// medianNuancePasses takes the conservative lower-middle of the per-sample
// counted-item nuance-pass counts (a sample with no counted item counts 0) —
// the depth figure the recalibration watermark compares against.
func medianNuancePasses(samples []SampleScore) int {
	counts := make([]int, len(samples))
	for i, s := range samples {
		for _, ok := range s.NuancePasses {
			if ok {
				counts[i]++
			}
		}
	}
	sort.Ints(counts)
	return counts[(len(counts)-1)/2]
}

func decidingSample(samples []SampleScore, median string) *SampleScore {
	for i := range samples {
		if samples[i].Granularity == median {
			return &samples[i]
		}
	}
	return nil
}

// AggregateTarget folds one target's per-sample scores into its verdict entry.
// Majority (median) across samples decides pass/fail; matcher repeats already
// stabilized each sample, so only sample-level splits card — without
// provisional-fail, because a genuinely-present target with inherent 2-of-3
// sample agreement would otherwise provisional-fail forever (spec). All other
// unadjudicated triggers provisional-fail a would-pass.
func AggregateTarget(r Rubric, status string, samples []SampleScore, effectiveAnchors int, adj map[string]Adjudication, everPassed bool) (TargetVerdict, []PendingCard) {
	tv := TargetVerdict{ID: r.ID, Part: r.Part, Tier: r.Tier, Status: status,
		PassAt: r.PassAt, EffectiveAnchors: effectiveAnchors, Granularity: "absent"}
	if len(samples) == 0 {
		// Unscoreable target (e.g. expected bucket missing from the record):
		// absent takes full status semantics — fail-closed, a HIGH miss must
		// never degrade to a warning just because nothing could be scored.
		switch status {
		case "must_pass":
			tv.HardFail = r.Tier == "HIGH"
		case "expected_partial":
			tv.HardFail = true // presence regression
		case "expected_fail", "needs_reconfirmation":
			tv.MeetsExpectation = true
		}
		return tv, nil
	}
	var cards []PendingCard
	grans := make([]string, len(samples))
	for i, s := range samples {
		grans[i] = s.Granularity
		tv.Samples = append(tv.Samples, TargetSampleVerdict{SampleIndex: s.SampleIndex,
			Granularity: s.Granularity, RepeatAgreement: s.RepeatAgreement,
			RepeatsTaken: s.RepeatsTaken, Corroboration: s.Corroboration, ItemRef: s.ItemRef})
	}
	tv.Granularity = medianGranularity(grans)
	tv.NuancePassMedian = medianNuancePasses(samples)
	agree := 0
	for _, g := range grans {
		if g == tv.Granularity {
			agree++
		}
	}
	tv.SampleAgreement = float64(agree) / float64(len(grans))
	wouldPass := granularityRank[tv.Granularity] >= granularityRank[r.PassAt]
	deciding := decidingSample(samples, tv.Granularity)

	// addKeyed registers a keyed trigger and, unless already adjudicated,
	// queues its card. Returns the adjudicated decision ("" when none).
	addKeyed := func(typ string, k AdjKey, card PendingCard) string {
		tr := Trigger{Type: typ, KeyHash: k.Hash()}
		if a, ok := adj[k.Hash()]; ok {
			tr.Adjudicated = a.Decision
		}
		tv.Triggers = append(tv.Triggers, tr)
		if tr.Adjudicated == "" {
			card.TargetID, card.Trigger, card.Key = r.ID, typ, k
			cards = append(cards, card)
		}
		return tr.Adjudicated
	}

	if status == "must_pass" {
		var minority []int
		for _, s := range samples {
			if (granularityRank[s.Granularity] >= granularityRank[r.PassAt]) != wouldPass {
				minority = append(minority, s.SampleIndex)
			}
		}
		if len(minority) > 0 {
			tv.Triggers = append(tv.Triggers, Trigger{Type: "sample_split", SampleIndexes: minority})
			cards = append(cards, PendingCard{TargetID: r.ID, Trigger: "sample_split",
				ItemText: textOf(deciding), Quotes: quotesOf(deciding), Granularity: tv.Granularity,
				Note: fmt.Sprintf("samples %v disagree with the majority (%s); majority stands, no provisional-fail", minority, tv.Granularity)})
		}
	}

	// Side matches are collected across ALL samples (deduped by key): the spec
	// cards every non-expected-bucket match, not just the majority sample's.
	seenSide := map[string]bool{}
	for _, s := range samples {
		for _, sm := range s.SideMatches {
			k := AdjKey{TargetID: r.ID, Statement: normalizeStatement(sm.Text),
				IDSetHash: idSetHash(sm.SessionIDs), RubricHash: r.Hash, Trigger: sm.Corroboration}
			if seenSide[k.Hash()] {
				continue
			}
			seenSide[k.Hash()] = true
			addKeyed(sm.Corroboration, k, PendingCard{Adjudicable: true, Ref: sm.Ref, ItemText: sm.Text,
				Granularity: sm.Granularity, SessionIDs: sm.SessionIDs,
				Note: "matcher-matched but uncounted (" + sm.Corroboration + ")"})
		}
	}
	if deciding != nil {
		for _, h := range deciding.AdjApplied {
			tv.Triggers = append(tv.Triggers, Trigger{Type: "adjudication_applied", KeyHash: h, Adjudicated: "accept"})
		}
	}

	// Grounding-only oversight (rec-surface corroboration amendment, review
	// C1): a HIGH pass carried by grounded — precision-path — samples gets a
	// standing informational card; the pass stands, no provisional-fail.
	if status == "must_pass" && wouldPass && r.Tier == "HIGH" {
		var groundedIdx []int
		for _, s := range samples {
			if s.Corroboration == CorroborationGrounded {
				groundedIdx = append(groundedIdx, s.SampleIndex)
			}
		}
		if len(groundedIdx) > 0 {
			tv.Triggers = append(tv.Triggers, Trigger{Type: "grounded_pass", SampleIndexes: groundedIdx})
			cards = append(cards, PendingCard{TargetID: r.ID, Trigger: "grounded_pass",
				ItemText: textOf(deciding), Quotes: quotesOf(deciding), Granularity: tv.Granularity,
				Note: fmt.Sprintf("HIGH pass with grounding-only corroboration on samples %v (≥1 anchor hit + precision, under the recall bar) — oversight only, pass stands", groundedIdx)})
		}
	}

	provisional := false
	if status == "must_pass" && wouldPass && effectiveAnchors == 0 && !everPassed && deciding != nil {
		k := AdjKey{TargetID: r.ID, Statement: normalizeStatement(deciding.ItemText),
			IDSetHash: idSetHash(deciding.ItemSessionIDs), RubricHash: r.Hash, Trigger: "first_pass_no_anchor"}
		if addKeyed("first_pass_no_anchor", k, PendingCard{Adjudicable: true, Ref: deciding.ItemRef,
			ItemText: deciding.ItemText, Granularity: tv.Granularity, Quotes: deciding.ItemQuotes,
			Note: "first-ever pass of a no-anchor target — no corroboration channel, confirm by recognition"}) != "accept" {
			provisional = true
		}
	}

	ratchet := ((status == "expected_fail" || status == "needs_reconfirmation") && wouldPass) ||
		(status == "expected_partial" && tv.Granularity == "full")
	if ratchet && deciding != nil {
		k := AdjKey{TargetID: r.ID, Statement: normalizeStatement(deciding.ItemText),
			IDSetHash: idSetHash(deciding.ItemSessionIDs), RubricHash: r.Hash, Trigger: "ratchet_candidate"}
		addKeyed("ratchet_candidate", k, PendingCard{Adjudicable: true, Ref: deciding.ItemRef,
			ItemText: deciding.ItemText, Granularity: tv.Granularity, Quotes: deciding.ItemQuotes,
			Note: "ready to ratchet: confirm, then flip the status in benchmark.json (manual edit)"})
	}

	if status == "needs_reconfirmation" {
		tv.Triggers = append(tv.Triggers, Trigger{Type: "needs_reconfirmation"})
		cards = append(cards, PendingCard{TargetID: r.ID, Trigger: "needs_reconfirmation",
			ItemText: textOf(deciding), Granularity: tv.Granularity,
			Note: "pool version bumped: target scores as expected_fail until manually reconfirmed"})
	}

	switch status {
	case "must_pass":
		tv.Pass = wouldPass && !provisional
		tv.ProvisionalFail = wouldPass && provisional
		tv.MeetsExpectation = tv.Pass
		// Hard fail is granularity-based (spec: "HIGH miss = hard fail",
		// miss = granularity below pass_at). A provisional-fail is a
		// would-pass held for confirmation, NOT a miss — it lowers recall
		// but must not turn an improvement into a hard-red suite.
		tv.HardFail = r.Tier == "HIGH" && !wouldPass
	case "expected_partial":
		tv.MeetsExpectation = tv.Granularity != "absent"
		tv.HardFail = tv.Granularity == "absent" // presence regression (spec)
	case "expected_fail", "needs_reconfirmation":
		tv.MeetsExpectation = true // gap progress is informational, never a failure
	}
	return tv, cards
}

func textOf(s *SampleScore) string {
	if s == nil {
		return ""
	}
	return s.ItemText
}

func quotesOf(s *SampleScore) []string {
	if s == nil {
		return nil
	}
	return s.ItemQuotes
}
