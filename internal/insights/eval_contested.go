package insights

import (
	"fmt"
	"strings"
)

// Card is a recognition card for the human pass: identifiable by the session title
// and the user's own opening words, NEVER by session-id or transcript body.
type Card struct {
	Title           string `json:"title"`
	Opening         string `json:"opening"`
	Claim           string `json:"claim"`
	Quote           string `json:"quote,omitempty"`
	ContestedReason string `json:"contested_reason"`
}

const genericQuoteMaxRunes = 20

func multiMatch(quote string, vi VerbatimIndex) bool {
	return quote != "" && strings.Count(vi.allExact, quote) > 1
}

func borderlineOutcome(sr sessionRun) bool {
	mostly, partially := false, false
	for _, rr := range sr.Repeats {
		switch rr.Validated.Outcome {
		case "unclear":
			return true
		case "mostly_achieved":
			mostly = true
		case "partially_achieved":
			partially = true
		}
	}
	return mostly && partially
}

func genericSurvivingQuote(sr sessionRun) bool {
	short := func(q string) bool {
		return q != "" && len([]rune(q)) < genericQuoteMaxRunes && multiMatch(q, sr.Verbatim)
	}
	for _, rr := range sr.Repeats {
		for _, inc := range rr.Validated.FrictionIncidents {
			if short(inc.EvidenceQuote) {
				return true
			}
		}
		for _, p := range rr.Validated.StandingPreferences {
			if short(p.EvidenceQuote) {
				return true
			}
		}
	}
	return false
}

// contested decides whether a session needs human adjudication and lists why.
func contested(sr sessionRun, sc SessionScore) (bool, []string) {
	var reasons []string
	if sc.DistinctOutcomes > 1 || sc.TypeChurn || sc.FrictionRange >= 2 {
		reasons = append(reasons, "run-to-run disagreement")
	}
	if borderlineOutcome(sr) {
		reasons = append(reasons, "borderline outcome")
	}
	if genericSurvivingQuote(sr) {
		reasons = append(reasons, "fabrication-pressure quote")
	}
	if sc.FalseFriction {
		reasons = append(reasons, "false-friction candidate")
	}
	if sc.RecallMiss {
		reasons = append(reasons, "recall candidate")
	}
	return len(reasons) > 0, reasons
}

// buildCards renders one card per contested claim, grouped by session. Returns nil
// for an uncontested session, and for a meta/insights-dev session: it still folds
// into the metrics (F3), but its cards are withheld from the human pass (empty title
// + structured-output-as-friction are low recognition value) — display-only.
func buildCards(sr sessionRun, sc SessionScore) []Card {
	if sc.IsMeta {
		return nil
	}
	ok, reasons := contested(sr, sc)
	if !ok {
		return nil
	}
	reason := strings.Join(reasons, "; ")
	title, open := sr.Stats.AiTitle, sr.FirstUserTurn
	var cards []Card

	if sc.DistinctOutcomes > 1 || borderlineOutcome(sr) {
		var os []string
		for i, rr := range sr.Repeats {
			os = append(os, fmt.Sprintf("r%d=%s", i+1, rr.Validated.Outcome))
		}
		cards = append(cards, Card{Title: title, Opening: open, Claim: "Outcome across repeats: " + strings.Join(os, ", "), ContestedReason: reason})
	}

	// Dedup at type level, not exact (type, one_line): the model rephrases one_line
	// each repeat, so exact-keying lets every rephrasing of the same incident spawn a
	// card (one session produced ~20). One representative card per friction type keeps
	// the human pass tractable and matches the Jaccard stability metric's granularity.
	seen := map[string]bool{}
	for _, rr := range sr.Repeats {
		for _, inc := range rr.Validated.FrictionIncidents {
			if seen[inc.Type] {
				continue
			}
			seen[inc.Type] = true
			cards = append(cards, Card{Title: title, Opening: open, Claim: fmt.Sprintf("Friction [%s]: %s", inc.Type, inc.OneLine), Quote: inc.EvidenceQuote, ContestedReason: reason})
		}
	}

	if sc.RecallMiss {
		cards = append(cards, Card{Title: title, Opening: open, Claim: fmt.Sprintf(
			"Deterministic friction present (tool_errors=%d interrupts=%d rejections=%d) but %d/%d repeats reported zero friction",
			sr.Stats.ToolErrors, sr.Stats.Interrupts, sr.Stats.Rejections, zeroFrictionRepeats(sr), len(sr.Repeats)), ContestedReason: reason})
	}
	return cards
}
