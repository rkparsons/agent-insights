package insights

import "testing"

// buildVI builds a VerbatimIndex: userText lands in BOTH corpora (as user prose),
// claudeText lands in the full corpus only (as assistant prose).
func buildVI(userText, claudeText string) VerbatimIndex {
	var vb verbatimBuilder
	vb.addUser(userText)
	vb.addAll(claudeText)
	return vb.finish()
}

func TestValidateFriction(t *testing.T) {
	vi := buildVI("please keep the diff small and focused", "I changed the whole module structure here")
	in := JudgedFields{FrictionIncidents: []FrictionIncident{
		{Type: "excessive_changes", OneLine: "rewrote too much", EvidenceQuote: "changed the whole module structure"}, // in full corpus, >=12
		{Type: "wrong_approach", OneLine: "bad take", EvidenceQuote: "a paraphrase never said anywhere"},              // not in corpus
		{Type: "incomplete", OneLine: "left work", EvidenceQuote: ""},                                                 // no quote
		{Type: "buggy_code", OneLine: "tiny", EvidenceQuote: "the bug"},                                               // too short (<12)
	}}
	out := validateQuotes(in, vi)
	if len(out.FrictionIncidents) != 4 {
		t.Fatalf("friction count = %d, want 4 (none dropped)", len(out.FrictionIncidents))
	}
	if out.FrictionIncidents[0].EvidenceQuote == "" || out.FrictionIncidents[0].QuoteUnverified {
		t.Error("verbatim friction quote should be kept")
	}
	if out.FrictionIncidents[1].EvidenceQuote != "" || !out.FrictionIncidents[1].QuoteUnverified {
		t.Error("paraphrase friction quote should be cleared + flagged")
	}
	if out.FrictionIncidents[2].QuoteUnverified {
		t.Error("no-quote incident should not be flagged")
	}
	if out.FrictionIncidents[3].EvidenceQuote != "" || !out.FrictionIncidents[3].QuoteUnverified {
		t.Error("too-short friction quote should be cleared + flagged")
	}
}

func TestValidatePreferences(t *testing.T) {
	vi := buildVI("always follow the existing conventions in the package", "let me follow my own different idea instead")
	in := JudgedFields{StandingPreferences: []StandingPreference{
		{Rule: "follow conventions", EvidenceQuote: "follow the existing conventions"}, // in USER corpus, >=12
		{Rule: "claude words", EvidenceQuote: "follow my own different idea"},          // in FULL only (claude), not user
		{Rule: "paraphrase", EvidenceQuote: "be consistent with the codebase always"},  // not in corpus
		{Rule: "short", EvidenceQuote: "do it"},                                        // too short
	}}
	out := validateQuotes(in, vi)
	if len(out.StandingPreferences) != 1 {
		t.Fatalf("pref count = %d, want 1 (only the verbatim user one kept)", len(out.StandingPreferences))
	}
	if out.StandingPreferences[0].Rule != "follow conventions" {
		t.Errorf("wrong preference survived: %+v", out.StandingPreferences[0])
	}
}

func TestValidateNormalizedFallbackAndEmpty(t *testing.T) {
	// Corpus has irregular whitespace; the quote is the same words single-spaced.
	vi := buildVI("keep   the\tchanges   minimal please", "")
	in := JudgedFields{
		FrictionIncidents: []FrictionIncident{
			{Type: "excessive_changes", OneLine: "x", EvidenceQuote: "keep the changes minimal"},
		},
		StandingPreferences: []StandingPreference{},
	}
	out := validateQuotes(in, vi)
	if out.FrictionIncidents[0].QuoteUnverified {
		t.Error("reflowed-whitespace quote should verify via normalized fallback")
	}
	// Empty preferences must remain a non-nil slice ([] not null).
	if out.StandingPreferences == nil {
		t.Error("empty preferences should be non-nil slice")
	}
}
