package synthesis

import "github.com/rkparsons/agent-insights/internal/insights"

// BuildShowJSON converts RepoSynthesis records into insights.ShowJSON, the
// `insights show --json` payload. This lives in synthesis (not
// insights/contract.go, where the type definitions live) because insights
// cannot import synthesis — synthesis already imports insights, and Go
// forbids the cycle — while synthesis importing insights is the existing,
// legal direction.
func BuildShowJSON(syntheses []RepoSynthesis) insights.ShowJSON {
	out := make([]insights.SynthesisJSON, 0, len(syntheses))
	for _, s := range syntheses {
		out = append(out, synthesisToJSON(s))
	}
	return insights.ShowJSON{SchemaVersion: insights.ContractVersion, Syntheses: out}
}

func synthesisToJSON(s RepoSynthesis) insights.SynthesisJSON {
	themes := make([]insights.ThemeJSON, 0, len(s.Themes))
	for _, t := range s.Themes {
		themes = append(themes, insights.ThemeJSON{
			Title:           t.Title,
			Kind:            t.Kind,
			Summary:         t.Summary,
			Rank:            t.Rank,
			IncidentCount:   t.IncidentCount,
			SessionCount:    t.SessionCount,
			TypeBreakdown:   t.TypeBreakdown,
			Quotes:          nonNil(t.Quotes),
			SessionIDs:      nonNil(t.SessionIDs),
			SignalRefs:      t.SignalRefs,
			OverGeneralized: t.OverGeneralized,
		})
	}
	recs := make([]insights.RecommendationJSON, 0, len(s.Recommendations))
	for _, r := range s.Recommendations {
		recs = append(recs, insights.RecommendationJSON{
			Type:           r.Type,
			Statement:      r.Statement,
			ThemeRefs:      nonNil(r.ThemeRefs),
			SessionCount:   r.SessionCount,
			Quotes:         nonNil(r.Quotes),
			AlreadyAdopted: r.AlreadyAdopted,
			Audience:       r.Audience,
			ActedKey:       ActedKey(r, s.Repo),
		})
	}
	return insights.SynthesisJSON{
		Repo:        s.Repo,
		GeneratedAt: s.GeneratedAt,
		Window: insights.WindowJSON{
			From:          s.Window.From,
			To:            s.Window.To,
			SessionCount:  s.Window.SessionCount,
			AnalyzedCount: s.Window.AnalyzedCount,
		},
		Themes:          themes,
		Recommendations: recs,
		Meta: insights.MetaJSON{
			Model:            s.Meta.Model,
			UnthemedFriction: s.Meta.UnthemedFriction,
			ValidationErrors: s.Meta.ValidationErrors,
			PrefCountByRec:   s.Meta.PrefCountByRec,
		},
	}
}

// nonNil normalizes a nil slice to empty. The contract's array fields
// (quotes, session_ids, theme_refs) are always-required arrays, never
// null — Theme.Quotes/SessionIDs and Recommendation.Quotes/ThemeRefs can be
// nil zero values (e.g. every cited quote got filtered, or an evidence-free
// recommendation), so this is the JSON-boundary normalization point,
// mirroring insights.BuildStatus's due_repos/acted_keys nil handling.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
