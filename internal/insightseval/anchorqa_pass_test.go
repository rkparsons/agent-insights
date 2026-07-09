package insightseval

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestDevAnchorQAPass executes one anchor-QA pass (spec "Anchor-QA pass",
// docs/superpowers/specs/2026-07-03-insights-outcome-eval-design.md): the
// pinned judge audits every anchored rubric's full pre-QA source theme
// against the frozen pool and writes committed-shape inputs/ and verdicts/
// under $ANCHOR_QA_PASS_DIR (intended: <data-repo>/anchor-qa/<pass-name>).
// Real LLM spend; resumable — rubrics with an existing verdicts file are
// skipped. Applying removals to rubric files stays a separate, human-reviewed
// step (operator veto is toward keep only).
func TestDevAnchorQAPass(t *testing.T) {
	passDir := os.Getenv("ANCHOR_QA_PASS_DIR")
	if passDir == "" {
		t.Skip("set ANCHOR_QA_PASS_DIR=<pass output dir> to run the anchor-QA judge (real LLM spend)")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(home, "Developer", "insights-eval-data")
	const poolVersion = "v1"
	poolDir := filepath.Join(dataDir, "baseline-pool", poolVersion)

	rubrics, err := LoadRubrics()
	if err != nil {
		t.Fatal(err)
	}
	version, err := ClaudeVersionString()
	if err != nil {
		t.Fatal(err)
	}
	pin, err := ComposeEnvPin(dataDir, t.TempDir(), nil, version)
	if err != nil {
		t.Fatal(err)
	}
	rsh, err := RubricSetHash()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"inputs", "verdicts"} {
		if err := os.MkdirAll(filepath.Join(passDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	meta := map[string]any{
		"protocol":            "Anchor-QA pass, docs/superpowers/specs/2026-07-03-insights-outcome-eval-design.md (amendment 2026-07-06)",
		"judge_model":         AnchorQAJudgeModel,
		"claude_version":      version,
		"env_hash":            pin.EnvHash,
		"pool_version":        poolVersion,
		"prompt_sha256":       sha256hex([]byte(anchorQAPrompt)),
		"schema_sha256":       sha256hex([]byte(anchorQASchema)),
		"pre_pass_rubric_set": rsh,
		"started_at":          time.Now().UTC().Format(time.RFC3339),
	}
	if err := writeJSON(filepath.Join(passDir, "meta.json"), meta); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	audited, removals := 0, 0
	for _, r := range rubrics {
		if len(r.AnchorSessionIDs) == 0 {
			continue
		}
		audited++
		verdictPath := filepath.Join(passDir, "verdicts", r.ID+".json")
		if _, err := os.Stat(verdictPath); err == nil {
			t.Logf("%s: verdicts exist, skipping (resume)", r.ID)
			continue
		}
		entries, err := loadPoolEntries(poolDir, r.SourceThemeSessionIDs)
		if err != nil {
			t.Fatal(err)
		}
		in, err := buildJudgeInput(r, entries)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeJSON(filepath.Join(passDir, "inputs", r.ID+".json"), in); err != nil {
			t.Fatal(err)
		}
		res, err := judgeRubricAnchors(ctx, in, pin.ConfigDir, pin.WorkDir)
		if err != nil {
			t.Fatalf("%s: %v", r.ID, err)
		}
		if err := writeJSON(verdictPath, res); err != nil {
			t.Fatal(err)
		}
		rm := 0
		for _, v := range res.Verdicts {
			if v.Verdict == "remove" {
				rm++
				t.Logf("%s: REMOVE %s — %s", r.ID, v.SessionID, v.Rationale)
			}
		}
		removals += rm
		t.Logf("%s: %d sessions judged, %d removals", r.ID, len(res.Verdicts), rm)
	}
	// 21 at pass 1; C-A/C-D1 degraded to no-anchor there; the rec-surface
	// corroboration amendment (2026-07-09) dropped C-E2's anchors; pass 2
	// (run at 18) degraded C-D2.
	if audited != 17 {
		t.Fatalf("audited %d anchored rubrics, want 17 (symmetry violated)", audited)
	}
	t.Logf("pass complete: %d rubrics audited, %d total removals proposed", audited, removals)
}
