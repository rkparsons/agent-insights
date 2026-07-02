package synthesis

import "fmt"

type Card struct {
	Repo, ThemeTitle string
	MatchTotal       string
	Quotes           []string
	ProposedRec      string
}

// Cards builds human-recognition cards for adjudication — no session-ids or
// transcripts, only the Go match/total count, verified quotes, and the tied
// recommendation, so a reviewer can confirm the theme is real without re-deriving it.
func Cards(s RepoSynthesis, bundle EvidenceBundle) []Card {
	var cards []Card
	for ti := range s.Themes {
		t := s.Themes[ti]
		if t.SessionCount == 0 {
			continue
		}
		q := t.Quotes
		if len(q) > 2 {
			q = q[:2]
		}
		match := fmt.Sprintf("%d of %d sessions", t.SessionCount, bundle.AnalyzedCount)
		rec := ""
		for _, r := range s.Recommendations {
			for _, ref := range r.ThemeRefs {
				if ref == ti {
					rec = r.Statement
				}
			}
		}
		cards = append(cards, Card{Repo: s.Repo, ThemeTitle: t.Title, MatchTotal: match, Quotes: q, ProposedRec: rec})
	}
	return cards
}
