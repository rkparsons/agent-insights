package synthesis

import (
	"sort"
	"time"
)

type CuratedRec struct {
	Rec           Recommendation
	SourceRepo    string
	ThemeTitles   []string
	WindowFrom    string
	WindowTo      string
	GeneratedAt   time.Time
	Model         string
	AnalyzedCount int
	ActedKey      string
}

type candidate struct {
	c     CuratedRec
	order int // original index within its synthesis, for stable tie-break
}

// Curate drops already-adopted and user-acted recommendations, then orders
// the remainder type-diverse round-robin so buildable types (new_skill,
// hook, setting) aren't buried by high-volume claude_md_rule counts.
func Curate(syntheses []RepoSynthesis, acted map[string]bool, maxRows int) ([]CuratedRec, int) {
	byType := map[string][]candidate{}
	adoptedCount := 0
	for _, s := range syntheses {
		for i, r := range s.Recommendations {
			if r.AlreadyAdopted == "yes" {
				adoptedCount++
				continue
			}
			key := ActedKey(r, s.Repo)
			if acted[key] {
				continue
			}
			cand := candidate{order: i, c: CuratedRec{
				Rec:           r,
				SourceRepo:    s.Repo,
				ThemeTitles:   themeTitles(s, r.ThemeRefs),
				WindowFrom:    s.Window.From,
				WindowTo:      s.Window.To,
				GeneratedAt:   s.GeneratedAt,
				Model:         s.Meta.Model,
				AnalyzedCount: s.Window.AnalyzedCount,
				ActedKey:      key,
			}}
			byType[r.Type] = append(byType[r.Type], cand)
		}
	}
	// sort each bucket
	for typ := range byType {
		b := byType[typ]
		sort.SliceStable(b, func(i, j int) bool {
			if b[i].c.Rec.SessionCount != b[j].c.Rec.SessionCount {
				return b[i].c.Rec.SessionCount > b[j].c.Rec.SessionCount
			}
			if b[i].c.SourceRepo != b[j].c.SourceRepo {
				return b[i].c.SourceRepo < b[j].c.SourceRepo
			}
			return b[i].order < b[j].order
		})
		byType[typ] = b
	}
	// deterministic type visitation order: by bucket-head SessionCount desc, then type name
	types := make([]string, 0, len(byType))
	for typ := range byType {
		types = append(types, typ)
	}
	sort.Slice(types, func(i, j int) bool {
		hi, hj := byType[types[i]][0].c.Rec.SessionCount, byType[types[j]][0].c.Rec.SessionCount
		if hi != hj {
			return hi > hj
		}
		return types[i] < types[j]
	})
	// round-robin fill
	var out []CuratedRec
	for len(out) < maxRows {
		progressed := false
		for _, typ := range types {
			if len(byType[typ]) == 0 {
				continue
			}
			out = append(out, byType[typ][0].c)
			byType[typ] = byType[typ][1:]
			progressed = true
			if len(out) >= maxRows {
				break
			}
		}
		if !progressed {
			break
		}
	}
	return out, adoptedCount
}

func themeTitles(s RepoSynthesis, refs []int) []string {
	var out []string
	for _, ref := range refs {
		if ref >= 0 && ref < len(s.Themes) {
			out = append(out, s.Themes[ref].Title)
		}
	}
	return out
}
