package synthesis

import (
	"os"
	"regexp"
	"strings"
)

// The verifier's text-level guards, kept together because they share one
// property: each decides whether a piece of model-authored text may reach the
// stored snapshot at all. VerifyGlobal is their only caller.

// minQuoteRunes is the shortest text the quote index will accept as a match. A
// fragment shorter than this appears verbatim in almost any pool by accident,
// so matching it would verify nothing.
const minQuoteRunes = 12

// quoteIndex is a cited item's quote pool, held both verbatim and
// whitespace-normalized so a re-wrapped copy of a real quote is not treated as
// invented.
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

// numberClaim matches the quantitative shapes a model can only be guessing at:
// Go owns every count describing the evidence.
var numberClaim = regexp.MustCompile(`\d+\s*%|\b\d+\s+(sessions?|times?|incidents?)\b|\b\d+\s+of\s+\d+\b`)

func hasQuantitativeClaim(s string) bool { return numberClaim.MatchString(s) }

// validAudiences is the audience enum a finding may declare.
var validAudiences = map[string]bool{
	"user": true, "orchestrator": true, "subagents": true, "both": true, "automation": true,
}

// leakPatterns is the blocking privacy scan: a snapshot is written to the
// store and read by the TUI, so a real home path, a session UUID, or a ticket
// marker in free text blocks the write outright (see verifier.scanPrivacy).
var leakPatterns = []*regexp.Regexp{
	regexp.MustCompile(`/Users/`),
	regexp.MustCompile(`\$HOME`),
	regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`), // session UUID
	regexp.MustCompile(`\bsc-\d+\b`), // Shortcut ticket
}

// tildeHome rewrites the user's home directory (literal or $HOME) to "~" in s.
// Every string that outlives the process gets this: the stored snapshot's
// paths, the verifier's notes, and the CLI-authored error text that lands in
// the run state and the TUI's error badge.
func tildeHome(s string) string {
	home, _ := os.UserHomeDir()
	return tildeWithHome(s, home)
}

// tildeWithHome is tildeHome against an explicit home, for callers that
// resolved it once (the verifier) or that must degrade to a no-op when the home
// directory is undeterminable.
func tildeWithHome(s, home string) string {
	if home != "" {
		s = strings.ReplaceAll(s, home, "~")
	}
	return strings.ReplaceAll(s, "$HOME", "~")
}
