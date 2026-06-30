package insights

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tmux-ctrl/internal/sources/claude"
	"tmux-ctrl/internal/userconfig"
)

// TestAnalyzeReal runs the full producer (real claude -p) over curated sessions when
// INSIGHTS_REAL_SESSIONS (a glob) is set. Manual gate — private machine data, real
// subscription calls. Curate ~4: a huge, a zero-friction, a frictionful, a short one.
//
//	INSIGHTS_REAL_SESSIONS="$HOME/.claude/projects/<proj>/<id>.jsonl" \
//	  go test ./internal/insights/ -run TestAnalyzeReal -v -timeout 30m
func TestAnalyzeReal(t *testing.T) {
	glob := os.Getenv("INSIGHTS_REAL_SESSIONS")
	if glob == "" {
		t.Skip("set INSIGHTS_REAL_SESSIONS=<glob> to run")
	}
	files, err := filepath.Glob(glob)
	if err != nil || len(files) == 0 {
		t.Fatalf("glob %q matched nothing: %v", glob, err)
	}
	cfg, err := userconfig.Load()
	if err != nil {
		t.Fatalf("load userconfig: %v", err)
	}
	repo := resolveRepo(&cfg)
	judge := NewClaudeJudge()

	var repoPopulated bool
	for _, f := range files {
		name := filepath.Base(f)
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			ev, c := claude.DecodeTranscript(strings.NewReader(string(data)))
			if len(ev) == 0 {
				t.Fatal("decoded 0 events")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			a, err := Analyze(ctx, ev, c, strings.TrimSuffix(name, ".jsonl"), repo, judge)
			if err != nil {
				t.Fatalf("Analyze: %v", err)
			}
			// Schema-valid: required scalar fields present.
			if a.UnderlyingGoal == "" || a.SessionType == "" || a.Outcome == "" || a.BriefSummary == "" {
				t.Errorf("missing required judged field: %+v", a.JudgedFields)
			}
			// Every surviving quote is verbatim (validation actually ran).
			vi := Extract(ev, c, "x", noRepo).Verbatim
			for i, inc := range a.FrictionIncidents {
				if inc.EvidenceQuote != "" && !inc.QuoteUnverified &&
					!vi.ContainsAny(inc.EvidenceQuote) && !vi.ContainsAnyNormalized(inc.EvidenceQuote) {
					t.Errorf("friction[%d] quote survived but is not verbatim: %q", i, inc.EvidenceQuote)
				}
			}
			for i, p := range a.StandingPreferences {
				if !vi.ContainsUser(p.EvidenceQuote) && !vi.ContainsUserNormalized(p.EvidenceQuote) {
					t.Errorf("preference[%d] quote survived but is not verbatim user words: %q", i, p.EvidenceQuote)
				}
			}
			// Arrays are non-nil ([] not null).
			if a.FrictionIncidents == nil || a.StandingPreferences == nil {
				t.Error("arrays must be non-nil")
			}
			if a.Stats.Repo != "" {
				repoPopulated = true
			}
			t.Logf("type=%s outcome=%s friction=%d prefs=%d repo=%q goal=%q",
				a.SessionType, a.Outcome, len(a.FrictionIncidents), len(a.StandingPreferences), a.Stats.Repo, a.UnderlyingGoal)
		})
	}
	if !repoPopulated {
		t.Log("note: no session resolved to a configured repo (Repo empty for all)")
	}
}
