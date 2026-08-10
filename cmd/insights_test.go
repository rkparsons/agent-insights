package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
	"github.com/rkparsons/agent-insights/internal/synthesis"
)

func TestParseAnalyzeArgs(t *testing.T) {
	cases := []struct {
		args      []string
		wantTgt   string
		wantForce bool
		wantErr   bool
	}{
		{[]string{"sess-1"}, "sess-1", false, false},
		{[]string{"sess-1", "--force"}, "sess-1", true, false},
		{[]string{"--force", "sess-1"}, "sess-1", true, false},
		{nil, "", false, true},
		{[]string{"sess-1", "sess-2"}, "", false, true},
		{[]string{"--bogus-flag"}, "", false, true},
		{[]string{"--dry-run"}, "", false, true}, // analyze has no --dry-run
	}
	for _, c := range cases {
		target, opts, err := parseAnalyzeArgs(c.args)
		if (err != nil) != c.wantErr {
			t.Errorf("%v: err=%v wantErr=%v", c.args, err, c.wantErr)
			continue
		}
		if c.wantErr {
			continue
		}
		if target != c.wantTgt || opts.Force != c.wantForce {
			t.Errorf("%v: target=%q force=%v, want target=%q force=%v", c.args, target, opts.Force, c.wantTgt, c.wantForce)
		}
		if opts.MinAssistantTurns != insights.DefaultMinAssistantTurns || opts.Timeout != 10*time.Minute {
			t.Errorf("%v: defaults not applied: %+v", c.args, opts)
		}
	}
}

func TestParseBackfillArgs(t *testing.T) {
	cases := []struct {
		args         []string
		wantForce    bool
		wantDry      bool
		wantTh       int
		wantTimeout  time.Duration
		wantQuietFor time.Duration
		wantErr      bool
	}{
		{nil, false, false, 5, 10 * time.Minute, 0, false},
		{[]string{"--force", "--threshold", "3"}, true, false, 3, 10 * time.Minute, 0, false},
		{[]string{"--dry-run"}, false, true, 5, 10 * time.Minute, 0, false},
		{[]string{"--quiet-for", "24h"}, false, false, 5, 10 * time.Minute, 24 * time.Hour, false},
		{[]string{"--timeout", "5m"}, false, false, 5, 5 * time.Minute, 0, false},
		{[]string{"sess-1"}, false, false, 0, 0, 0, true}, // backfill takes no positional args
		{[]string{"--bogus-flag"}, false, false, 0, 0, 0, true},
		{[]string{"--threshold"}, false, false, 0, 0, 0, true},
		{[]string{"--timeout"}, false, false, 0, 0, 0, true},
		{[]string{"--quiet-for"}, false, false, 0, 0, 0, true},
		{[]string{"--quiet-for", "bogus"}, false, false, 0, 0, 0, true},
		{[]string{"--retry-errored"}, false, false, 0, 0, 0, true}, // removed flag
	}
	for _, c := range cases {
		opts, err := parseBackfillArgs(c.args)
		if (err != nil) != c.wantErr {
			t.Errorf("%v: err=%v wantErr=%v", c.args, err, c.wantErr)
			continue
		}
		if c.wantErr {
			continue
		}
		if opts.Force != c.wantForce || opts.DryRun != c.wantDry || opts.MinAssistantTurns != c.wantTh {
			t.Errorf("%v: force=%v dry=%v th=%d", c.args, opts.Force, opts.DryRun, opts.MinAssistantTurns)
		}
		if opts.Timeout != c.wantTimeout {
			t.Errorf("%v: timeout=%v want=%v", c.args, opts.Timeout, c.wantTimeout)
		}
		if opts.QuietFor != c.wantQuietFor {
			t.Errorf("%v: quietFor=%v want=%v", c.args, opts.QuietFor, c.wantQuietFor)
		}
	}
}

func TestParseSynthesizeArgs(t *testing.T) {
	o, err := parseSynthesizeArgs([]string{"--repo", "alpha", "--min-sessions", "5", "--dry-run"})
	if err != nil {
		t.Fatal(err)
	}
	if o.Repo != "alpha" || o.MinSessions != 5 || !o.DryRun {
		t.Errorf("parsed = %+v", o)
	}
	if _, err := parseSynthesizeArgs([]string{"--bogus"}); err == nil {
		t.Error("expected error on unknown flag")
	}
}

func TestParseSynthesizeArgsDue(t *testing.T) {
	o, err := parseSynthesizeArgs([]string{"--due"})
	if err != nil {
		t.Fatal(err)
	}
	if !o.Due {
		t.Errorf("parsed = %+v, want Due=true", o)
	}
}

func TestParseSynthesizeArgsLog(t *testing.T) {
	o, err := parseSynthesizeArgs([]string{"--log", "/tmp/s.log"})
	if err != nil {
		t.Fatal(err)
	}
	if o.LogPath != "/tmp/s.log" {
		t.Errorf("parsed = %+v, want LogPath=/tmp/s.log", o)
	}
}

func TestParseEnrichArgs(t *testing.T) {
	opts, err := parseEnrichArgs([]string{"--repo", "alpha", "--dry-run", "--timeout", "5m"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Repo != "alpha" || !opts.DryRun || opts.Timeout != 5*time.Minute {
		t.Errorf("opts = %+v", opts)
	}
	if _, err := parseEnrichArgs([]string{"--bogus"}); err == nil {
		t.Error("expected error for unknown flag")
	}
	if _, err := parseEnrichArgs([]string{"--timeout"}); err == nil {
		t.Error("expected error for missing value")
	}
}

// TestRunStatusJSON runs `insights status --json` end to end against a temp
// store and checks the stdout payload parses as StatusJSON with the
// contract's schema_version. No analyses/syntheses exist in the temp store,
// so this also exercises the "empty store" path (LoadAnalyses on a missing
// dir, no run state file).
func TestRunStatusJSON(t *testing.T) {
	t.Setenv("AGENT_INSIGHTS_DIR", t.TempDir())
	t.Setenv("AGENT_INSIGHTS_CONFIG", filepath.Join(t.TempDir(), "nonexistent.yaml"))

	out := captureStdout(t, func() {
		runStatus(insights.Config{CadenceDays: 7, MinSessions: 10}, []string{"--json"})
	})

	var status insights.StatusJSON
	if err := json.Unmarshal(out, &status); err != nil {
		t.Fatalf("status --json did not parse: %v\noutput: %s", err, out)
	}
	if status.SchemaVersion != insights.ContractVersion {
		t.Errorf("schema_version = %d, want %d", status.SchemaVersion, insights.ContractVersion)
	}
	if status.Running {
		t.Error("Running = true against an empty store with no lock held")
	}
	if status.StoreRoot != insights.InsightsDir() {
		t.Errorf("store_root = %q, want %q", status.StoreRoot, insights.InsightsDir())
	}
	if status.DueRepos == nil || status.ActedKeys == nil {
		t.Errorf("due_repos/acted_keys must be [] not null: %+v", status)
	}
}

func TestLastRunJSONDiedRun(t *testing.T) {
	rs := synthesis.RunState{Status: "running", StartedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)}
	if lr := lastRunJSON(rs, false); lr.Error != "run died (no exit record)" {
		t.Errorf("died run error = %q", lr.Error)
	}
	if lr := lastRunJSON(rs, true); lr.Error != "" {
		t.Errorf("in-flight run must not report an error, got %q", lr.Error)
	}
	rs = synthesis.RunState{Status: "failed", Reason: "boom", StartedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)}
	if lr := lastRunJSON(rs, false); lr.Error != "boom" {
		t.Errorf("failed run error = %q, want boom", lr.Error)
	}
}

// TestBuildStatusJSONDiedRunEndToEnd drives buildStatusJSON through the real
// store rather than calling lastRunJSON/BuildStatus directly: a "running"
// run-state record with no lock held must surface as last_run.error and
// leave running/running_op false/empty. Guards the wiring itself — e.g. a
// regression that hardcodes the lockHeld flag passed to lastRunJSON, or
// swaps which flag feeds the died-run check vs. RunningOp — which the
// unit-level tests above can't catch since they call the helpers directly.
func TestBuildStatusJSONDiedRunEndToEnd(t *testing.T) {
	t.Setenv("AGENT_INSIGHTS_DIR", t.TempDir())
	t.Setenv("AGENT_INSIGHTS_CONFIG", filepath.Join(t.TempDir(), "nonexistent.yaml"))
	writeRunStateFile(t, synthesis.RunState{Status: "running", StartedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)})

	status, err := buildStatusJSON(insights.Config{CadenceDays: 7, MinSessions: 10})
	if err != nil {
		t.Fatal(err)
	}
	if status.Running {
		t.Error("Running = true with no lock held")
	}
	if status.RunningOp != "" {
		t.Errorf("RunningOp = %q, want empty with no lock held", status.RunningOp)
	}
	if status.LastRun == nil || status.LastRun.Error != "run died (no exit record)" {
		t.Errorf("last_run.error = %+v, want %q", status.LastRun, "run died (no exit record)")
	}
}

// TestBuildStatusJSONRunningEndToEnd is the live-run sibling: with the lock
// actually held (via insights.AcquireLock, the real code path a live
// analyze/synthesize run takes), the same "running" run-state record must
// NOT produce the died-run error, and running_op must reflect the lock's
// recorded op.
func TestBuildStatusJSONRunningEndToEnd(t *testing.T) {
	t.Setenv("AGENT_INSIGHTS_DIR", t.TempDir())
	t.Setenv("AGENT_INSIGHTS_CONFIG", filepath.Join(t.TempDir(), "nonexistent.yaml"))
	writeRunStateFile(t, synthesis.RunState{Status: "running", StartedAt: time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)})

	lock, err := insights.AcquireLock("synthesize")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	status, err := buildStatusJSON(insights.Config{CadenceDays: 7, MinSessions: 10})
	if err != nil {
		t.Fatal(err)
	}
	if !status.Running {
		t.Error("Running = false with lock held")
	}
	if status.RunningOp != "synthesize" {
		t.Errorf("RunningOp = %q, want synthesize", status.RunningOp)
	}
	if status.LastRun == nil || status.LastRun.Error != "" {
		t.Errorf("last_run.error = %+v, want empty while lock held", status.LastRun)
	}
}

// writeRunStateFile writes rs into the current AGENT_INSIGHTS_DIR store,
// mirroring synthesis's private runStatePath convention
// (InsightsDir()/synthesis-run.json) since that helper isn't exported.
func writeRunStateFile(t *testing.T, rs synthesis.RunState) {
	t.Helper()
	data, err := json.Marshal(rs)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(insights.InsightsDir(), "synthesis-run.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// captureStdout redirects os.Stdout for the duration of fn and returns what
// it wrote. Used to test the runStatus/runShow handlers, which write
// directly to os.Stdout via json.NewEncoder rather than taking an io.Writer
// (matching this file's other handlers, which are argv->exit-code CLI
// entrypoints, not library functions).
func captureStdout(t *testing.T, fn func()) []byte {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	fn()

	w.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
