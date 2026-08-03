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

// TestRunStatusJSON runs `insights status --json` end to end against a temp
// store and checks the stdout payload parses as StatusJSON with the
// contract's schema_version. No analyses/syntheses exist in the temp store,
// so this also exercises the "empty store" path (LoadAnalyses on a missing
// dir, no run state file).
func TestRunStatusJSON(t *testing.T) {
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
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
