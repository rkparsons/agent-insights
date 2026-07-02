package synthesis

import "testing"

func rec(t, stmt string, sc int, adopted string) Recommendation {
	return Recommendation{Type: t, Statement: stmt, SessionCount: sc, AlreadyAdopted: adopted}
}

func TestCurate_TypeDiverse_BuildablesNotBuriedByRules(t *testing.T) {
	// 6 rules with high session counts + 1 skill + 1 hook with lower counts.
	s := RepoSynthesis{Repo: "client-project", Recommendations: []Recommendation{
		rec("claude_md_rule", "r1", 9, "no"), rec("claude_md_rule", "r2", 9, "no"),
		rec("claude_md_rule", "r3", 8, "no"), rec("claude_md_rule", "r4", 7, "no"),
		rec("claude_md_rule", "r5", 7, "no"), rec("claude_md_rule", "r6", 7, "no"),
		rec("new_skill", "skill1", 4, "no"), rec("hook", "hook1", 2, "no"),
	}}
	rows, _ := Curate([]RepoSynthesis{s}, nil, 6)
	if len(rows) != 6 {
		t.Fatalf("len = %d, want 6", len(rows))
	}
	types := map[string]bool{}
	for _, r := range rows {
		types[r.Rec.Type] = true
	}
	if !types["new_skill"] || !types["hook"] {
		t.Errorf("type-diverse curation must surface new_skill AND hook, got types %v", types)
	}
}

func TestCurate_DropsAdoptedAndActed(t *testing.T) {
	s := RepoSynthesis{Repo: "client-project", Recommendations: []Recommendation{
		rec("claude_md_rule", "open", 5, "no"),
		rec("claude_md_rule", "adopted", 9, "yes"),
		rec("new_skill", "acted-one", 4, "no"),
	}}
	acted := map[string]bool{ActedKey(s.Recommendations[2], "client-project"): true}
	rows, adoptedCount := Curate([]RepoSynthesis{s}, acted, 6)
	if adoptedCount != 1 {
		t.Errorf("adoptedCount = %d, want 1", adoptedCount)
	}
	if len(rows) != 1 || rows[0].Rec.Statement != "open" {
		t.Fatalf("rows = %+v, want only the open, non-acted rec", rows)
	}
}

func TestCurate_ResolvesThemeTitlesAndProvenance(t *testing.T) {
	s := RepoSynthesis{
		Repo:            "client-project",
		Window:          Window{From: "2026-06-30", To: "2026-06-24", AnalyzedCount: 159},
		Meta:            Meta{Model: "claude-opus-4-8"},
		Themes:          []Theme{{Title: "T0"}, {Title: "T1"}},
		Recommendations: []Recommendation{{Type: "claude_md_rule", Statement: "x", SessionCount: 1, AlreadyAdopted: "no", ThemeRefs: []int{1}}},
	}
	rows, _ := Curate([]RepoSynthesis{s}, nil, 6)
	if len(rows) != 1 || len(rows[0].ThemeTitles) != 1 || rows[0].ThemeTitles[0] != "T1" {
		t.Fatalf("theme titles = %+v, want [T1]", rows)
	}
	if rows[0].AnalyzedCount != 159 || rows[0].Model != "claude-opus-4-8" || rows[0].SourceRepo != "client-project" {
		t.Errorf("provenance not carried: %+v", rows[0])
	}
}
