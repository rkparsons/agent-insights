package synthesis

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
)

// v2GeneratedAt is the fixed stamp every Verify2 test passes in, so snapshots
// are byte-stable across runs.
var v2GeneratedAt = time.Date(2026, 6, 12, 9, 0, 0, 0, time.UTC)

// v2Bundles is the two-repo evidence fixture the Verify2 tests verify against.
// Repo keys and session ids are synthetic (alpha/beta, sess-*) per the
// committed-fixture privacy constraint. Quotes are >= minQuoteRunes so the
// shared quote index does not drop them for length alone.
func v2Bundles() map[string]EvidenceBundle {
	return map[string]EvidenceBundle{
		"alpha": {
			Repo: "alpha", SessionCount: 3, AnalyzedCount: 3,
			From: "2026-06-01", To: "2026-06-09",
			Friction: []FrictionItem{
				{ID: "F1", Type: "wrong_approach", OneLine: "shipped unrun", Quote: "the build passed but I never ran it", SessionID: "sess-a1"},
				{ID: "F2", Type: "rework", OneLine: "review redone", Quote: "we had to redo the whole review pass", SessionID: "sess-a2"},
			},
			Prefs: []PrefItem{
				{ID: "P1", Rule: "smoke test first", Quote: "always run the smoke test before calling it done", SessionID: "sess-a1"},
			},
			Success: []SuccessItem{{ID: "S1", Goal: "ship the change", Summary: "clean run", SessionID: "sess-a3"}},
			Signals: []OppSignal{
				{ID: "G1", Kind: "retyped_directives", Magnitude: 3,
					MemberSessions: []string{"sess-a1", "sess-a2", "sess-a3"},
					Detail:         []string{"re-run the review after a rewrite"}},
				{ID: "G2", Kind: "high_read", Magnitude: 3, MemberSessions: []string{"sess-a2"}},
			},
			SessionDates: map[string]string{"sess-a1": "2026-06-01", "sess-a2": "2026-06-05", "sess-a3": "2026-06-09"},
		},
		"beta": {
			Repo: "beta", SessionCount: 2, AnalyzedCount: 2,
			From: "2026-06-11", To: "2026-06-12",
			Friction: []FrictionItem{
				{ID: "F1", Type: "wrong_approach", OneLine: "guard missing", Quote: "nothing stopped me committing that", SessionID: "sess-b1"},
			},
			Prefs: []PrefItem{
				{ID: "P1", Rule: "smoke test first", Quote: "smoke test it before you say it works", SessionID: "sess-b2"},
			},
			SessionDates: map[string]string{"sess-b1": "2026-06-11", "sess-b2": "2026-06-12"},
		},
	}
}

// v2Finding is a valid claude_md_rule finding: cross-repo P evidence, audience
// set, nothing for the verifier to correct.
func v2Finding() insights.RawFinding {
	return insights.RawFinding{
		Rank:           1,
		Title:          "Run the smoke test first",
		Statement:      "Run the smoke test before calling a task done.",
		RankRationale:  "Recurs across both repos and lets regressions ship.",
		Asset:          insights.AssetJSON{Type: "claude_md_rule", Target: "~/.claude/CLAUDE.md", Content: "Run the smoke test before calling a task done."},
		Audience:       "user",
		EvidenceIDs:    []string{"alpha/P1", "beta/P1"},
		AlreadyAdopted: insights.AdoptedJSON{Verdict: "no"},
	}
}

func v2Raw(findings ...insights.RawFinding) insights.RawGlobalSynthesis {
	return insights.RawGlobalSynthesis{SchemaVersion: 2, Findings: findings}
}

func v2Config() insights.Config {
	return insights.Config{SynthesisModel: "test-model"}
}

// verifyFixture runs the verifier over v2Bundles with no dotfiles repo, so the
// placement-recency arbitration is skipped.
func verifyFixture(t *testing.T, raw insights.RawGlobalSynthesis) (insights.GlobalSynthesisJSON, error) {
	t.Helper()
	return verifyGlobal(raw, v2Bundles(), v2Config(), v2GeneratedAt, nil)
}

func TestVerify2_DanglingID_Fails(t *testing.T) {
	danglingFinding := v2Finding()
	danglingFinding.EvidenceIDs = []string{"alpha/P1", "alpha/F99"}

	unnamespaced := v2Finding()
	unnamespaced.EvidenceIDs = []string{"P1"}

	droppedDangling := v2Raw(v2Finding())
	droppedDangling.Dropped = []insights.DroppedJSON{{
		Summary: "one-off tooling gripe", Reason: "single incident already addressed",
		EvidenceIDs: []string{"beta/F99"},
	}}

	unknownRepo := v2Finding()
	unknownRepo.EvidenceIDs = []string{"gamma/P1"}

	cases := []struct {
		name string
		raw  insights.RawGlobalSynthesis
	}{
		{"finding cites unknown item", v2Raw(danglingFinding)},
		{"finding cites id without namespace", v2Raw(unnamespaced)},
		{"finding cites unknown repo", v2Raw(unknownRepo)},
		{"dropped cites unknown item", droppedDangling},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := verifyFixture(t, c.raw); err == nil {
				t.Fatal("want fail-closed error, got nil")
			}
		})
	}
}

func TestVerify2_RankPermutation(t *testing.T) {
	ranked := func(ranks ...int) insights.RawGlobalSynthesis {
		var fs []insights.RawFinding
		for i, r := range ranks {
			f := v2Finding()
			f.Rank = r
			f.Title = fmt.Sprintf("Rule %c", 'a'+i)
			f.Statement = fmt.Sprintf("Rule %c must hold before a task is done.", 'a'+i)
			fs = append(fs, f)
		}
		return v2Raw(fs...)
	}

	elevenRanks := make([]int, 11)
	for i := range elevenRanks {
		elevenRanks[i] = i + 1
	}

	cases := []struct {
		name    string
		raw     insights.RawGlobalSynthesis
		wantErr bool
	}{
		{"duplicate rank", ranked(1, 2, 2), true},
		{"gap in ranks", ranked(1, 3), true},
		{"zero rank", ranked(0, 1), true},
		{"eleven findings", ranked(elevenRanks...), true},
		{"out of order permutation", ranked(2, 1), false},
		{"ten findings", ranked(elevenRanks[:10]...), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := verifyFixture(t, c.raw)
			if c.wantErr && err == nil {
				t.Fatal("want fail-closed error, got nil")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("want no error, got %v", err)
			}
		})
	}
}

// typedFinding builds a finding of assetType citing ids, with audience set
// only where the contract requires it.
func typedFinding(assetType string, ids ...string) insights.RawFinding {
	f := v2Finding()
	f.Asset.Type = assetType
	f.EvidenceIDs = ids
	if assetType != "claude_md_rule" && assetType != "repo_doc" {
		f.Audience = ""
	}
	return f
}

func TestVerify2_KindGrounding_Fails(t *testing.T) {
	cases := []struct {
		name    string
		finding insights.RawFinding
		wantErr bool
	}{
		{"claude_md_rule cites only success", typedFinding("claude_md_rule", "alpha/S1"), true},
		{"claude_md_rule cites a signal", typedFinding("claude_md_rule", "alpha/P1", "alpha/G1"), true},
		{"claude_md_rule cites prefs and friction", typedFinding("claude_md_rule", "alpha/P1", "alpha/F1"), false},
		{"repo_doc cites a signal", typedFinding("repo_doc", "alpha/G1"), true},
		{"hook cites only prefs", typedFinding("hook", "alpha/P1", "beta/P1"), true},
		{"hook cites friction and signal", typedFinding("hook", "alpha/F1", "alpha/G2"), false},
		{"setting cites success", typedFinding("setting", "alpha/S1"), true},
		{"new_skill cites a non-retyping signal", typedFinding("new_skill", "alpha/G2"), true},
		{"new_skill cites a retyping signal", typedFinding("new_skill", "alpha/G1", "alpha/P1"), false},
		{"new_skill cites friction", typedFinding("new_skill", "alpha/F1"), true},
		{"habit cites friction and success", typedFinding("habit", "alpha/F1", "alpha/S1"), false},
		{"habit cites prefs", typedFinding("habit", "alpha/P1"), true},
		{"unknown asset type", typedFinding("blog_post", "alpha/P1"), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := verifyFixture(t, v2Raw(c.finding))
			if c.wantErr && err == nil {
				t.Fatal("want fail-closed error, got nil")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("want no error, got %v", err)
			}
		})
	}
}

func TestVerify2_Audience(t *testing.T) {
	missing := func(assetType string, ids ...string) insights.RawFinding {
		f := typedFinding(assetType, ids...)
		f.Audience = ""
		return f
	}
	bad := v2Finding()
	bad.Audience = "everyone"

	cases := []struct {
		name    string
		finding insights.RawFinding
		wantErr bool
	}{
		{"claude_md_rule without audience", missing("claude_md_rule", "alpha/P1", "alpha/F1"), true},
		{"repo_doc without audience", missing("repo_doc", "alpha/P1", "alpha/F1"), true},
		{"habit without audience", missing("habit", "alpha/F1", "alpha/S1"), false},
		{"invalid audience value", bad, true},
		{"valid audience", v2Finding(), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := verifyFixture(t, v2Raw(c.finding))
			if c.wantErr && err == nil {
				t.Fatal("want fail-closed error, got nil")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("want no error, got %v", err)
			}
		})
	}
}

func TestVerify2_GoOwnedOverwrite(t *testing.T) {
	// insights.RawFinding structurally omits repos/session_count/last_seen/
	// acted_key, so the model cannot supply them at all: the verifier is the
	// only author of all four.
	f := typedFinding("hook", "alpha/G1", "alpha/F1", "beta/F1")
	out, err := verifyFixture(t, v2Raw(f))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	got := out.Findings[0]

	if want := []string{"alpha", "beta"}; !equalStrings(got.Repos, want) {
		t.Errorf("repos = %v, want %v (derived from cited ids' namespaces)", got.Repos, want)
	}
	// alpha/G1 contributes its three member sessions, alpha/F1 re-cites one of
	// them, beta/F1 adds one more.
	if got.SessionCount != 4 {
		t.Errorf("session_count = %d, want 4 (distinct sessions incl. signal members)", got.SessionCount)
	}
	if got.LastSeen != "2026-06-11" {
		t.Errorf("last_seen = %q, want 2026-06-11 (max cited session date)", got.LastSeen)
	}
	if want := ActedKeyV2("hook", f.Statement); got.ActedKey != want {
		t.Errorf("acted_key = %q, want %q", got.ActedKey, want)
	}
}

func TestVerify2_ActedKeyV2IsRepoFree(t *testing.T) {
	statement := "Run the smoke test before calling a task done."
	if ActedKeyV2("hook", statement) == ActedKeyV2("setting", statement) {
		t.Error("acted key must vary with asset type")
	}
	if k := ActedKeyV2("hook", statement); k != ActedKeyV2("hook", "  RUN the smoke  test before calling a task done. ") {
		t.Errorf("acted key must normalize whitespace and case, got %q", k)
	}
	if k := ActedKeyV2("hook", statement); len(k) != 16 {
		t.Errorf("acted key = %q, want 16 hex chars", k)
	}
}

func TestVerify2_Envelope(t *testing.T) {
	out, err := verifyFixture(t, v2Raw(v2Finding()))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if out.SchemaVersion != 2 {
		t.Errorf("schema_version = %d, want 2", out.SchemaVersion)
	}
	if !out.GeneratedAt.Equal(v2GeneratedAt) {
		t.Errorf("generated_at = %v, want %v", out.GeneratedAt, v2GeneratedAt)
	}
	if out.Window.From != "2026-06-01" || out.Window.To != "2026-06-12" {
		t.Errorf("window = %+v, want 2026-06-01..2026-06-12 (union of bundle windows)", out.Window)
	}
	if len(out.Repos) != 2 || out.Repos[0].Key != "alpha" || out.Repos[1].Key != "beta" {
		t.Fatalf("repos = %+v, want alpha then beta", out.Repos)
	}
	if out.Repos[0].SessionCount != 3 || out.Repos[0].AnalyzedCount != 3 {
		t.Errorf("alpha stats = %+v, want session/analyzed 3/3", out.Repos[0])
	}
	// Empty collections must marshal as [] — the show schema types them as
	// arrays, and a nil slice would emit null.
	if out.Dropped == nil {
		t.Error("dropped must be an empty slice, not nil")
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestVerify2_QuotePool(t *testing.T) {
	t.Run("non-verbatim quote dropped with a note", func(t *testing.T) {
		f := v2Finding()
		f.Quotes = []string{
			"always run the smoke test before calling it done", // alpha/P1, cited
			"I paraphrased this one instead of copying it",     // nowhere in the pool
		}
		out, err := verifyFixture(t, v2Raw(f))
		if err != nil {
			t.Fatalf("verify: %v", err)
		}
		if want := []string{"always run the smoke test before calling it done"}; !equalStrings(out.Findings[0].Quotes, want) {
			t.Errorf("quotes = %v, want %v", out.Findings[0].Quotes, want)
		}
		if len(out.Meta.ValidationNotes) != 1 {
			t.Errorf("validation_notes = %v, want exactly one quote-drop note", out.Meta.ValidationNotes)
		}
	})

	t.Run("signal detail line counts as pool", func(t *testing.T) {
		f := typedFinding("hook", "alpha/G1", "alpha/F1")
		f.Quotes = []string{"re-run the review after a rewrite"} // alpha/G1 detail
		out, err := verifyFixture(t, v2Raw(f))
		if err != nil {
			t.Fatalf("verify: %v", err)
		}
		if len(out.Findings[0].Quotes) != 1 {
			t.Errorf("quotes = %v, want the signal detail line kept", out.Findings[0].Quotes)
		}
		if len(out.Meta.ValidationNotes) != 0 {
			t.Errorf("validation_notes = %v, want none", out.Meta.ValidationNotes)
		}
	})

	t.Run("quote from an uncited item is dropped", func(t *testing.T) {
		f := v2Finding()                                           // cites alpha/P1 + beta/P1 only
		f.Quotes = []string{"the build passed but I never ran it"} // alpha/F1, uncited
		out, err := verifyFixture(t, v2Raw(f))
		if err != nil {
			t.Fatalf("verify: %v", err)
		}
		if len(out.Findings[0].Quotes) != 0 {
			t.Errorf("quotes = %v, want empty (pool is cited items only)", out.Findings[0].Quotes)
		}
	})
}

func TestVerify2_StampsMeta(t *testing.T) {
	f := v2Finding()
	f.Quotes = []string{"a quote nobody in the evidence ever said"}
	raw := v2Raw(f)
	raw.Meta = insights.GlobalMetaJSON{Model: "model-authored-lie", ValidationNotes: []string{"model-authored note"}}

	out, err := verifyFixture(t, raw)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if out.Meta.Model != "test-model" {
		t.Errorf("meta.model = %q, want the configured model", out.Meta.Model)
	}
	if len(out.Meta.ValidationNotes) != 1 {
		t.Fatalf("validation_notes = %v, want only the verifier's own note", out.Meta.ValidationNotes)
	}
	if out.Meta.ValidationNotes[0] == "model-authored note" {
		t.Error("model-emitted validation notes must be discarded")
	}
}

func TestVerify2_QuantGuard(t *testing.T) {
	withStatement := v2Finding()
	withStatement.Statement = "Run the smoke test, which was skipped in 3 sessions."

	withTitle := v2Finding()
	withTitle.Title = "Retyped 5 times a week"

	withRationale := v2Finding()
	withRationale.RankRationale = "It cost rework in 40% of the runs."

	withContent := v2Finding()
	withContent.Asset.Content = "Keep at most 4 options in the picker; 3 sessions showed more is unusable."

	droppedRaw := func(summary, reason string) insights.RawGlobalSynthesis {
		raw := v2Raw(v2Finding())
		raw.Dropped = []insights.DroppedJSON{{Summary: summary, Reason: reason, EvidenceIDs: []string{"alpha/S1"}}}
		return raw
	}

	cases := []struct {
		name    string
		raw     insights.RawGlobalSynthesis
		wantErr bool
	}{
		{"number in statement", v2Raw(withStatement), true},
		{"number in title", v2Raw(withTitle), true},
		{"number in rank rationale", v2Raw(withRationale), true},
		{"number in dropped summary", droppedRaw("cost rework 6 times", "already covered"), true},
		{"number in dropped reason", droppedRaw("tooling gripe", "only 2 incidents, both addressed"), true},
		// asset.content is deliberately outside the guard: a deliverable may
		// legitimately contain a bound that is part of the practice.
		{"number in asset content", v2Raw(withContent), false},
		{"clean dropped entry", droppedRaw("tooling gripe", "single incident the user already addressed"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := verifyFixture(t, c.raw)
			if c.wantErr && err == nil {
				t.Fatal("want fail-closed error, got nil")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("want no error, got %v", err)
			}
		})
	}
}

// writeHomeFile writes content to home/rel, creating parents, and returns the
// absolute path.
func writeHomeFile(t *testing.T, home, rel, content string) string {
	t.Helper()
	path := filepath.Join(home, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestVerify2_AdoptedExcerpt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	rulePath := writeHomeFile(t, home, ".claude/CLAUDE.md",
		"# Rules\n\nAlways run the smoke test before calling a task done.\n")

	adopted := func(a insights.AdoptedJSON) insights.RawGlobalSynthesis {
		f := v2Finding()
		f.AlreadyAdopted = a
		return v2Raw(f)
	}

	cases := []struct {
		name        string
		raw         insights.RawGlobalSynthesis
		wantVerdict string
		wantNotes   int
	}{
		{"verbatim excerpt keeps yes", adopted(insights.AdoptedJSON{
			Verdict: "yes", SourcePath: rulePath,
			Excerpt: "Always run the smoke test before calling a task done.",
		}), "yes", 0},
		{"paraphrased excerpt downgrades", adopted(insights.AdoptedJSON{
			Verdict: "yes", SourcePath: rulePath,
			Excerpt: "Always smoke test things before you finish them.",
		}), "unknown", 1},
		{"unreadable source downgrades", adopted(insights.AdoptedJSON{
			Verdict: "yes", SourcePath: filepath.Join(home, ".claude", "absent.md"),
			Excerpt: "Always run the smoke test before calling a task done.",
		}), "unknown", 1},
		{"yes without an excerpt downgrades", adopted(insights.AdoptedJSON{Verdict: "yes"}), "unknown", 1},
		{"verdict no is left alone", adopted(insights.AdoptedJSON{Verdict: "no"}), "no", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := verifyFixture(t, c.raw)
			if err != nil {
				t.Fatalf("verify: %v", err)
			}
			if got := out.Findings[0].AlreadyAdopted.Verdict; got != c.wantVerdict {
				t.Errorf("verdict = %q, want %q", got, c.wantVerdict)
			}
			if got := len(out.Meta.ValidationNotes); got != c.wantNotes {
				t.Errorf("validation_notes = %v, want %d", out.Meta.ValidationNotes, c.wantNotes)
			}
		})
	}
}

// stubRuleDate is the injected git-date lookup: every rule dates to day.
func stubRuleDate(t *testing.T, day string) RuleDateFunc {
	t.Helper()
	d, err := time.Parse("2006-01-02", day)
	if err != nil {
		t.Fatalf("parse %q: %v", day, err)
	}
	return func(string) (time.Time, bool) { return d, true }
}

func TestVerify2_PlacementRecency(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dotfiles := filepath.Join(home, "dotfiles")
	rulePath := writeHomeFile(t, home, "dotfiles/claude/CLAUDE.md",
		"Always open a draft PR before requesting review.\n")
	existingRule := &insights.EscalatedFromJSON{
		SourcePath: rulePath, Excerpt: "Always open a draft PR before requesting review.",
	}

	// The cited evidence's latest session date is 2026-06-05 (alpha/F2).
	placement := func(from *insights.EscalatedFromJSON) insights.RawGlobalSynthesis {
		f := typedFinding("placement_fix", "alpha/P1", "alpha/F2")
		f.EscalatedFrom = from
		return v2Raw(f)
	}
	withDotfiles := insights.Config{SynthesisModel: "test-model", DotfilesRepo: dotfiles}

	t.Run("missing escalated_from fails", func(t *testing.T) {
		if _, err := verifyFixture(t, placement(nil)); err == nil {
			t.Fatal("want fail-closed error, got nil")
		}
	})

	t.Run("excerpt absent from the rule file fails", func(t *testing.T) {
		bogus := &insights.EscalatedFromJSON{SourcePath: rulePath, Excerpt: "Never open a draft PR at all."}
		if _, err := verifyFixture(t, placement(bogus)); err == nil {
			t.Fatal("want fail-closed error, got nil")
		}
	})

	t.Run("unreadable rule file fails", func(t *testing.T) {
		absent := &insights.EscalatedFromJSON{
			SourcePath: filepath.Join(dotfiles, "claude", "absent.md"),
			Excerpt:    "Always open a draft PR before requesting review.",
		}
		if _, err := verifyFixture(t, placement(absent)); err == nil {
			t.Fatal("want fail-closed error, got nil")
		}
	})

	t.Run("rule newer than every cited session removes the finding", func(t *testing.T) {
		out, err := verifyGlobal(placement(existingRule), v2Bundles(), withDotfiles, v2GeneratedAt, stubRuleDate(t, "2026-07-01"))
		if err != nil {
			t.Fatalf("verify: %v", err)
		}
		if len(out.Findings) != 0 {
			t.Errorf("findings = %d, want the escalation removed (the rule never had a chance to work)", len(out.Findings))
		}
		if len(out.Meta.ValidationNotes) != 1 {
			t.Errorf("validation_notes = %v, want one removal note", out.Meta.ValidationNotes)
		}
	})

	t.Run("a cited session postdating the rule keeps it", func(t *testing.T) {
		out, err := verifyGlobal(placement(existingRule), v2Bundles(), withDotfiles, v2GeneratedAt, stubRuleDate(t, "2026-05-01"))
		if err != nil {
			t.Fatalf("verify: %v", err)
		}
		if len(out.Findings) != 1 {
			t.Fatalf("findings = %d, want the escalation kept", len(out.Findings))
		}
		if len(out.Meta.ValidationNotes) != 0 {
			t.Errorf("validation_notes = %v, want none", out.Meta.ValidationNotes)
		}
	})

	t.Run("no dotfiles repo skips the recency check", func(t *testing.T) {
		out, err := verifyGlobal(placement(existingRule), v2Bundles(), v2Config(), v2GeneratedAt, stubRuleDate(t, "2026-07-01"))
		if err != nil {
			t.Fatalf("verify: %v", err)
		}
		if len(out.Findings) != 1 {
			t.Fatalf("findings = %d, want the escalation kept (recency skipped)", len(out.Findings))
		}
		if len(out.Meta.ValidationNotes) != 0 {
			t.Errorf("validation_notes = %v, want none", out.Meta.ValidationNotes)
		}
	})

	t.Run("undatable rule keeps the finding with a note", func(t *testing.T) {
		undatable := func(string) (time.Time, bool) { return time.Time{}, false }
		out, err := verifyGlobal(placement(existingRule), v2Bundles(), withDotfiles, v2GeneratedAt, undatable)
		if err != nil {
			t.Fatalf("verify: %v", err)
		}
		if len(out.Findings) != 1 {
			t.Fatalf("findings = %d, want the escalation kept", len(out.Findings))
		}
		if len(out.Meta.ValidationNotes) != 1 {
			t.Errorf("validation_notes = %v, want one skipped-check note", out.Meta.ValidationNotes)
		}
	})

	t.Run("removal compacts the remaining ranks", func(t *testing.T) {
		stale := typedFinding("placement_fix", "alpha/P1", "alpha/F2")
		stale.Rank = 1
		stale.EscalatedFrom = existingRule
		survivor := v2Finding()
		survivor.Rank = 2

		out, err := verifyGlobal(v2Raw(stale, survivor), v2Bundles(), withDotfiles, v2GeneratedAt, stubRuleDate(t, "2026-07-01"))
		if err != nil {
			t.Fatalf("verify: %v", err)
		}
		if len(out.Findings) != 1 {
			t.Fatalf("findings = %d, want only the survivor", len(out.Findings))
		}
		if out.Findings[0].Rank != 1 {
			t.Errorf("rank = %d, want 1 (ranks re-normalized after removal)", out.Findings[0].Rank)
		}
	})
}

func TestVerify2_GitRuleDate(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.email=dev@example.com", "-c", "user.name=dev"}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE=2026-05-04T10:00:00Z", "GIT_COMMITTER_DATE=2026-05-04T10:00:00Z")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init")
	if err := os.WriteFile(filepath.Join(repo, "CLAUDE.md"), []byte("a rule\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	git("add", "CLAUDE.md")
	git("commit", "-m", "add rule")

	lookup := gitRuleDate(repo)
	got, ok := lookup(filepath.Join(repo, "CLAUDE.md"))
	if !ok {
		t.Fatal("want a date for a tracked rule file")
	}
	if got.UTC().Format("2006-01-02") != "2026-05-04" {
		t.Errorf("date = %v, want 2026-05-04", got)
	}
	if _, ok := lookup(filepath.Join(repo, "untracked.md")); ok {
		t.Error("want ok=false for an untracked path")
	}
	if _, ok := lookup("/etc/hosts"); ok {
		t.Error("want ok=false for a path outside the dotfiles repo")
	}
}

func TestVerify2_PathNormalization(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// The existing rule's own text carries an absolute home path, so the
	// excerpt only matches the file if $HOME is still un-rewritten when the
	// existence check runs.
	ruleLine := "Write scratch files under " + home + "/scratch."
	rulePath := writeHomeFile(t, home, "dotfiles/claude/CLAUDE.md", ruleLine+"\n")

	f := typedFinding("placement_fix", "alpha/P1", "alpha/F2")
	f.Asset.Target = filepath.Join(home, "Developer", "alpha", "CLAUDE.md")
	f.AlreadyAdopted = insights.AdoptedJSON{
		Verdict: "yes", SourcePath: filepath.Join(home, ".claude", "absent.md"), Excerpt: "not there",
	}
	f.EscalatedFrom = &insights.EscalatedFromJSON{SourcePath: rulePath, Excerpt: ruleLine}

	out, err := verifyFixture(t, v2Raw(f))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	got := out.Findings[0]
	if got.Asset.Target != "~/Developer/alpha/CLAUDE.md" {
		t.Errorf("asset.target = %q, want ~-relative", got.Asset.Target)
	}
	if got.AlreadyAdopted.SourcePath != "~/.claude/absent.md" {
		t.Errorf("already_adopted.source_path = %q, want ~-relative", got.AlreadyAdopted.SourcePath)
	}
	if got.EscalatedFrom.SourcePath != "~/dotfiles/claude/CLAUDE.md" {
		t.Errorf("escalated_from.source_path = %q, want ~-relative", got.EscalatedFrom.SourcePath)
	}
	if got.EscalatedFrom.Excerpt != "Write scratch files under ~/scratch." {
		t.Errorf("escalated_from.excerpt = %q, want the home path rewritten after the existence check", got.EscalatedFrom.Excerpt)
	}
	for _, n := range out.Meta.ValidationNotes {
		if strings.Contains(n, home) {
			t.Errorf("validation note carries an absolute home path: %q", n)
		}
	}

	t.Run("dollar-home target", func(t *testing.T) {
		d := v2Finding()
		d.Asset.Target = "$HOME/.claude/CLAUDE.md"
		out, err := verifyFixture(t, v2Raw(d))
		if err != nil {
			t.Fatalf("verify: %v", err)
		}
		if out.Findings[0].Asset.Target != "~/.claude/CLAUDE.md" {
			t.Errorf("asset.target = %q, want ~/.claude/CLAUDE.md", out.Findings[0].Asset.Target)
		}
	})
}

func TestVerify2_PrivacyScan(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	statementLeak := v2Finding()
	statementLeak.Statement = "Keep scratch files under /Users/dev/Developer/alpha instead of the repo."

	contentLeak := v2Finding()
	contentLeak.Asset.Content = "Write logs to /Users/dev/logs before reporting."

	titleLeak := v2Finding()
	titleLeak.Title = "Use $HOME for scratch"

	droppedLeak := v2Raw(v2Finding())
	droppedLeak.Dropped = []insights.DroppedJSON{{
		Summary: "scratch files land in /Users/dev/tmp", Reason: "infrastructure rather than workflow",
		EvidenceIDs: []string{"alpha/S1"},
	}}

	// An excerpt is a verbatim copy of a real config file, which legitimately
	// contains absolute paths: exempt from the blocking scan.
	excerptLine := "Cache lives in /Users/dev/.cache/agent-insights."
	excerptPath := writeHomeFile(t, home, ".claude/settings-note.md", excerptLine+"\n")
	excerptExempt := v2Finding()
	excerptExempt.AlreadyAdopted = insights.AdoptedJSON{Verdict: "yes", SourcePath: excerptPath, Excerpt: excerptLine}

	cases := []struct {
		name    string
		raw     insights.RawGlobalSynthesis
		wantErr bool
	}{
		{"home path in statement", v2Raw(statementLeak), true},
		{"home path in asset content", v2Raw(contentLeak), true},
		{"home variable in title", v2Raw(titleLeak), true},
		{"home path in dropped summary", droppedLeak, true},
		{"home path in an excerpt", v2Raw(excerptExempt), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := verifyFixture(t, c.raw)
			if c.wantErr && err == nil {
				t.Fatal("want fail-closed error, got nil")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("want no error, got %v", err)
			}
		})
	}
}

// TestVerify2_DoesNotMutateRaw guards the eval harness's re-verification path:
// a cached raw synthesis must verify identically every time, which it cannot
// if the first pass rewrites paths in place.
func TestVerify2_DoesNotMutateRaw(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	rulePath := writeHomeFile(t, home, "dotfiles/CLAUDE.md", "Open a draft PR before requesting review.\n")

	f := typedFinding("placement_fix", "alpha/P1", "alpha/F2")
	f.Asset.Target = filepath.Join(home, "Developer", "alpha", "CLAUDE.md")
	f.Quotes = []string{"a quote nobody in the evidence ever said"}
	f.EscalatedFrom = &insights.EscalatedFromJSON{
		SourcePath: rulePath, Excerpt: "Open a draft PR before requesting review.",
	}
	raw := v2Raw(f)

	if _, err := verifyFixture(t, raw); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if got := raw.Findings[0].EscalatedFrom.SourcePath; got != rulePath {
		t.Errorf("raw escalated_from.source_path = %q, want it untouched", got)
	}
	if got := raw.Findings[0].Asset.Target; !strings.HasPrefix(got, home) {
		t.Errorf("raw asset.target = %q, want it untouched", got)
	}
	if len(raw.Findings[0].Quotes) != 1 {
		t.Errorf("raw quotes = %v, want them untouched", raw.Findings[0].Quotes)
	}
}

func TestVerify2_EmptyCitations_Fails(t *testing.T) {
	noEvidence := v2Finding()
	noEvidence.EvidenceIDs = nil

	emptyDropped := v2Raw(v2Finding())
	emptyDropped.Dropped = []insights.DroppedJSON{{
		Summary: "tooling gripe", Reason: "single incident the user already addressed",
	}}

	cases := []struct {
		name string
		raw  insights.RawGlobalSynthesis
	}{
		{"finding cites nothing", v2Raw(noEvidence)},
		{"dropped entry cites nothing", emptyDropped},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := verifyFixture(t, c.raw); err == nil {
				t.Fatal("want fail-closed error, got nil")
			}
		})
	}
}

func TestVerify2_EscalatedFromCompleteness(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	rulePath := writeHomeFile(t, home, "dotfiles/CLAUDE.md", "Open a draft PR before requesting review.\n")

	placement := func(from insights.EscalatedFromJSON) insights.RawGlobalSynthesis {
		f := typedFinding("placement_fix", "alpha/P1", "alpha/F2")
		f.EscalatedFrom = &from
		return v2Raw(f)
	}
	cases := []struct {
		name string
		raw  insights.RawGlobalSynthesis
	}{
		{"empty excerpt", placement(insights.EscalatedFromJSON{SourcePath: rulePath})},
		{"empty source path", placement(insights.EscalatedFromJSON{Excerpt: "Open a draft PR before requesting review."})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := verifyFixture(t, c.raw); err == nil {
				t.Fatal("want fail-closed error, got nil (an empty excerpt verifies trivially)")
			}
		})
	}
}

func TestVerify2_AbsorbedSchemaConstraints(t *testing.T) {
	t.Run("empty title fails", func(t *testing.T) {
		f := v2Finding()
		f.Title = ""
		if _, err := verifyFixture(t, v2Raw(f)); err == nil {
			t.Fatal("want fail-closed error, got nil")
		}
	})

	t.Run("wrong schema version fails", func(t *testing.T) {
		for _, version := range []int{0, 1, 3} {
			raw := v2Raw(v2Finding())
			raw.SchemaVersion = version
			if _, err := verifyFixture(t, raw); err == nil {
				t.Errorf("schema_version %d: want fail-closed error, got nil", version)
			}
		}
	})

	t.Run("invalid adopted verdict fails", func(t *testing.T) {
		for _, verdict := range []string{"", "Yes", "adopted", "maybe"} {
			f := v2Finding()
			f.AlreadyAdopted = insights.AdoptedJSON{Verdict: verdict}
			if _, err := verifyFixture(t, v2Raw(f)); err == nil {
				t.Errorf("verdict %q: want fail-closed error, got nil", verdict)
			}
		}
	})

	t.Run("more than three quotes trimmed with a note", func(t *testing.T) {
		f := typedFinding("hook", "alpha/G1", "alpha/F1", "alpha/F2", "beta/F1")
		f.Quotes = []string{
			"re-run the review after a rewrite",    // alpha/G1 detail
			"the build passed but I never ran it",  // alpha/F1
			"we had to redo the whole review pass", // alpha/F2
			"nothing stopped me committing that",   // beta/F1
		}
		out, err := verifyFixture(t, v2Raw(f))
		if err != nil {
			t.Fatalf("verify: %v", err)
		}
		got := out.Findings[0].Quotes
		if want := f.Quotes[:3]; !equalStrings(got, want) {
			t.Errorf("quotes = %v, want the first three %v", got, want)
		}
		if len(out.Meta.ValidationNotes) != 1 {
			t.Errorf("validation_notes = %v, want one trim note", out.Meta.ValidationNotes)
		}
	})
}

func TestVerify2_FindingsSortedByRank(t *testing.T) {
	ranked := func(rank int, name string) insights.RawFinding {
		f := v2Finding()
		f.Rank = rank
		f.Title = "Rule " + name
		f.Statement = "Rule " + name + " must hold before a task is done."
		return f
	}
	out, err := verifyFixture(t, v2Raw(ranked(3, "c"), ranked(1, "a"), ranked(2, "b")))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	var titles []string
	for i, f := range out.Findings {
		if f.Rank != i+1 {
			t.Errorf("findings[%d].rank = %d, want %d", i, f.Rank, i+1)
		}
		titles = append(titles, f.Title)
	}
	if want := []string{"Rule a", "Rule b", "Rule c"}; !equalStrings(titles, want) {
		t.Errorf("titles = %v, want %v (array ordered by rank)", titles, want)
	}
}
