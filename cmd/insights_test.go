package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
	o, err := parseSynthesizeArgs([]string{"--min-sessions", "5", "--dry-run", "--timeout", "30m"})
	if err != nil {
		t.Fatal(err)
	}
	if o.MinSessions != 5 || !o.DryRun || o.Timeout != 30*time.Minute {
		t.Errorf("parsed = %+v", o)
	}
	if _, err := parseSynthesizeArgs([]string{"--bogus"}); err == nil {
		t.Error("expected error on unknown flag")
	}
	// --repo died with the per-repo synthesis: a stale invocation must fail
	// loudly rather than be silently ignored into a full global run.
	if _, err := parseSynthesizeArgs([]string{"--repo", "alpha"}); err == nil {
		t.Error("expected error for the removed --repo flag")
	}
	if _, err := parseSynthesizeArgs([]string{"--timeout"}); err == nil {
		t.Error("expected error for a missing --timeout value")
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

// TestRunShowJSONValidatesAgainstSchema drives `insights show --json` end to
// end over a stored snapshot and validates the emitted payload against the
// published contract (schemas/show.schema.json) — the file the TUI vendors.
// Closes the Task 1 carry-forward: show now emits the real v2 payload.
func TestRunShowJSONValidatesAgainstSchema(t *testing.T) {
	t.Setenv("AGENT_INSIGHTS_DIR", t.TempDir())
	seedGlobalSnapshot(t)

	out := captureStdout(t, func() { runShow([]string{"--json"}) })

	var snap insights.GlobalSynthesisJSON
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&snap); err != nil {
		t.Fatalf("show --json did not decode as GlobalSynthesisJSON: %v\noutput: %s", err, out)
	}
	if snap.SchemaVersion != insights.ContractVersion || len(snap.Findings) != 1 {
		t.Errorf("payload = %+v, want the stored v2 snapshot", snap)
	}
	assertMatchesSchema(t, "show.schema.json", out)
}

// TestRunShowJSONNeverRunValidatesAgainstSchema is the degraded state: no
// snapshot on disk must still produce a schema-valid, array-complete payload.
func TestRunShowJSONNeverRunValidatesAgainstSchema(t *testing.T) {
	t.Setenv("AGENT_INSIGHTS_DIR", t.TempDir())

	out := captureStdout(t, func() { runShow([]string{"--json"}) })

	if bytes.Contains(out, []byte("null")) {
		t.Errorf("never-run payload must not carry null arrays: %s", out)
	}
	assertMatchesSchema(t, "show.schema.json", out)
}

// TestRunStatusJSONValidatesAgainstSchema pins status --json to its own
// schema, whose running_op enum lost "enrich" with the subcommand.
func TestRunStatusJSONValidatesAgainstSchema(t *testing.T) {
	t.Setenv("AGENT_INSIGHTS_DIR", t.TempDir())
	t.Setenv("AGENT_INSIGHTS_CONFIG", filepath.Join(t.TempDir(), "nonexistent.yaml"))

	out := captureStdout(t, func() {
		runStatus(insights.Config{CadenceDays: 14, MinSessions: 10, DueNewSessions: 10}, []string{"--json"})
	})

	assertMatchesSchema(t, "status.schema.json", out)
}

// TestStatusDueReposReflectsGlobalGate: due_repos names the repos contributing
// new sessions to a due global run, and is empty whenever no run is due.
func TestStatusDueReposReflectsGlobalGate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENT_INSIGHTS_DIR", dir)
	t.Setenv("AGENT_INSIGHTS_CONFIG", filepath.Join(t.TempDir(), "nonexistent.yaml"))
	cfg := insights.Config{CadenceDays: 14, MinSessions: 10, DueNewSessions: 10}
	adir := filepath.Join(dir, "analyses")
	if err := os.MkdirAll(adir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		var a insights.AgentSessionAnalysis
		a.Stats.SessionID = "s" + strconv.Itoa(i)
		a.Stats.Repo = "/Users/dev/Developer/alpha"
		data, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(adir, a.Stats.SessionID+".json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	status, err := buildStatusJSON(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.DueRepos) != 1 || status.DueRepos[0] != "alpha" {
		t.Fatalf("due_repos = %v, want [alpha] (no snapshot yet, threshold cleared)", status.DueRepos)
	}

	// A fresh snapshot resets both terms of the gate: nothing is due, so
	// nothing contributes.
	if _, err := synthesis.StoreGlobal(insights.GlobalSynthesisJSON{SchemaVersion: 2, GeneratedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	status, err = buildStatusJSON(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.DueRepos) != 0 {
		t.Errorf("due_repos = %v, want empty when no run is due", status.DueRepos)
	}
}

// seedGlobalSnapshot stores one minimal but fully-populated v2 snapshot in the
// current store.
func seedGlobalSnapshot(t *testing.T) {
	t.Helper()
	snap := insights.GlobalSynthesisJSON{
		SchemaVersion: insights.ContractVersion,
		GeneratedAt:   time.Date(2026, 8, 13, 9, 0, 0, 0, time.UTC),
		Window:        insights.WindowBoundsJSON{From: "2026-07-27", To: "2026-08-10"},
		Repos: []insights.RepoStatsJSON{{
			Key: "alpha", Window: insights.WindowBoundsJSON{From: "2026-07-27", To: "2026-08-10"},
			SessionCount: 12, AnalyzedCount: 12,
		}},
		Findings: []insights.FindingJSON{{
			Rank: 1, Title: "Run the tests first", Statement: "Run the test suite before reporting a task done.",
			RankRationale: "Recurring rework across repos.",
			Asset:         insights.AssetJSON{Type: "claude_md_rule", Target: "~/.claude/CLAUDE.md", Content: "Run the tests first."},
			Audience:      "user", EvidenceIDs: []string{"alpha/P1"},
			Quotes:         []string{"always run the tests before you tell me it works"},
			AlreadyAdopted: insights.AdoptedJSON{Verdict: "no"},
			Repos:          []string{"alpha"}, SessionCount: 4, LastSeen: "2026-08-06", ActedKey: "0123456789abcdef",
		}},
		Dropped: []insights.DroppedJSON{{Summary: "editor lag", Reason: "environmental", EvidenceIDs: []string{"alpha/G1"}}},
		Meta:    insights.GlobalMetaJSON{Model: insights.DefaultSynthesisModel, ValidationNotes: []string{"note"}},
	}
	if _, err := synthesis.StoreGlobal(snap); err != nil {
		t.Fatal(err)
	}
}

// assertMatchesSchema validates a CLI payload against a published JSON schema:
// required keys present, no key the schema does not declare (every object
// declares additionalProperties:false), declared types honored, and `const`
// values matched. Deliberately a local walker rather than a validator
// dependency — the schemas use one narrow subset of draft-07, and the point is
// to catch contract drift in what the CLI actually prints.
func assertMatchesSchema(t *testing.T, schemaName string, payload []byte) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "schemas", schemaName))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	var doc any
	if err := json.Unmarshal(payload, &doc); err != nil {
		t.Fatalf("parse payload: %v", err)
	}
	validateNode(t, schema, schema, doc, "$")
}

func validateNode(t *testing.T, root, node map[string]any, value any, path string) {
	t.Helper()
	if ref, ok := node["$ref"].(string); ok {
		defs, _ := root["definitions"].(map[string]any)
		target, ok := defs[strings.TrimPrefix(ref, "#/definitions/")].(map[string]any)
		if !ok {
			t.Fatalf("%s: unresolved $ref %q", path, ref)
		}
		node = target
	}
	if want, ok := node["const"]; ok && fmt.Sprint(want) != fmt.Sprint(value) {
		t.Errorf("%s = %v, want const %v", path, value, want)
	}
	if allowed, ok := node["enum"].([]any); ok {
		matched := false
		for _, a := range allowed {
			if fmt.Sprint(a) == fmt.Sprint(value) {
				matched = true
			}
		}
		if !matched {
			t.Errorf("%s = %v, want one of %v", path, value, allowed)
		}
	}
	switch node["type"] {
	case "object":
		obj, ok := value.(map[string]any)
		if !ok {
			t.Errorf("%s = %T, want object", path, value)
			return
		}
		props, _ := node["properties"].(map[string]any)
		if req, ok := node["required"].([]any); ok {
			for _, r := range req {
				if _, present := obj[fmt.Sprint(r)]; !present {
					t.Errorf("%s missing required key %v", path, r)
				}
			}
		}
		for k, v := range obj {
			sub, declared := props[k].(map[string]any)
			if !declared {
				if node["additionalProperties"] == false {
					t.Errorf("%s.%s is not declared in the schema", path, k)
				}
				continue
			}
			validateNode(t, root, sub, v, path+"."+k)
		}
	case "array":
		arr, ok := value.([]any)
		if !ok {
			t.Errorf("%s = %T, want array", path, value)
			return
		}
		items, _ := node["items"].(map[string]any)
		for i, el := range arr {
			validateNode(t, root, items, el, fmt.Sprintf("%s[%d]", path, i))
		}
	case "string":
		if _, ok := value.(string); !ok {
			t.Errorf("%s = %T, want string", path, value)
		}
	case "integer":
		if _, ok := value.(float64); !ok {
			t.Errorf("%s = %T, want integer", path, value)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			t.Errorf("%s = %T, want boolean", path, value)
		}
	}
}
