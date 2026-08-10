package synthesis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
)

func writeEnrichFixtures(t *testing.T) {
	t.Helper()
	// analyses pool: two sessions with known start dates
	if err := os.MkdirAll(filepath.Join(insights.InsightsDir(), "analyses"), 0o755); err != nil {
		t.Fatal(err)
	}
	for sid, start := range map[string]time.Time{
		"00000000-0000-4000-8000-000000000001": time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC),
		"00000000-0000-4000-8000-000000000002": time.Date(2026, 7, 9, 9, 0, 0, 0, time.UTC),
	} {
		a := insights.AgentSessionAnalysis{Stats: insights.AgentSessionStats{SessionID: sid, Start: start}}
		data, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(insights.InsightsDir(), "analyses", sid+".json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := RepoSynthesis{
		Repo:   "alpha",
		Window: Window{From: "2026-07-01", To: "2026-07-10"},
		Themes: []Theme{{Title: "t0", Kind: "friction",
			SessionIDs: []string{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002"}}},
		Recommendations: []Recommendation{
			{Type: "habit", Statement: "needs both", ThemeRefs: []int{0}},
			{Type: "habit", Statement: "bad refs", ThemeRefs: []int{7, -1}},
			{Type: "habit", Title: "Already titled", Statement: "has title", ThemeRefs: []int{0}, LastSeen: "2026-07-02"},
		},
	}
	if err := Store(s, Render(s), "2026-07-10"); err != nil {
		t.Fatal(err)
	}
}

func TestRunEnrichFillsMissingFields(t *testing.T) {
	t.Setenv("AGENT_INSIGHTS_DIR", t.TempDir())
	writeEnrichFixtures(t)
	titler := func(ctx context.Context, reqs []TitleReq) (map[int]string, error) {
		out := map[int]string{}
		for _, r := range reqs {
			out[r.Index] = "Title for " + strconv.Itoa(r.Index) + "."
		}
		return out, nil
	}
	sum, err := RunEnrich(context.Background(), titler, EnrichOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Updated != 1 || sum.TitlesFilled != 2 || sum.LastSeenFilled != 2 {
		t.Errorf("summary = %+v", sum)
	}
	got, ok := newestInRepoDir(filepath.Join(synthesisDir(), "alpha"))
	if !ok {
		t.Fatal("snapshot unreadable after enrich")
	}
	r := got.Recommendations
	if r[0].Title != "Title for 0" || r[0].LastSeen != "2026-07-09" {
		t.Errorf("rec 0 = %+v (want normalized title, max theme session date)", r[0])
	}
	if r[1].LastSeen != "2026-07-10" {
		t.Errorf("rec 1 LastSeen = %q, want window.to fallback", r[1].LastSeen)
	}
	if r[2].Title != "Already titled" || r[2].LastSeen != "2026-07-02" {
		t.Errorf("rec 2 mutated: %+v", r[2])
	}
	if !reflect.DeepEqual(got.Meta, Meta{}) {
		t.Errorf("enrich mutated meta: %+v", got.Meta)
	}
	md, err := os.ReadFile(filepath.Join(synthesisDir(), "alpha", "2026-07-10.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(md), "**Title for 0**") {
		t.Errorf("md not re-rendered with titles:\n%s", md)
	}
}

func TestRunEnrichIdempotent(t *testing.T) {
	t.Setenv("AGENT_INSIGHTS_DIR", t.TempDir())
	writeEnrichFixtures(t)
	titler := func(ctx context.Context, reqs []TitleReq) (map[int]string, error) {
		out := map[int]string{}
		for _, r := range reqs {
			out[r.Index] = "T" + strconv.Itoa(r.Index)
		}
		return out, nil
	}
	if _, err := RunEnrich(context.Background(), titler, EnrichOptions{}); err != nil {
		t.Fatal(err)
	}
	sum, err := RunEnrich(context.Background(),
		func(ctx context.Context, reqs []TitleReq) (map[int]string, error) {
			t.Errorf("titler called on second run with %v", reqs)
			return nil, nil
		}, EnrichOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Updated != 0 || sum.TitlesFilled != 0 || sum.LastSeenFilled != 0 {
		t.Errorf("second run not a no-op: %+v", sum)
	}
}

func TestRunEnrichDryRun(t *testing.T) {
	t.Setenv("AGENT_INSIGHTS_DIR", t.TempDir())
	writeEnrichFixtures(t)
	before, err := os.ReadFile(filepath.Join(synthesisDir(), "alpha", "2026-07-10.json"))
	if err != nil {
		t.Fatal(err)
	}
	sum, err := RunEnrich(context.Background(),
		func(ctx context.Context, reqs []TitleReq) (map[int]string, error) {
			t.Errorf("titler called in dry-run")
			return nil, nil
		}, EnrichOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Updated != 0 {
		t.Errorf("dry-run updated %d", sum.Updated)
	}
	after, err := os.ReadFile(filepath.Join(synthesisDir(), "alpha", "2026-07-10.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("dry-run modified the snapshot")
	}
}

func TestRunEnrichLeakBlocksSnapshot(t *testing.T) {
	t.Setenv("AGENT_INSIGHTS_DIR", t.TempDir())
	writeEnrichFixtures(t)
	path := filepath.Join(synthesisDir(), "alpha", "2026-07-10.json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// "/Users/dev" mirrors the README's example home path: it trips the
	// md leak scan under test while staying safe for the repo privacy scan.
	sum, err := RunEnrich(context.Background(),
		func(ctx context.Context, reqs []TitleReq) (map[int]string, error) {
			out := map[int]string{}
			for _, r := range reqs {
				out[r.Index] = "Leaky /Users/dev title"
			}
			return out, nil
		}, EnrichOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.Updated != 0 || sum.Skipped != 1 {
		t.Errorf("summary = %+v, want leak-blocked snapshot skipped", sum)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("leak-blocked snapshot was rewritten")
	}
}

func TestRunEnrichTitlerFailureStillFillsLastSeen(t *testing.T) {
	t.Setenv("AGENT_INSIGHTS_DIR", t.TempDir())
	writeEnrichFixtures(t)
	sum, err := RunEnrich(context.Background(),
		func(ctx context.Context, reqs []TitleReq) (map[int]string, error) {
			return nil, errors.New("boom")
		}, EnrichOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if sum.LastSeenFilled != 2 || sum.TitlesFilled != 0 || sum.Updated != 1 {
		t.Errorf("summary = %+v", sum)
	}
	got, _ := newestInRepoDir(filepath.Join(synthesisDir(), "alpha"))
	if got.Recommendations[0].Title != "" {
		t.Error("title appeared despite titler failure")
	}
	if got.Recommendations[0].LastSeen != "2026-07-09" {
		t.Errorf("last_seen not written on titler failure: %+v", got.Recommendations[0])
	}
}
