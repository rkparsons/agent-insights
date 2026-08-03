package eval

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
)

func writePoolAnalysis(t *testing.T, dir, id, repo string, turns int) {
	t.Helper()
	a := insights.AgentSessionAnalysis{
		Stats: insights.AgentSessionStats{SessionID: id, Repo: repo, AssistantTurns: turns},
		JudgedFields: insights.JudgedFields{
			UnderlyingGoal: "goal-" + id, Outcome: "fully_achieved", SessionType: "single_task",
		},
		TranscriptMtime: time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := writeJSON(filepath.Join(dir, id+".json"), a); err != nil {
		t.Fatal(err)
	}
}

func TestRecomputeFactsMergesPoolJudgmentWithFrozenStats(t *testing.T) {
	data, plain := buildCorpusFixture(t) // s1, s2 frozen
	pool := t.TempDir()
	writePoolAnalysis(t, pool, "s1", "/Users/x/Developer/myrepo", 99) // stale turn count
	writePoolAnalysis(t, pool, "s2", "/Users/x/Developer/myrepo", 99)
	writePoolAnalysis(t, pool, "gapped", "/Users/x/Developer/myrepo", 7) // no corpus file
	c, err := OpenCorpus(data, plain)
	if err != nil {
		t.Fatal(err)
	}
	cache := NewCache(t.TempDir())

	res, err := RecomputeFacts(c, cache, "cv1", pool, []string{"s2", "s1", "gapped"})
	if err != nil {
		t.Fatal(err)
	}
	var gotIDs []string
	for _, a := range res.Analyses {
		gotIDs = append(gotIDs, a.Stats.SessionID)
	}
	if !slices.Equal(gotIDs, []string{"gapped", "s1", "s2"}) {
		t.Fatalf("order: %v", gotIDs)
	}
	if !slices.Equal(res.GapFallbacks, []string{"gapped"}) {
		t.Fatalf("gaps: %v", res.GapFallbacks)
	}
	for _, a := range res.Analyses {
		if a.UnderlyingGoal != "goal-"+a.Stats.SessionID {
			t.Fatalf("judged fields lost for %s", a.Stats.SessionID)
		}
	}
	// frozen transcript has 0 assistant turns: recomputed stats replace the
	// pool's stale 99 for corpus sessions, and Repo is preserved from the pool
	for _, a := range res.Analyses {
		switch a.Stats.SessionID {
		case "gapped":
			if a.Stats.AssistantTurns != 7 {
				t.Fatalf("gap fallback stats replaced: %+v", a.Stats)
			}
		default:
			if a.Stats.AssistantTurns != 0 {
				t.Fatalf("stats not recomputed for %s: turns=%d", a.Stats.SessionID, a.Stats.AssistantTurns)
			}
			if a.Stats.Repo != "/Users/x/Developer/myrepo" {
				t.Fatalf("repo not preserved: %q", a.Stats.Repo)
			}
			if !a.TranscriptMtime.Equal(time.Date(2026, 6, 25, 10, 0, 0, 0, time.UTC)) {
				t.Fatalf("mtime not restamped from manifest: %v", a.TranscriptMtime)
			}
		}
	}
	if res.CacheMisses != 2 || res.CacheHits != 0 {
		t.Fatalf("first run cache: hits=%d misses=%d", res.CacheHits, res.CacheMisses)
	}
	if res.FactsHash == "" || res.PoolSliceHash == "" {
		t.Fatal("hashes must be populated")
	}

	res2, err := RecomputeFacts(c, cache, "cv1", pool, []string{"s2", "s1", "gapped"})
	if err != nil {
		t.Fatal(err)
	}
	if res2.CacheHits != 2 || res2.CacheMisses != 0 {
		t.Fatalf("second run cache: hits=%d misses=%d", res2.CacheHits, res2.CacheMisses)
	}
	if res2.FactsHash != res.FactsHash {
		t.Fatal("FactsHash must be stable across cached runs")
	}

	// a code-version bump invalidates the facts cache
	res3, err := RecomputeFacts(c, cache, "cv2", pool, []string{"s1"})
	if err != nil {
		t.Fatal(err)
	}
	if res3.CacheMisses != 1 {
		t.Fatalf("code-version bump must miss: %+v", res3)
	}
}

func TestRecomputeFactsMissingPoolAnalysisErrors(t *testing.T) {
	data, plain := buildCorpusFixture(t)
	c, err := OpenCorpus(data, plain)
	if err != nil {
		t.Fatal(err)
	}
	_, err = RecomputeFacts(c, NewCache(t.TempDir()), "cv", t.TempDir(), []string{"s1"})
	if err == nil {
		t.Fatal("a benchmark id without a pool analysis must fail loudly")
	}
}
