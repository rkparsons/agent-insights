package synthesis

import (
	"context"
	"sort"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
)

type ValidationReport struct {
	RawQuoteDropRate float64
	HardErrors       []string
}

var validAudiences = map[string]bool{
	"user": true, "orchestrator": true, "subagents": true, "both": true, "automation": true,
}

func Synthesize(ctx context.Context, repoKey string, group []insights.AgentSessionAnalysis, syn Synthesizer, adopt AdoptChecker) (RepoSynthesis, ValidationReport, error) {
	b := BuildBundle(repoKey, group)
	raw, err := syn.Synthesize(ctx, b)
	if err != nil {
		return RepoSynthesis{}, ValidationReport{}, err
	}
	rs, report := Finalize(repoKey, b, raw, adopt, time.Now().UTC())
	return rs, report, nil
}

// Finalize applies the deterministic post-LLM half of synthesis — id validation
// and counting, the pool-quote guard, ranking, and the already-adopted check —
// to a raw synthesis. Exported (with an explicit generatedAt) so the eval
// harness can cache raw LLM outputs and re-verify them without re-calling the
// model, byte-stable across runs.
func Finalize(repoKey string, b EvidenceBundle, raw RawSynthesis, adopt AdoptChecker, generatedAt time.Time) (RepoSynthesis, ValidationReport) {
	var poolQuotes []string
	for _, f := range b.Friction {
		if f.Quote != "" {
			poolQuotes = append(poolQuotes, f.Quote)
		}
	}
	for _, p := range b.Prefs {
		poolQuotes = append(poolQuotes, p.Quote)
	}
	for _, g := range b.Signals {
		poolQuotes = append(poolQuotes, g.Detail...)
	}
	qi := newQuoteIndex(poolQuotes)

	themes, unthemed, hard := validateAndCount(b, raw)

	rawCited, droppedCited := 0, 0
	for i := range themes {
		kept, dropped := qi.filter(raw.Themes[i].CitedQuotes)
		themes[i].Quotes = kept
		rawCited += len(raw.Themes[i].CitedQuotes)
		droppedCited += dropped
	}

	rankThemes(themes, b.AnalyzedCount)

	var recs []Recommendation
	prefCount := map[int]int{}
	for ri, rr := range raw.Recommendations {
		if hasQuantitativeClaim(rr.Statement) {
			hard = append(hard, "recommendation statement contains a number: "+rr.Statement)
		}
		if rr.Type == "claude_md_rule" {
			for _, id := range rr.EvidenceIDs {
				if !isPorF(b, id) {
					hard = append(hard, "claude_md_rule "+rr.Statement+" cites non-P/F id "+id)
				}
			}
		}
		if rr.Audience != "" && !validAudiences[rr.Audience] {
			hard = append(hard, "invalid audience "+rr.Audience+": "+rr.Statement)
		}
		if rr.Type == "claude_md_rule" && rr.Audience == "" {
			hard = append(hard, "claude_md_rule missing audience: "+rr.Statement)
		}
		kept, dropped := qi.filter(rr.CitedQuotes)
		rawCited += len(rr.CitedQuotes)
		droppedCited += dropped
		rec := Recommendation{Type: rr.Type, Statement: rr.Statement, ThemeRefs: rr.ThemeRefs, Quotes: kept, Audience: rr.Audience}
		rec.SessionCount = distinctSessions(b, rr.EvidenceIDs)
		rec.AlreadyAdopted = adopt(rec)
		recs = append(recs, rec)
		prefCount[ri] = countP(b, rr.EvidenceIDs)
	}

	rate := 0.0
	if rawCited > 0 {
		rate = float64(droppedCited) / float64(rawCited)
	}
	rs := RepoSynthesis{
		Repo: repoKey, GeneratedAt: generatedAt,
		Window:          Window{From: b.From, To: b.To, SessionCount: b.SessionCount, AnalyzedCount: b.AnalyzedCount},
		Themes:          themes,
		Recommendations: recs,
		Meta:            Meta{Model: synthesisModel, UnthemedFriction: unthemed, ValidationErrors: hard, PrefCountByRec: prefCount},
	}
	return rs, ValidationReport{RawQuoteDropRate: rate, HardErrors: hard}
}

// rankThemes assigns Rank (1 = top). Friction: normalized incident_count desc.
// Opportunity: session_count desc. Friction and opportunity are ranked within their kind.
func rankThemes(themes []Theme, analyzed int) {
	rankKind := func(kind string, score func(Theme) float64) {
		var idx []int
		for i := range themes {
			if themes[i].Kind == kind {
				idx = append(idx, i)
			}
		}
		sort.SliceStable(idx, func(a, b int) bool { return score(themes[idx[a]]) > score(themes[idx[b]]) })
		for rank, i := range idx {
			themes[i].Rank = rank + 1
		}
	}
	rankKind("friction", func(t Theme) float64 {
		if analyzed > 0 {
			return float64(t.IncidentCount) / float64(analyzed)
		}
		return 0
	})
	rankKind("opportunity", func(t Theme) float64 { return float64(t.SessionCount) })
}

func isPorF(b EvidenceBundle, id string) bool {
	for _, f := range b.Friction {
		if f.ID == id {
			return true
		}
	}
	for _, p := range b.Prefs {
		if p.ID == id {
			return true
		}
	}
	return false
}

func distinctSessions(b EvidenceBundle, ids []string) int {
	seen := map[string]bool{}
	for _, id := range ids {
		for _, f := range b.Friction {
			if f.ID == id {
				seen[f.SessionID] = true
			}
		}
		for _, p := range b.Prefs {
			if p.ID == id {
				seen[p.SessionID] = true
			}
		}
		for _, s := range b.Success {
			if s.ID == id {
				seen[s.SessionID] = true
			}
		}
	}
	return len(seen)
}

func countP(b EvidenceBundle, ids []string) int {
	prefIDs := map[string]bool{}
	for _, p := range b.Prefs {
		prefIDs[p.ID] = true
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if prefIDs[id] {
			seen[id] = true
		}
	}
	return len(seen)
}
