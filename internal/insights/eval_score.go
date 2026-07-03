package insights

var validOutcomes = map[string]bool{
	"fully_achieved": true, "mostly_achieved": true, "partially_achieved": true,
	"not_achieved": true, "unclear": true,
}

var validSessionTypes = map[string]bool{
	"single_task": true, "multi_task": true, "iterative_refinement": true,
	"exploration": true, "quick_question": true,
}

var validFrictionTypes = map[string]bool{
	"wrong_approach": true, "buggy_code": true, "misunderstood_request": true,
	"excessive_changes": true, "user_rejected_action": true, "incomplete": true,
}

// outcomeOrdinal maps definite outcomes to a scale; unclear is off-scale (0,false).
func outcomeOrdinal(o string) (int, bool) {
	switch o {
	case "not_achieved":
		return 1, true
	case "partially_achieved":
		return 2, true
	case "mostly_achieved":
		return 3, true
	case "fully_achieved":
		return 4, true
	}
	return 0, false
}

func schemaValid(j JudgedFields) bool {
	if !validOutcomes[j.Outcome] || !validSessionTypes[j.SessionType] {
		return false
	}
	if j.UnderlyingGoal == "" || j.BriefSummary == "" {
		return false
	}
	for _, f := range j.FrictionIncidents {
		if !validFrictionTypes[f.Type] {
			return false
		}
	}
	for _, p := range j.StandingPreferences {
		if p.Rule == "" || p.EvidenceQuote == "" {
			return false
		}
	}
	return true
}

// SessionScore holds every metric for one session over its repeats.
type SessionScore struct {
	Cell              string
	IsMeta            bool
	NumRepeats        int
	SchemaValid       bool // every repeat schema-valid
	RawQuotes         int  // total raw evidence quotes across repeats
	RawFabricated     int  // non-verbatim raw quotes
	ValidationFired   int  // quote_unverified flags + dropped preferences
	ProducedItems     int  // raw friction + preferences produced
	SurvivorOK        bool // every surviving (validated) quote re-verifies verbatim
	DistinctOutcomes  int
	ModalOutcomeCount int
	TwoClassJump      bool
	FrictionRange     int
	TypeJaccard       float64
	TypeChurn         bool
	FalseFriction     bool
	RecallMiss        bool
	Contested         bool
	ContestedReasons  []string
}

func scoreSession(sr sessionRun) SessionScore {
	s := SessionScore{Cell: sr.Cell, IsMeta: IsMeta(sr.Stats), NumRepeats: len(sr.Repeats), SchemaValid: true, SurvivorOK: true}

	outcomeCounts := map[string]int{}
	var counts []int
	var typeSets []map[string]bool

	for _, rr := range sr.Repeats {
		if !schemaValid(rr.Validated) {
			s.SchemaValid = false
		}
		for _, qc := range rr.RawQuotes {
			s.RawQuotes++
			if !qc.Verbatim {
				s.RawFabricated++
			}
		}
		s.ProducedItems += len(rr.Raw.FrictionIncidents) + len(rr.Raw.StandingPreferences)
		for _, inc := range rr.Validated.FrictionIncidents {
			if inc.QuoteUnverified {
				s.ValidationFired++
			}
			if inc.EvidenceQuote != "" && !inc.QuoteUnverified &&
				!sr.Verbatim.ContainsAny(inc.EvidenceQuote) && !sr.Verbatim.ContainsAnyNormalized(inc.EvidenceQuote) {
				s.SurvivorOK = false
			}
		}
		s.ValidationFired += rr.Report.DroppedPreferences
		for _, p := range rr.Validated.StandingPreferences {
			if !sr.Verbatim.ContainsUser(p.EvidenceQuote) && !sr.Verbatim.ContainsUserNormalized(p.EvidenceQuote) {
				s.SurvivorOK = false
			}
		}
		outcomeCounts[rr.Validated.Outcome]++
		counts = append(counts, len(rr.Validated.FrictionIncidents))
		ts := map[string]bool{}
		for _, inc := range rr.Validated.FrictionIncidents {
			ts[inc.Type] = true
		}
		typeSets = append(typeSets, ts)
	}

	s.DistinctOutcomes = len(outcomeCounts)
	s.ModalOutcomeCount = maxCount(outcomeCounts)
	s.TwoClassJump = twoClassJump(outcomeCounts)
	s.FrictionRange = intRange(counts)
	s.TypeJaccard = meanPairwiseJaccard(typeSets)
	s.TypeChurn = typeChurn(typeSets)
	s.FalseFriction = sr.ZeroFriction && anyFriction(sr)
	s.RecallMiss = sr.Frictionful && zeroFrictionRepeats(sr) >= ceilHalf(len(sr.Repeats))
	return s
}

func twoClassJump(outcomes map[string]int) bool {
	lo, hi := 99, -1
	for o := range outcomes {
		if ord, ok := outcomeOrdinal(o); ok {
			if ord < lo {
				lo = ord
			}
			if ord > hi {
				hi = ord
			}
		}
	}
	return hi >= 0 && hi-lo >= 2
}

func maxCount(m map[string]int) int {
	max := 0
	for _, v := range m {
		if v > max {
			max = v
		}
	}
	return max
}

func intRange(xs []int) int {
	if len(xs) == 0 {
		return 0
	}
	lo, hi := xs[0], xs[0]
	for _, x := range xs[1:] {
		if x < lo {
			lo = x
		}
		if x > hi {
			hi = x
		}
	}
	return hi - lo
}

func jaccard(a, b map[string]bool) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1
	}
	inter := 0
	union := map[string]bool{}
	for k := range a {
		union[k] = true
		if b[k] {
			inter++
		}
	}
	for k := range b {
		union[k] = true
	}
	return float64(inter) / float64(len(union))
}

func meanPairwiseJaccard(sets []map[string]bool) float64 {
	if len(sets) < 2 {
		return 1
	}
	var sum float64
	var n int
	for i := 0; i < len(sets); i++ {
		for j := i + 1; j < len(sets); j++ {
			sum += jaccard(sets[i], sets[j])
			n++
		}
	}
	return sum / float64(n)
}

func typeChurn(sets []map[string]bool) bool {
	union := map[string]bool{}
	for _, s := range sets {
		for k := range s {
			union[k] = true
		}
	}
	for k := range union {
		for _, s := range sets {
			if !s[k] {
				return true
			}
		}
	}
	return false
}

func anyFriction(sr sessionRun) bool {
	for _, rr := range sr.Repeats {
		if len(rr.Validated.FrictionIncidents) > 0 {
			return true
		}
	}
	return false
}

func zeroFrictionRepeats(sr sessionRun) int {
	n := 0
	for _, rr := range sr.Repeats {
		if len(rr.Validated.FrictionIncidents) == 0 {
			n++
		}
	}
	return n
}

func ceilHalf(n int) int { return (n + 1) / 2 }
