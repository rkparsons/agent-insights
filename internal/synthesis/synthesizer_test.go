package synthesis

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
	"github.com/rkparsons/agent-insights/skills"
)

func TestWrapClaudeExit(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 3")
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected exit 3 to error")
	}
	wrapped := wrapClaudeExit([]byte("boom from stdout"), err)
	if wrapped == nil {
		t.Fatal("expected a wrapped error")
	}
	if !strings.Contains(wrapped.Error(), "exit 3") || !strings.Contains(wrapped.Error(), "boom from stdout") {
		t.Fatalf("wrapped error = %q, want it to mention exit 3 and stdout tail", wrapped.Error())
	}

	plain := errors.New("not an exit error")
	if got := wrapClaudeExit(nil, plain); got != plain {
		t.Fatalf("non-ExitError must pass through unchanged, got %v", got)
	}
}

// globalTestBundles is the two-repo evidence the global run synthesizes over.
// Fixture repo keys are synthetic (alpha/beta), never real ones.
func globalTestBundles() map[string]EvidenceBundle {
	return map[string]EvidenceBundle{
		"alpha": {Repo: "alpha", SessionCount: 12, AnalyzedCount: 12, From: "2026-05-02", To: "2026-08-01",
			SessionDates: map[string]string{"00000000-0000-4000-8000-000000000001": "2026-05-02"}},
		"beta": {Repo: "beta", SessionCount: 7, AnalyzedCount: 7, From: "2026-04-10", To: "2026-07-20"},
	}
}

func globalTestConfig() insights.Config {
	return insights.Config{
		Repos:          []string{"/Users/dev/Developer/alpha", "/Users/dev/Developer/beta"},
		SynthesisModel: "claude-fable-5",
		DotfilesRepo:   "/Users/dev/Developer/dotfiles",
	}
}

func TestGlobalManifestStatesWindowBundlesAndAssets(t *testing.T) {
	got := buildGlobalManifest("/Users/dev/.claude", globalTestConfig(), globalTestBundles())
	for _, want := range []string{
		"# Synthesis manifest",
		"Window: 2026-04-10 → 2026-08-01", // global envelope, both repos
		"alpha-bundle.json (12 sessions)",
		"beta-bundle.json (7 sessions)",
		"global CLAUDE.md /Users/dev/.claude/CLAUDE.md",
		"alpha: /Users/dev/Developer/alpha",
		"beta: /Users/dev/Developer/beta",
		"skills dir /Users/dev/.claude/skills",
		"settings /Users/dev/.claude/settings.json",
		"dotfiles git history /Users/dev/Developer/dotfiles",
		"Write RawGlobalSynthesis JSON to ./synthesis.json",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("manifest missing %q:\n%s", want, got)
		}
	}
	// The window bounds are the only dates the model may see.
	if strings.Contains(got, "2026-05-02") || strings.Contains(got, "2026-07-20") {
		t.Errorf("manifest leaked a per-repo bound as a date the model can attribute:\n%s", got)
	}
}

func TestGlobalManifestDegradesWithoutDotfilesOrRepoRoots(t *testing.T) {
	got := buildGlobalManifest("/Users/dev/.claude", insights.Config{}, globalTestBundles())
	if !strings.Contains(got, "dotfiles git history unavailable") {
		t.Errorf("unset dotfiles_repo must read unavailable:\n%s", got)
	}
	// A repo with no configured checkout is named as unavailable, never
	// omitted: a missing key reads as "this repo has no assets" rather than
	// "its assets cannot be read from here".
	for _, want := range []string{"alpha: unavailable", "beta: unavailable"} {
		if !strings.Contains(got, want) {
			t.Errorf("manifest missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "alpha: /") {
		t.Errorf("unconfigured repos must not invent roots:\n%s", got)
	}
}

func flagValue(args []string, flag string) string {
	i := slices.Index(args, flag)
	if i < 0 || i+1 >= len(args) {
		return ""
	}
	return args[i+1]
}

func TestNewGlobalSynthesizeCommandFlags(t *testing.T) {
	cmd, err := newGlobalSynthesizeCommand(context.Background(), "claude-fable-5", "/tmp/cfg", "/tmp/work")
	if err != nil {
		t.Fatal(err)
	}
	if got := flagValue(cmd.Args, "--model"); got != "claude-fable-5" {
		t.Errorf("--model = %q, want the configured model verbatim", got)
	}
	if !slices.Contains(cmd.Args, "--no-session-persistence") {
		t.Error("nested synthesis must not persist its own session transcript")
	}
	// v2's payload is a file the skill writes, not stdout structured output.
	if slices.Contains(cmd.Args, "--json-schema") {
		t.Error("--json-schema must be dropped: the contract is <workdir>/synthesis.json")
	}
	allowed := flagValue(cmd.Args, "--allowed-tools")
	for _, want := range []string{"Read", "Glob", "Grep", "Bash(git log:*)", "Write(synthesis.json)"} {
		if !strings.Contains(allowed, want) {
			t.Errorf("--allowed-tools %q missing %q", allowed, want)
		}
	}
	denied := flagValue(cmd.Args, "--disallowed-tools")
	for _, want := range []string{"Edit", "Agent"} {
		if !strings.Contains(denied, want) {
			t.Errorf("--disallowed-tools %q missing %q", denied, want)
		}
	}
	// A blanket Write denial would make the skill's central instruction unactionable.
	if strings.Contains(denied, "Write") {
		t.Errorf("--disallowed-tools %q must not deny Write outright", denied)
	}
	if cmd.Dir != "/tmp/work" {
		t.Errorf("Dir = %q", cmd.Dir)
	}
	if !slices.Contains(cmd.Env, "CLAUDE_CONFIG_DIR=/tmp/cfg") {
		t.Error("env missing pinned CLAUDE_CONFIG_DIR")
	}
}

// An empty model would reach the CLI as a bare flag value; the config layer
// defaults it, so an empty one here is a wiring bug, never a reason to
// substitute a model of our own.
func TestNewGlobalSynthesizeCommandRejectsEmptyModelAndWorkDir(t *testing.T) {
	if _, err := newGlobalSynthesizeCommand(context.Background(), "", "", "/tmp/work"); err == nil {
		t.Error("expected an error for an empty model")
	}
	if _, err := newGlobalSynthesizeCommand(context.Background(), "m", "", ""); err == nil {
		t.Error("expected an error for an empty workDir")
	}
}

// A pinned config dir replaces ~/.claude for the nested claude, so the assets
// the manifest names must come from there too.
func TestGlobalManifestFollowsThePinnedConfigDir(t *testing.T) {
	pinned, ok := NewClaudeGlobalSynthesizerPinned(globalTestConfig(), "/tmp/frozen", "/tmp/work").(claudeGlobalSynthesizer)
	if !ok {
		t.Fatal("expected a claudeGlobalSynthesizer")
	}
	if got := buildGlobalManifest(pinned.globalRoot, pinned.cfg, globalTestBundles()); !strings.Contains(got, "global CLAUDE.md /tmp/frozen/CLAUDE.md") {
		t.Errorf("manifest must name the pinned config dir:\n%s", got)
	}
	live, _ := NewClaudeGlobalSynthesizer(globalTestConfig())("/tmp/work").(claudeGlobalSynthesizer)
	if live.globalRoot != globalAssetRoot() {
		t.Errorf("globalRoot = %q, want the live ~/.claude when unpinned", live.globalRoot)
	}
	if live.workDir != "/tmp/work" || live.cfg.SynthesisModel != globalTestConfig().SynthesisModel {
		t.Errorf("factory built %+v, want the run's workdir and the caller's config", live)
	}
}

// fakeGlobalCLI builds a synthesizer over a workdir with the skills materialized,
// standing in for the nested claude with a func that sees the same cwd the real
// CLI would: cli reports what the workdir held when it ran.
func fakeGlobalCLI(t *testing.T, cli func(workDir string) ([]byte, error)) (claudeGlobalSynthesizer, string) {
	t.Helper()
	workDir, cleanup, err := skills.TempWorkdir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	s := claudeGlobalSynthesizer{cfg: globalTestConfig(), workDir: workDir, globalRoot: "/Users/dev/.claude"}
	s.run = func(ctx context.Context) ([]byte, error) { return cli(workDir) }
	return s, workDir
}

const fakeRawSynthesis = `{"schema_version":2,"findings":[{"rank":1,"title":"One rule","evidence_ids":["alpha/F1"]}],"dropped":[]}`

func TestSynthesizeGlobalPreparesWorkdirAndDecodesOutput(t *testing.T) {
	var sawWorkdir []string
	s, workDir := fakeGlobalCLI(t, func(dir string) ([]byte, error) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			sawWorkdir = append(sawWorkdir, e.Name())
		}
		return []byte(`{"is_error":false,"result":"wrote synthesis.json"}`),
			os.WriteFile(filepath.Join(dir, "synthesis.json"), []byte(fakeRawSynthesis), 0o644)
	})

	raw, err := s.SynthesizeGlobal(context.Background(), globalTestBundles())
	if err != nil {
		t.Fatalf("SynthesizeGlobal: %v", err)
	}
	if raw.SchemaVersion != 2 || len(raw.Findings) != 1 || raw.Findings[0].Title != "One rule" {
		t.Errorf("raw = %+v, want the decoded synthesis.json", raw)
	}
	for _, want := range []string{"alpha-bundle.json", "beta-bundle.json", "manifest.md"} {
		if !slices.Contains(sawWorkdir, want) {
			t.Errorf("workdir at CLI time = %v, missing %q", sawWorkdir, want)
		}
	}
	skill := filepath.Join(workDir, ".claude", "skills", skills.SynthesisSkill, "SKILL.md")
	if _, err := os.Stat(skill); err != nil {
		t.Errorf("workdir must carry the materialized skill: %v", err)
	}
	// The bundle files the model reads carry no per-session dates (Task 2).
	data, err := os.ReadFile(filepath.Join(workDir, "alpha-bundle.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "session_dates") {
		t.Error("bundle file leaked session_dates to the model")
	}
}

func TestSynthesizeGlobalErrorsWhenSkillWroteNoOutput(t *testing.T) {
	s, _ := fakeGlobalCLI(t, func(string) ([]byte, error) {
		return []byte(`{"is_error":false,"result":"nothing to do"}`), nil
	})
	_, err := s.SynthesizeGlobal(context.Background(), globalTestBundles())
	if err == nil {
		t.Fatal("expected an error when no synthesis.json was written")
	}
	if !strings.Contains(err.Error(), "synthesis.json") || !strings.Contains(err.Error(), "nothing to do") {
		t.Errorf("error = %q, want it to name the missing file and the CLI's own report", err)
	}
}

// TestSynthesizeGlobalErrorTextIsHomeFree: the CLI's own report lands verbatim
// in the run state and the TUI's error badge, so it gets the same home-path
// rewrite as every other string that outlives the process.
func TestSynthesizeGlobalErrorTextIsHomeFree(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	s, _ := fakeGlobalCLI(t, func(string) ([]byte, error) {
		body, err := json.Marshal(map[string]any{
			"is_error": true,
			"result":   "cannot read " + home + "/.claude/CLAUDE.md or $HOME/settings.json",
		})
		if err != nil {
			t.Fatal(err)
		}
		return body, nil
	})
	_, err := s.SynthesizeGlobal(context.Background(), globalTestBundles())
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), home) || strings.Contains(err.Error(), "$HOME") {
		t.Errorf("error = %q, want home paths rewritten to ~", err)
	}
	if !strings.Contains(err.Error(), "~/.claude/CLAUDE.md") {
		t.Errorf("error = %q, want the report preserved with ~ paths", err)
	}
}

// TestSynthesizeGlobalFailsClosedOnEnvelopeError: the CLI reports a refusal or
// an aborted turn in its envelope, sometimes on a zero exit and sometimes
// alongside a partially-written output file — which would otherwise be
// accepted as a successful run.
func TestSynthesizeGlobalFailsClosedOnEnvelopeError(t *testing.T) {
	s, _ := fakeGlobalCLI(t, func(dir string) ([]byte, error) {
		return []byte(`{"is_error":true,"result":"Not logged in"}`),
			os.WriteFile(filepath.Join(dir, "synthesis.json"), []byte(fakeRawSynthesis), 0o644)
	})
	_, err := s.SynthesizeGlobal(context.Background(), globalTestBundles())
	if err == nil {
		t.Fatal("expected an error when the CLI envelope reports one")
	}
	if !strings.Contains(err.Error(), "Not logged in") {
		t.Errorf("error = %q, want the CLI's own report", err)
	}
}

// TestSynthesizeGlobalRequiresMaterializedSkill: the nested claude resolves the
// skill from its cwd, so a workdir without it would run the model with no
// contract at all and burn the whole deadline.
func TestSynthesizeGlobalRequiresMaterializedSkill(t *testing.T) {
	s := claudeGlobalSynthesizer{cfg: globalTestConfig(), workDir: t.TempDir(),
		run: func(context.Context) ([]byte, error) {
			t.Fatal("the CLI must not be invoked without the materialized skill")
			return nil, nil
		}}
	_, err := s.SynthesizeGlobal(context.Background(), globalTestBundles())
	if err == nil || !strings.Contains(err.Error(), skills.SynthesisSkill) {
		t.Errorf("error = %v, want it to name the missing skill", err)
	}
}

// A previous call's output must never be mistaken for this one's.
func TestSynthesizeGlobalIgnoresStaleOutput(t *testing.T) {
	s, workDir := fakeGlobalCLI(t, func(string) ([]byte, error) { return []byte(`{"is_error":false}`), nil })
	stale := filepath.Join(workDir, "synthesis.json")
	if err := os.WriteFile(stale, []byte(fakeRawSynthesis), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SynthesizeGlobal(context.Background(), globalTestBundles()); err == nil {
		t.Fatal("expected an error, not the stale output from an earlier run")
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale synthesis.json must be cleared before the run: %v", err)
	}
}

func TestSynthesizeGlobalErrorsOnInvalidOutput(t *testing.T) {
	for name, body := range map[string]string{
		"garbage": "not json at all",
		"empty":   "",
		"null":    "null",
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := fakeGlobalCLI(t, func(dir string) ([]byte, error) {
				return []byte(`{"is_error":false}`), os.WriteFile(filepath.Join(dir, "synthesis.json"), []byte(body), 0o644)
			})
			if _, err := s.SynthesizeGlobal(context.Background(), globalTestBundles()); err == nil {
				t.Fatalf("expected an error for %s synthesis.json", name)
			}
		})
	}
}

func TestSynthesizeGlobalSurfacesCommandErrorVerbatim(t *testing.T) {
	s, _ := fakeGlobalCLI(t, func(string) ([]byte, error) {
		return nil, errors.New("claude exit 1: unknown model claude-nope-9")
	})
	_, err := s.SynthesizeGlobal(context.Background(), globalTestBundles())
	if err == nil || !strings.Contains(err.Error(), "unknown model claude-nope-9") {
		t.Errorf("error = %v, want the CLI's failure passed through with no model fallback", err)
	}
}

func TestSynthesizeGlobalRefusesEmptyWorkdirOrBundles(t *testing.T) {
	ran := false
	s, _ := fakeGlobalCLI(t, func(string) ([]byte, error) { ran = true; return nil, nil })
	if _, err := s.SynthesizeGlobal(context.Background(), nil); err == nil {
		t.Error("expected an error with no bundles to synthesize")
	}
	if ran {
		t.Error("a bundle-less run must not reach the CLI")
	}
	empty := s
	empty.workDir = ""
	if _, err := empty.SynthesizeGlobal(context.Background(), globalTestBundles()); err == nil {
		t.Error("expected an error for an empty workDir")
	}
}

// The run must be bounded even if a caller forgets: an unbounded call would
// outlive the spend ceiling the CLI layer sets.
func TestSynthesizeGlobalBoundsAnUndeadlinedContext(t *testing.T) {
	var deadline time.Time
	s, _ := fakeGlobalCLI(t, func(dir string) ([]byte, error) {
		return nil, errors.New("stop")
	})
	inner := s.run
	s.run = func(ctx context.Context) ([]byte, error) {
		deadline, _ = ctx.Deadline()
		return inner(ctx)
	}
	if _, err := s.SynthesizeGlobal(context.Background(), globalTestBundles()); err == nil {
		t.Fatal("expected the fake CLI's error")
	}
	if deadline.IsZero() || time.Until(deadline) > DefaultGlobalTimeout {
		t.Errorf("deadline = %v, want one within %v", deadline, DefaultGlobalTimeout)
	}
}
