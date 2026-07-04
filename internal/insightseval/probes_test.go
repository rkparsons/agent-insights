package insightseval

import (
	"context"
	"strings"
	"testing"
)

// probeMatcher answers probe payloads by scripted behavior per class.
type probeMatcher struct {
	generous bool // matches even forbidden forms as full (drifted matcher)
	calls    int
}

func trues(n int) []bool {
	out := make([]bool, n)
	for i := range out {
		out[i] = true
	}
	return out
}

func (p *probeMatcher) Match(_ context.Context, payload MatchPayload) (MatchResult, error) {
	p.calls++
	id := payload.Items[0].ID
	switch {
	case id == "probe/recall" || id == "probe/negative_recall":
		return MatchResult{Matches: []ItemMatch{{ItemID: id, Granularity: "full",
			NuanceResults: trues(len(payload.Rubric.RequiredNuances)), ForbiddenFormsMatched: []int{}}}}, nil
	case p.generous: // near-miss matched as full = generosity drift
		return MatchResult{Matches: []ItemMatch{{ItemID: id, Granularity: "full",
			NuanceResults: trues(len(payload.Rubric.RequiredNuances)), ForbiddenFormsMatched: []int{}}}}, nil
	default:
		return MatchResult{}, nil
	}
}

func probeRubrics() []Rubric {
	return []Rubric{
		{ID: "C-01", Part: "regression", Surface: "either", Repos: []string{"client-project"}, Hash: "h1",
			Statement:       "verify diagnoses against real evidence before asserting",
			RequiredNuances: []string{"search logs first"}},
		{ID: "C-04", Part: "regression", Surface: "either", Repos: []string{"tmux-ctrl"}, Hash: "h2",
			Statement:                "match process weight to task difficulty",
			RequiredNuances:          []string{"gate on difficulty"},
			ForbiddenGeneralizations: []string{"never dispatch parallel subagents"}},
		{ID: "N-01", Part: "negative", Hash: "h3",
			Statement:                "an automated gofmt hook recommendation",
			ForbiddenGeneralizations: []string{"add a PostToolUse hook that runs gofmt after every edit"}},
	}
}

func TestRunProbesAllClassesPass(t *testing.T) {
	pm := &probeMatcher{}
	res, err := RunProbes(context.Background(), NewCache(t.TempDir()), pm, "env1", probeRubrics(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 3 {
		t.Fatalf("probes = %d", len(res))
	}
	for _, pr := range res {
		if !pr.Pass || len(pr.Granularities) != 3 {
			t.Errorf("probe %s: %+v", pr.Class, pr)
		}
	}
	if pm.calls != 9 { // 3 classes × 3 repeats
		t.Fatalf("calls = %d", pm.calls)
	}
}

func TestRunProbesNearMissCatchesGenerosityDrift(t *testing.T) {
	pm := &probeMatcher{generous: true}
	res, err := RunProbes(context.Background(), NewCache(t.TempDir()), pm, "env1", probeRubrics(), 3)
	if err != nil {
		t.Fatal(err)
	}
	var nearMiss *ProbeResult
	for i := range res {
		if res[i].Class == "near_miss" {
			nearMiss = &res[i]
		}
	}
	if nearMiss == nil || nearMiss.Pass {
		t.Fatalf("a generous matcher must fail the near-miss probe: %+v", nearMiss)
	}
}

func TestRunProbesMissingRubricFails(t *testing.T) {
	if _, err := RunProbes(context.Background(), NewCache(t.TempDir()), &probeMatcher{}, "env1", probeRubrics()[:2], 3); err == nil || !strings.Contains(err.Error(), "N-01") {
		t.Fatalf("missing probe rubric must error: %v", err)
	}
}
