package synthesis

import (
	"strings"
	"testing"
)

func TestQuoteGuard(t *testing.T) {
	pool := newQuoteIndex([]string{"try not to duplicate too much existing code", "No bloat please"})
	kept, dropped := pool.filter([]string{
		"try not to duplicate too much existing code", // verbatim → kept
		"this was never said by anyone at all",        // fabricated → dropped
		"short",                                       // below 12-rune floor → dropped
	})
	if len(kept) != 1 || kept[0] != "try not to duplicate too much existing code" {
		t.Errorf("kept = %v, want the one verbatim quote", kept)
	}
	if dropped != 2 {
		t.Errorf("dropped = %d, want 2", dropped)
	}
}

func bundleFixture() EvidenceBundle {
	return EvidenceBundle{
		Repo: "client-project", AnalyzedCount: 3,
		Friction: []FrictionItem{
			{ID: "F1", Type: "wrong_approach", SessionID: "s1", Quote: "investigate the existing pattern first"},
			{ID: "F2", Type: "wrong_approach", SessionID: "s2"},
			{ID: "F3", Type: "buggy_code", SessionID: "s1"},
		},
		Signals: []OppSignal{{ID: "G1", Kind: "high_read", Magnitude: 4, MemberSessions: []string{"s1", "s2", "s3", "s4"}}},
	}
}

func TestValidateCountsPartition(t *testing.T) {
	b := bundleFixture()
	raw := RawSynthesis{Themes: []RawTheme{
		{Title: "Wrong approach", Kind: "friction", EvidenceIDs: []string{"F1", "F2"}, CitedQuotes: []string{"investigate the existing pattern first"}},
		{Title: "Bugs", Kind: "friction", EvidenceIDs: []string{"F3"}, CitedQuotes: nil},
	}}
	themes, unthemed, hard := validateAndCount(b, raw)
	if len(hard) != 0 {
		t.Fatalf("unexpected hard errors: %v", hard)
	}
	if themes[0].IncidentCount != 2 || themes[0].SessionCount != 2 {
		t.Errorf("theme0 counts = inc %d sess %d, want 2/2", themes[0].IncidentCount, themes[0].SessionCount)
	}
	if unthemed != 0 {
		t.Errorf("unthemed = %d, want 0 (F1,F2,F3 all placed)", unthemed)
	}
}

func TestValidateRejectsOverlapAndBadIDs(t *testing.T) {
	b := bundleFixture()
	overlap := RawSynthesis{Themes: []RawTheme{
		{Title: "A", Kind: "friction", EvidenceIDs: []string{"F1"}},
		{Title: "B", Kind: "friction", EvidenceIDs: []string{"F1"}}, // F1 in two friction themes
	}}
	if _, _, hard := validateAndCount(b, overlap); len(hard) == 0 {
		t.Error("expected hard error on F-id overlap")
	}
	badID := RawSynthesis{Themes: []RawTheme{{Title: "A", Kind: "friction", EvidenceIDs: []string{"F99"}}}}
	if _, _, hard := validateAndCount(b, badID); len(hard) == 0 {
		t.Error("expected hard error on out-of-range id")
	}
	wrongKind := RawSynthesis{Themes: []RawTheme{{Title: "A", Kind: "friction", EvidenceIDs: []string{"G1"}}}}
	if _, _, hard := validateAndCount(b, wrongKind); len(hard) == 0 {
		t.Error("expected hard error: friction theme referencing a G id")
	}
	orphan := RawSynthesis{Themes: []RawTheme{{Title: "A", Kind: "friction", EvidenceIDs: []string{"F1"}}}}
	_, unthemed, _ := validateAndCount(b, orphan)
	if unthemed != 2 {
		t.Errorf("unthemed = %d, want 2 (F2,F3 unplaced)", unthemed)
	}
}

func TestValidateRejectsNumberInThemeTitle(t *testing.T) {
	b := bundleFixture()
	raw := RawSynthesis{Themes: []RawTheme{
		{Title: "Wrong approach in 40% of sessions", Kind: "friction", EvidenceIDs: []string{"F1"}},
	}}
	_, _, hard := validateAndCount(b, raw)
	found := false
	for _, h := range hard {
		if strings.Contains(h, "theme title contains a number") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected hard error for numeric claim in theme title, got %v", hard)
	}
}

func TestValidateFrictionOverGeneralized(t *testing.T) {
	b := EvidenceBundle{Friction: []FrictionItem{
		{ID: "F1", Type: "wrong_approach", SessionID: "s1"},
		{ID: "F2", Type: "buggy_code", SessionID: "s2"},
		{ID: "F3", Type: "missing_context", SessionID: "s3"},
	}}
	raw := RawSynthesis{Themes: []RawTheme{{Title: "Grab bag", Kind: "friction", EvidenceIDs: []string{"F1", "F2", "F3"}}}}
	themes, _, hard := validateAndCount(b, raw)
	if len(hard) != 0 {
		t.Fatalf("unexpected hard errors: %v", hard)
	}
	if !themes[0].OverGeneralized {
		t.Error("expected OverGeneralized = true for a theme spanning 3 friction types")
	}
}

func TestValidateOpportunityThemeAnchoring(t *testing.T) {
	b := bundleFixture()
	b.Success = []SuccessItem{
		{ID: "S1", SessionID: "s1"}, {ID: "S2", SessionID: "s2"},
		{ID: "S3", SessionID: "s3"}, {ID: "S4", SessionID: "s4"},
	}

	anchoredOnG := RawSynthesis{Themes: []RawTheme{
		{Title: "Read-heavy", Kind: "opportunity", SignalRefs: []string{"G1"}, EvidenceIDs: []string{"S1"}},
	}}
	themes, _, hard := validateAndCount(b, anchoredOnG)
	if len(hard) != 0 {
		t.Fatalf("unexpected hard errors for G-anchored theme: %v", hard)
	}
	if len(themes[0].SignalRefs) != 1 || themes[0].SignalRefs[0] != "G1" {
		t.Errorf("SignalRefs = %v, want [G1]", themes[0].SignalRefs)
	}

	anchoredOn4S := RawSynthesis{Themes: []RawTheme{
		{Title: "Recurring success", Kind: "opportunity", EvidenceIDs: []string{"S1", "S2", "S3", "S4"}},
	}}
	if _, _, hard := validateAndCount(b, anchoredOn4S); len(hard) != 0 {
		t.Errorf("unexpected hard errors for 4-success-anchored theme: %v", hard)
	}

	underAnchored := RawSynthesis{Themes: []RawTheme{
		{Title: "Weak", Kind: "opportunity", EvidenceIDs: []string{"S1", "S2"}},
	}}
	if _, _, hard := validateAndCount(b, underAnchored); len(hard) == 0 {
		t.Error("expected hard error: opportunity theme with no G signal and < 4 success anchors")
	}
}

func TestValidateFrictionCountsDistinctValidIDs(t *testing.T) {
	b := bundleFixture()
	raw := RawSynthesis{Themes: []RawTheme{
		{Title: "Dup", Kind: "friction", EvidenceIDs: []string{"F1", "F1", "F2", "F99"}},
	}}
	themes, _, hard := validateAndCount(b, raw)
	if themes[0].IncidentCount != 2 {
		t.Errorf("IncidentCount = %d, want 2 (distinct valid ids F1,F2)", themes[0].IncidentCount)
	}
	foundOutOfRange, foundPartition := false, false
	for _, h := range hard {
		if strings.Contains(h, "out-of-range id F99") {
			foundOutOfRange = true
		}
		if strings.Contains(h, "partition violated") {
			foundPartition = true
		}
	}
	if !foundOutOfRange {
		t.Errorf("expected hard error for out-of-range id F99, got %v", hard)
	}
	if foundPartition {
		t.Errorf("did not expect partition-violated error from a duplicate id within one theme, got %v", hard)
	}
}

func TestValidateOpportunityRejectsDuplicateS(t *testing.T) {
	b := bundleFixture()
	b.Success = []SuccessItem{{ID: "S1", SessionID: "s1"}}
	raw := RawSynthesis{Themes: []RawTheme{
		{Title: "Fake anchors", Kind: "opportunity", SignalRefs: nil, EvidenceIDs: []string{"S1", "S1", "S1", "S1"}},
	}}
	_, _, hard := validateAndCount(b, raw)
	if len(hard) == 0 {
		t.Error("expected hard error: duplicate S id must not satisfy the >= 4 distinct success anchors requirement")
	}
}

func TestQuoteGuardNormalizedWhitespace(t *testing.T) {
	qi := newQuoteIndex([]string{"investigate the existing pattern first"})
	kept, dropped := qi.filter([]string{"investigate  the   existing   pattern first"})
	if len(kept) != 1 {
		t.Errorf("kept = %v, want the whitespace-variant quote kept via normalized match", kept)
	}
	if dropped != 0 {
		t.Errorf("dropped = %d, want 0", dropped)
	}
}

func TestHasQuantitativeClaim(t *testing.T) {
	cases := []struct {
		s    string
		want bool
	}{
		{"seen in 50% of sessions", true},
		{"happened in 12 sessions this week", true},
		{"occurred 3 times", true},
		{"3 of 5 sessions hit this", true},
		{"a recurring pattern across sessions", false},
		{"F1 and F2 both show this", false},
	}
	for _, c := range cases {
		if got := hasQuantitativeClaim(c.s); got != c.want {
			t.Errorf("hasQuantitativeClaim(%q) = %v, want %v", c.s, got, c.want)
		}
	}
}
