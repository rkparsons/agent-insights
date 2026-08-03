package insights

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tmux-ctrl/internal/transcript"
)

// TestLightEvalReal runs the step-7 light gate end-to-end (real claude -p, N repeats)
// when INSIGHTS_LIGHT_EVAL is set. Manual gate — private machine data, real
// subscription calls. It curates the corpus deterministically, runs the decomposed
// pipeline, scores, and writes the committed artifacts plus a local (un-committed)
// manifest. One-shot: the live corpus prunes, so it is not a repeatable regression test.
//
//	INSIGHTS_LIGHT_EVAL=1 go test ./internal/insights/ -run TestLightEvalReal -v -timeout 120m
func TestLightEvalReal(t *testing.T) {
	if os.Getenv("INSIGHTS_LIGHT_EVAL") == "" {
		t.Skip("set INSIGHTS_LIGHT_EVAL=1 to run the light gate (real subscription calls)")
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	repo := cfg.Resolver()

	// 1. Curate deterministically from the live corpus (decode-only, no LLM).
	refs, err := transcript.WalkTranscripts()
	if err != nil {
		t.Fatalf("walk transcripts: %v", err)
	}
	var pool []sessionStat
	for _, ref := range refs {
		ev, c, _, err := transcript.LoadTranscript(ref.Path)
		if err != nil || len(ev) == 0 {
			continue
		}
		st := Extract(ev, c, ref.SessionID, repo).Stats
		fi, statErr := os.Stat(ref.Path)
		var size int64
		if statErr == nil {
			size = fi.Size()
		}
		pool = append(pool, sessionStat{Ref: ref, Stats: st, Bytes: size})
	}
	curated := curate(pool)
	t.Logf("curated %d sessions from %d transcripts", len(curated), len(pool))
	if len(curated) == 0 {
		t.Fatal("curated 0 sessions — empty or unreadable corpus")
	}

	// 2. Run the decomposed pipeline, N repeats per curated session (real LLM).
	judge := NewClaudeJudge()
	var runs []sessionRun
	skipped := 0
	for _, cs := range curated {
		ev, c, _, err := transcript.LoadTranscript(cs.Ref.Path)
		if err != nil || len(ev) == 0 {
			t.Logf("skip %s: load failed (%v)", cs.Ref.SessionID, err)
			skipped++
			continue
		}
		// A single claude subprocess can fail transiently (e.g. "claude exit -1",
		// killed by signal) on a 60+ call real run. Retry once, then skip the
		// session (a coverage gap reported in the verdict) rather than aborting the
		// whole run — losing 15 good sessions to one transient crash is the wrong call.
		var sr sessionRun
		var runErr error
		for attempt := 1; attempt <= 2; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			sr, runErr = runSession(ctx, ev, c, cs.Ref.SessionID, repo, cs.Cell, cs.Repeats, judge)
			cancel()
			if runErr == nil {
				break
			}
			t.Logf("runSession %s (%s) attempt %d failed: %v", cs.Ref.SessionID, cs.Cell, attempt, runErr)
		}
		if runErr != nil {
			t.Logf("skip %s (%s): runSession failed after retry: %v", cs.Ref.SessionID, cs.Cell, runErr)
			skipped++
			continue
		}
		runs = append(runs, sr)
		t.Logf("ran %s cell=%s repeats=%d", cs.Ref.SessionID, cs.Cell, cs.Repeats)
	}
	t.Logf("ran %d sessions, skipped %d", len(runs), skipped)

	// 3. Score + assemble the provisional verdict.
	rep := assembleReport(runs)

	// 4. Write artifacts (committed dir + local manifest outside the repo).
	dir := os.Getenv("INSIGHTS_EVAL_OUT")
	if dir == "" {
		dir = "../../../docs/superpowers/specs/eval"
	}
	local := filepath.Join(os.TempDir(), "tmux-ctrl-light-eval-manifest.json")
	if err := writeEvalArtifacts(dir, local, rep, runs); err != nil {
		t.Fatalf("write artifacts: %v", err)
	}

	t.Logf("VERDICT: %s", rep.Verdict())
	t.Logf("calls=%d notional=$%.2f cards=%d", rep.Calls, rep.NotionalSpendUSD, len(rep.Cards))
	t.Logf("raw_fabrication=%.3f mean_jaccard=%.3f false_friction=%d recall=%d",
		rep.RawFabricationRate, rep.MeanTypeJaccard, rep.FalseFrictionCandidates, rep.RecallCandidates)
	for _, sf := range rep.SoftFloors {
		t.Logf("soft floor %-28s value=%.3f target=%.3f pass=%v", sf.Name, sf.Value, sf.Target, sf.Pass)
	}
	if len(rep.MetaFindings) > 0 {
		t.Logf("meta findings (reported, not blocking): %v", rep.MetaFindings)
	}
	t.Logf("artifacts: %s ; local manifest: %s", dir, local)
}
