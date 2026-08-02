package cmd

import (
	"testing"
	"time"
)

func TestParseInsightsArgs(t *testing.T) {
	cases := []struct {
		args         []string
		wantMode     string
		wantForce    bool
		wantDry      bool
		wantTh       int
		wantTimeout  time.Duration
		wantQuietFor time.Duration
		wantErr      bool
	}{
		{[]string{"analyze", "sess-1"}, "single", false, false, 5, 10 * time.Minute, 0, false},
		{[]string{"analyze", "--backfill"}, "backfill", false, false, 5, 10 * time.Minute, 0, false},
		{[]string{"analyze", "--backfill", "--force", "--threshold", "3"}, "backfill", true, false, 3, 10 * time.Minute, 0, false},
		{[]string{"analyze", "--backfill", "--dry-run"}, "backfill", false, true, 5, 10 * time.Minute, 0, false},
		{[]string{"analyze", "sess-1", "--force"}, "single", true, false, 5, 10 * time.Minute, 0, false},
		{[]string{"analyze", "--backfill", "--quiet-for", "24h"}, "backfill", false, false, 5, 10 * time.Minute, 24 * time.Hour, false},
		{[]string{"analyze"}, "", false, false, 0, 0, 0, true},
		{[]string{"bogus"}, "", false, false, 0, 0, 0, true},
		{[]string{"analyze", "--backfill", "sess-1"}, "", false, false, 0, 0, 0, true},
		{[]string{"analyze", "--bogus-flag"}, "", false, false, 0, 0, 0, true},
		{[]string{"analyze", "--threshold"}, "", false, false, 0, 0, 0, true},
		{[]string{"analyze", "--timeout"}, "", false, false, 0, 0, 0, true},
		{[]string{"analyze", "--quiet-for"}, "", false, false, 0, 0, 0, true},
		{[]string{"analyze", "--backfill", "--quiet-for", "bogus"}, "", false, false, 0, 0, 0, true},
		{[]string{"analyze", "--dry-run"}, "", false, false, 0, 0, 0, true},                     // dry-run needs --backfill
		{[]string{"analyze", "sess-1", "--dry-run"}, "", false, false, 0, 0, 0, true},           // dry-run needs --backfill
		{[]string{"analyze", "--backfill", "--retry-errored"}, "", false, false, 0, 0, 0, true}, // removed flag
	}
	for _, c := range cases {
		mode, target, opts, err := parseInsightsArgs(c.args)
		if (err != nil) != c.wantErr {
			t.Errorf("%v: err=%v wantErr=%v", c.args, err, c.wantErr)
			continue
		}
		if c.wantErr {
			continue
		}
		if mode != c.wantMode || opts.Force != c.wantForce || opts.DryRun != c.wantDry || opts.MinAssistantTurns != c.wantTh {
			t.Errorf("%v: mode=%q force=%v dry=%v th=%d target=%q", c.args, mode, opts.Force, opts.DryRun, opts.MinAssistantTurns, target)
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
	o, err := parseSynthesizeArgs([]string{"--repo", "client-project", "--min-sessions", "5", "--dry-run"})
	if err != nil {
		t.Fatal(err)
	}
	if o.Repo != "client-project" || o.MinSessions != 5 || !o.DryRun {
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
