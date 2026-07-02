package synthesis

import (
	"regexp"
	"strings"
)

const minQuoteRunes = 12

type quoteIndex struct{ exact, norm string }

func newQuoteIndex(quotes []string) quoteIndex {
	var ex, nm strings.Builder
	for _, q := range quotes {
		ex.WriteString(q)
		ex.WriteByte('\n')
		nm.WriteString(normalizeWS(q))
		nm.WriteByte('\n')
	}
	return quoteIndex{exact: ex.String(), norm: nm.String()}
}

func (qi quoteIndex) contains(q string) bool {
	if len([]rune(q)) < minQuoteRunes {
		return false
	}
	if strings.Contains(qi.exact, q) {
		return true
	}
	return strings.Contains(qi.norm, normalizeWS(q))
}

// filter returns the kept quotes and the count dropped (non-verbatim or too short).
func (qi quoteIndex) filter(quotes []string) (kept []string, dropped int) {
	for _, q := range quotes {
		if qi.contains(q) {
			kept = append(kept, q)
		} else {
			dropped++
		}
	}
	return kept, dropped
}

func normalizeWS(s string) string { return strings.Join(strings.Fields(s), " ") }

var numberClaim = regexp.MustCompile(`\d+\s*%|\b\d+\s+(sessions?|times?|incidents?)\b|\b\d+\s+of\s+\d+\b`)

func hasQuantitativeClaim(s string) bool { return numberClaim.MatchString(s) }

// validateAndCount enforces id validity + F-partition and computes friction theme
// counts. Returns computed friction/opportunity themes, the unthemed-friction count,
// and any hard validation errors (surfaced, not silently dropped).
func validateAndCount(b EvidenceBundle, raw RawSynthesis) (themes []Theme, unthemed int, hard []string) {
	fByID := map[string]FrictionItem{}
	for _, f := range b.Friction {
		fByID[f.ID] = f
	}
	sByID := map[string]bool{}
	for _, s := range b.Success {
		sByID[s.ID] = true
	}
	gByID := map[string]bool{}
	for _, g := range b.Signals {
		gByID[g.ID] = true
	}

	usedF := map[string]int{} // F id -> count of friction themes referencing it
	for _, rt := range raw.Themes {
		th := Theme{Title: rt.Title, Kind: rt.Kind, Summary: rt.Summary}
		if hasQuantitativeClaim(rt.Summary) {
			hard = append(hard, "theme summary contains a number: "+rt.Title)
		}
		if hasQuantitativeClaim(rt.Title) {
			hard = append(hard, "theme title contains a number: "+rt.Title)
		}
		switch rt.Kind {
		case "friction":
			breakdown := map[string]int{}
			sessions := map[string]bool{}
			seen := map[string]bool{}
			for _, id := range rt.EvidenceIDs {
				f, ok := fByID[id]
				if !ok {
					hard = append(hard, "friction theme "+rt.Title+" references non-friction/out-of-range id "+id)
					continue
				}
				if seen[id] {
					continue
				}
				seen[id] = true
				usedF[id]++
				breakdown[f.Type]++
				sessions[f.SessionID] = true
				th.SessionIDs = append(th.SessionIDs, f.SessionID)
			}
			th.IncidentCount = len(seen)
			th.SessionCount = len(sessions)
			th.TypeBreakdown = breakdown
			th.OverGeneralized = len(breakdown) > 2
		case "opportunity":
			th.SignalRefs = rt.SignalRefs
			hasG := false
			for _, id := range rt.SignalRefs {
				if gByID[id] {
					hasG = true
				} else {
					hard = append(hard, "opportunity theme "+rt.Title+" references out-of-range signal "+id)
				}
			}
			sSeen := map[string]bool{}
			sessions := map[string]bool{}
			for _, id := range rt.EvidenceIDs {
				if sByID[id] {
					sSeen[id] = true
				} else if _, ok := fByID[id]; !ok {
					hard = append(hard, "opportunity theme "+rt.Title+" references out-of-range id "+id)
				}
			}
			for _, s := range b.Success { // record session ids for the referenced S items
				for _, id := range rt.EvidenceIDs {
					if s.ID == id {
						sessions[s.SessionID] = true
						th.SessionIDs = append(th.SessionIDs, s.SessionID)
					}
				}
			}
			if !hasG && len(sSeen) < 4 {
				hard = append(hard, "opportunity theme "+rt.Title+" has no G signal and < 4 success anchors")
			}
			th.SessionCount = len(sessions)
		default:
			hard = append(hard, "unknown theme kind: "+rt.Kind)
		}
		themes = append(themes, th)
	}
	for id, n := range usedF {
		if n > 1 {
			hard = append(hard, "friction id "+id+" appears in multiple friction themes (partition violated)")
		}
	}
	for _, f := range b.Friction {
		if usedF[f.ID] == 0 {
			unthemed++
		}
	}
	return themes, unthemed, hard
}
