package insights

import (
	"regexp"
	"strings"
	"unicode"
)

// DirectiveClause is one distinct normalized user clause in a session (Phase-3
// detector 2, facts tier). Exemplar is the first-seen sanitized raw clause —
// the bundle Detail / quote-index source. Counts are internal (never rendered
// into Detail).
type DirectiveClause struct {
	Norm      string `json:"norm"`
	Exemplar  string `json:"exemplar"`
	Count     int    `json:"count"`
	FirstTurn int    `json:"first_turn"`
}

var (
	pastedTimestampRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[T ]`)
	anyLetterRE       = regexp.MustCompile(`[a-zA-Z]`)
)

// extractClauses applies the pinned Phase-3 rules to one user prose turn:
// fences/log-lines/image-placeholders out, newline + sentence-terminator
// split, triviality filter (>= 4 tokens and >= 16 runes), sanitized output.
func extractClauses(turn string) []string {
	var out []string
	inFence := false
	for _, line := range strings.Split(turn, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") {
			inFence = !inFence
			continue
		}
		if inFence || t == "" || pastedTimestampRE.MatchString(t) ||
			!anyLetterRE.MatchString(t) || strings.HasPrefix(t, "[image:") {
			continue
		}
		for _, clause := range splitClauses(t) {
			c := SanitizeEvidenceText(clause)
			norm := normalizeClause(c)
			if len(ClauseTokens(norm)) < 4 || len([]rune(norm)) < 16 {
				continue
			}
			out = append(out, c)
		}
	}
	return out
}

func splitClauses(line string) []string {
	var out []string
	runes := []rune(line)
	start := 0
	for i, r := range runes {
		if (r == '.' || r == '!' || r == '?') && i+1 < len(runes) && unicode.IsSpace(runes[i+1]) {
			if c := strings.TrimSpace(string(runes[start : i+1])); c != "" {
				out = append(out, c)
			}
			start = i + 1
		}
	}
	if c := strings.TrimSpace(string(runes[start:])); c != "" {
		out = append(out, c)
	}
	return out
}

func normalizeClause(c string) string {
	n := strings.Join(strings.Fields(strings.ToLower(c)), " ")
	return strings.TrimRight(n, ".!?;:, ")
}

// ClauseTokens keeps interior punctuation ("as-is" is one token) — only token
// edges are trimmed. Exported: bundle-time clustering must tokenize
// identically to the facts tier.
func ClauseTokens(norm string) []string {
	var out []string
	for _, f := range strings.Fields(norm) {
		f = strings.TrimFunc(f, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}
