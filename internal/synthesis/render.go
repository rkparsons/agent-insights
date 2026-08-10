package synthesis

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

func Render(s RepoSynthesis) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — workflow insights\n\n", s.Repo)
	fmt.Fprintf(&b, "_Window %s–%s · %d analyzed sessions · model %s_\n\n", s.Window.From, s.Window.To, s.Window.AnalyzedCount, s.Meta.Model)

	friction := themesByKind(s.Themes, "friction")
	if len(friction) > 0 {
		b.WriteString("## Top friction themes\n\n")
		for _, t := range friction {
			fmt.Fprintf(&b, "%d. **%s** — %d incidents across %d sessions. %s\n", t.Rank, redactNumbers(t.Title), t.IncidentCount, t.SessionCount, redactNumbers(t.Summary))
		}
		b.WriteString("\n")
	}
	if s.Meta.UnthemedFriction > 0 {
		fmt.Fprintf(&b, "_Per-theme session counts overlap and do not sum to the total; %d friction incidents are unthemed._\n\n", s.Meta.UnthemedFriction)
	}
	opps := themesByKind(s.Themes, "opportunity")
	if len(opps) > 0 {
		b.WriteString("## Workflow opportunities\n\n")
		for _, t := range opps {
			fmt.Fprintf(&b, "- **%s** (%d sessions). %s\n", redactNumbers(t.Title), t.SessionCount, redactNumbers(t.Summary))
		}
		b.WriteString("\n")
	}
	newRecs, adopted := splitAdopted(s.Recommendations)
	if len(newRecs) > 0 {
		b.WriteString("## Recommendations\n\n")
		for _, r := range newRecs {
			if r.Title != "" {
				fmt.Fprintf(&b, "- `[%s]` **%s** — %s (evidence: %d sessions)\n", r.Type, redactNumbers(r.Title), redactNumbers(r.Statement), r.SessionCount)
			} else {
				fmt.Fprintf(&b, "- `[%s]` %s (evidence: %d sessions)\n", r.Type, redactNumbers(r.Statement), r.SessionCount)
			}
		}
		b.WriteString("\n")
	}
	if len(adopted) > 0 {
		b.WriteString("## Already in place (reinforce?)\n\n")
		for _, r := range adopted {
			if r.Title != "" {
				fmt.Fprintf(&b, "- `[%s]` **%s** — %s\n", r.Type, redactNumbers(r.Title), redactNumbers(r.Statement))
			} else {
				fmt.Fprintf(&b, "- `[%s]` %s\n", r.Type, redactNumbers(r.Statement))
			}
		}
		b.WriteString("\n")
	}
	if len(s.Meta.ValidationErrors) > 0 {
		b.WriteString("## Validation warnings\n\n")
		for _, e := range s.Meta.ValidationErrors {
			fmt.Fprintf(&b, "- %s\n", redactNumbers(e))
		}
	}
	return b.String()
}

// redactNumbers strips LLM-authored quantitative claims (the same numberClaim
// pattern hasQuantitativeClaim flags in verify.go) from any string the render
// interpolates verbatim from the LLM. Go-computed integer fields are never routed
// through this — they render as-is.
func redactNumbers(s string) string {
	return numberClaim.ReplaceAllString(s, "[redacted]")
}

func themesByKind(ts []Theme, kind string) []Theme {
	var out []Theme
	for _, t := range ts {
		if t.Kind == kind {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Rank < out[j].Rank })
	return out
}

func splitAdopted(rs []Recommendation) (fresh, adopted []Recommendation) {
	for _, r := range rs {
		if r.AlreadyAdopted == "yes" {
			adopted = append(adopted, r)
		} else {
			fresh = append(fresh, r)
		}
	}
	return
}

var leakPatterns = []*regexp.Regexp{
	regexp.MustCompile(`/Users/`),
	regexp.MustCompile(`\$HOME`),
	regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`), // session UUID
	regexp.MustCompile(`\bsc-\d+\b`), // Shortcut ticket
}

func scanReport(md string) []string {
	var leaks []string
	for _, re := range leakPatterns {
		if loc := re.FindString(md); loc != "" {
			leaks = append(leaks, loc)
		}
	}
	return leaks
}
