package synthesis

import "testing"

func TestBuildShowJSONCarriesTitleAndLastSeen(t *testing.T) {
	s := RepoSynthesis{Repo: "r", Recommendations: []Recommendation{
		{Type: "habit", Title: "Verify before claiming done", Statement: "st", LastSeen: "2026-07-09"},
	}}
	show := BuildShowJSON([]RepoSynthesis{s})
	rec := show.Syntheses[0].Recommendations[0]
	if rec.Title != "Verify before claiming done" {
		t.Errorf("Title = %q", rec.Title)
	}
	if rec.LastSeen != "2026-07-09" {
		t.Errorf("LastSeen = %q", rec.LastSeen)
	}
}
