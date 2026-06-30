package insights

import (
	"fmt"
	"math"
)

type SoftFloorResult struct {
	Name   string  `json:"name"`
	Value  float64 `json:"value"`
	Target float64 `json:"target"`
	Pass   bool    `json:"pass"`
}

// EvalReport is the full provisional gate result: per-session scores, aggregate
// metrics, hard-axis verdict (meta-exempt), soft floors, the contested cards, and
// the notional spend. Auto-PASS means "no producer bug + within tolerance"; the
// quality decision is finalized in the human recognition pass.
type EvalReport struct {
	Sessions []SessionScore `json:"sessions"`
	Cards    []Card         `json:"cards"`

	SchemaValidPct      float64 `json:"schema_valid_pct"`
	SurvivorOKPct       float64 `json:"survivor_ok_pct"`
	RawFabricationRate  float64 `json:"raw_fabrication_rate"`
	ValidationFiredRate float64 `json:"validation_fired_rate"`
	MeanTypeJaccard     float64 `json:"mean_type_jaccard"`

	FalseFrictionCandidates int `json:"false_friction_candidates"`
	RecallCandidates        int `json:"recall_candidates"`

	HardFail        bool              `json:"hard_fail"`
	HardFailReasons []string          `json:"hard_fail_reasons"`
	MetaFindings    []string          `json:"meta_findings"`
	SoftFloors      []SoftFloorResult `json:"soft_floors"`

	Calls            int     `json:"calls"`
	NotionalSpendUSD float64 `json:"notional_spend_usd"`
	DetectableEffect string  `json:"detectable_effect"`
}

// notionalPerCallUSD is the spike's observed Opus cost/call (subscription, not
// billed). Used only to report a notional spend estimate.
const notionalPerCallUSD = 0.114

func assembleReport(runs []sessionRun) EvalReport {
	var rep EvalReport
	schemaValidRepeats, totalRepeats, survivorOKSessions := 0, 0, 0
	rawQuotes, rawFab, fired, produced, cleanRuns := 0, 0, 0, 0, 0
	var jaccards []float64

	for _, sr := range runs {
		sc := scoreSession(sr)
		ok, reasons := contested(sr, sc)
		sc.Contested = ok
		sc.ContestedReasons = reasons
		rep.Sessions = append(rep.Sessions, sc)
		rep.Cards = append(rep.Cards, buildCards(sr, sc)...)

		for _, rr := range sr.Repeats {
			totalRepeats++
			if schemaValid(rr.Validated) {
				schemaValidRepeats++
			}
		}
		if sc.SurvivorOK {
			survivorOKSessions++
		}
		rawQuotes += sc.RawQuotes
		rawFab += sc.RawFabricated
		fired += sc.ValidationFired
		produced += sc.ProducedItems
		jaccards = append(jaccards, sc.TypeJaccard)
		if sc.FalseFriction {
			rep.FalseFrictionCandidates++
		}
		if sc.RecallMiss {
			rep.RecallCandidates++
		}
		if sr.ZeroFriction {
			cleanRuns += len(sr.Repeats)
		}

		title := sr.Stats.AiTitle
		if !sc.SchemaValid {
			addHardOrMeta(&rep, sc.IsMeta, title+": schema invalid")
		}
		if !sc.SurvivorOK {
			addHardOrMeta(&rep, sc.IsMeta, title+": surviving quote not verbatim (producer bug)")
		}
		if sc.TwoClassJump {
			addHardOrMeta(&rep, sc.IsMeta, title+": 2-class outcome jump")
		}
	}

	n := len(runs)
	rep.SchemaValidPct = ratio(schemaValidRepeats, totalRepeats)
	rep.SurvivorOKPct = ratio(survivorOKSessions, n)
	rep.RawFabricationRate = rate(rawFab, rawQuotes)
	rep.ValidationFiredRate = rate(fired, produced)
	rep.MeanTypeJaccard = mean(jaccards)
	rep.Calls = totalRepeats
	rep.NotionalSpendUSD = float64(totalRepeats) * notionalPerCallUSD
	rep.DetectableEffect = detectableEffect(cleanRuns)
	rep.SoftFloors = []SoftFloorResult{
		{"raw_fabrication ≤2%", rep.RawFabricationRate, 0.02, rep.RawFabricationRate <= 0.02},
		{"mean_type_jaccard ≥0.6", rep.MeanTypeJaccard, 0.6, rep.MeanTypeJaccard >= 0.6},
		{"validation_fired ≤5%", rep.ValidationFiredRate, 0.05, rep.ValidationFiredRate <= 0.05},
		{"modal_outcome_majority (all)", boolToF(allModalMajority(rep.Sessions)), 1, allModalMajority(rep.Sessions)},
	}
	rep.HardFail = len(rep.HardFailReasons) > 0
	return rep
}

func addHardOrMeta(rep *EvalReport, isMeta bool, msg string) {
	if isMeta {
		rep.MetaFindings = append(rep.MetaFindings, msg)
		return
	}
	rep.HardFailReasons = append(rep.HardFailReasons, msg)
}

// Verdict renders the provisional gate result. PASS is always provisional on the
// human recognition pass (false-friction/recall/borderline adjudication).
func (r EvalReport) Verdict() string {
	if r.HardFail {
		return fmt.Sprintf("FAIL (hard axes): %v", r.HardFailReasons)
	}
	return fmt.Sprintf("PASS (provisional — %d contested cards pending human adjudication)", len(r.Cards))
}

func allModalMajority(scores []SessionScore) bool {
	for _, s := range scores {
		if s.NumRepeats > 0 && s.ModalOutcomeCount*2 <= s.NumRepeats {
			return false
		}
	}
	return true
}

func detectableEffect(cleanRuns int) string {
	p5 := 1 - math.Pow(0.95, float64(cleanRuns))
	p1 := 1 - math.Pow(0.99, float64(cleanRuns))
	return fmt.Sprintf("%d clean runs: ~%.0f%% chance to catch a 5%%/run fabrication rate, ~%.0f%% for 1%%", cleanRuns, p5*100, p1*100)
}

// ratio treats 0/0 as 1 — for "valid %" metrics (nothing to check = nothing failed).
func ratio(num, den int) float64 {
	if den == 0 {
		return 1
	}
	return float64(num) / float64(den)
}

// rate treats 0/0 as 0 — for "bad %" metrics (fabrication, validation-fired): nothing
// produced means a 0% bad rate, not 100%.
func rate(num, den int) float64 {
	if den == 0 {
		return 0
	}
	return float64(num) / float64(den)
}

func mean(xs []float64) float64 {
	if len(xs) == 0 {
		return 1
	}
	var sum float64
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

func boolToF(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
