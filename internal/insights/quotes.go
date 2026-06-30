package insights

// minQuoteRunes is the floor below which an evidence quote is treated as
// unverifiable: VerbatimIndex uses plain substring matching, so a trivially short
// quote would match almost any corpus. 12 is below the shortest validated real
// preference ("No bloat please" = 15 runes).
const minQuoteRunes = 12

func longEnough(quote string) bool {
	return len([]rune(quote)) >= minQuoteRunes
}

// validateQuotes applies the anti-fabrication policy. Friction quotes are checked
// against the full corpus and flagged-and-cleared when not verbatim (the incident
// survives — a quoteless incident is schema-valid). Preference quotes are checked
// against the user corpus and the whole preference dropped when not verbatim (a
// preference without the user's own words violates its contract).
func validateQuotes(j JudgedFields, vi VerbatimIndex) JudgedFields {
	out := j

	fr := make([]FrictionIncident, 0, len(j.FrictionIncidents))
	for _, inc := range j.FrictionIncidents {
		switch {
		case inc.EvidenceQuote == "":
			// no quote to validate; keep as-is
		case longEnough(inc.EvidenceQuote) &&
			(vi.ContainsAny(inc.EvidenceQuote) || vi.ContainsAnyNormalized(inc.EvidenceQuote)):
			// verbatim; keep
		default:
			inc.EvidenceQuote = ""
			inc.QuoteUnverified = true
		}
		fr = append(fr, inc)
	}
	out.FrictionIncidents = fr

	pr := make([]StandingPreference, 0, len(j.StandingPreferences))
	for _, p := range j.StandingPreferences {
		if longEnough(p.EvidenceQuote) &&
			(vi.ContainsUser(p.EvidenceQuote) || vi.ContainsUserNormalized(p.EvidenceQuote)) {
			pr = append(pr, p)
		}
	}
	out.StandingPreferences = pr

	return out
}
