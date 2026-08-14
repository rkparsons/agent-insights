package synthesis

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
)

// fakeGlobalSynth is the run's model seam: it records what it was handed and
// returns a canned raw synthesis, so the pipeline (bundles → verify → store)
// is exercised end to end with no LLM.
type fakeGlobalSynth struct {
	raw      insights.RawGlobalSynthesis
	err      error
	bundles  map[string]EvidenceBundle
	deadline time.Time
	workDir  string
	calls    int
}

func (f *fakeGlobalSynth) SynthesizeGlobal(ctx context.Context, bundles map[string]EvidenceBundle) (insights.RawGlobalSynthesis, error) {
	f.calls++
	f.bundles = bundles
	f.deadline, _ = ctx.Deadline()
	return f.raw, f.err
}

func fixedGlobalSynth(f *fakeGlobalSynth) GlobalSynthesizerFactory {
	return func(workDir string) GlobalSynthesizer {
		f.workDir = workDir
		return f
	}
}

// writeAnalysisFixture seeds one analysis in the store: a friction incident and
// a standing preference so BuildBundle emits citable F/P items.
func writeAnalysisFixture(t *testing.T, adir, id, repo string) {
	t.Helper()
	var a insights.AgentSessionAnalysis
	a.Stats.SessionID = id
	a.Stats.Repo = repo
	a.Stats.Cwd = repo
	a.Stats.Start = time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	a.SessionType = "single_task"
	a.Outcome = "fully_achieved"
	a.FrictionIncidents = []insights.FrictionIncident{{
		Type: "rework", OneLine: "had to redo the change", EvidenceQuote: "please just fix the failing test first",
	}}
	a.StandingPreferences = []insights.StandingPreference{{
		Rule: "run tests before claiming done", EvidenceQuote: "always run the tests before you tell me it works",
	}}
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(adir, id+".json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedStore writes n analyses for repoKey into a fresh store and points the
// environment at it. The config path is set to a nonexistent file so LoadConfig
// yields defaults rather than the operator's real config.
func seedStore(t *testing.T, repoKey string, n int) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("AGENT_INSIGHTS_DIR", dir)
	t.Setenv("AGENT_INSIGHTS_CONFIG", filepath.Join(t.TempDir(), "nonexistent.yaml"))
	adir := filepath.Join(dir, "analyses")
	if err := os.MkdirAll(adir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		writeAnalysisFixture(t, adir, "s"+string(rune('a'+i)), "/Users/dev/Developer/"+repoKey)
	}
	return dir
}

// validRaw is a raw synthesis that passes verification against the seeded
// store's bundle: one habit citing the first friction item, quoting it verbatim.
func validRaw() insights.RawGlobalSynthesis {
	return insights.RawGlobalSynthesis{
		SchemaVersion: 2,
		Findings: []insights.RawFinding{{
			Rank: 1, Title: "Run the tests first", Statement: "Run the test suite before reporting a task done.",
			RankRationale:  "The same rework appears across sessions.",
			Asset:          insights.AssetJSON{Type: "habit"},
			EvidenceIDs:    []string{"alpha/F1"},
			Quotes:         []string{"please just fix the failing test first"},
			AlreadyAdopted: insights.AdoptedJSON{Verdict: "no"},
		}},
		Dropped: []insights.DroppedJSON{},
	}
}

func TestRunSynthesizeWritesOneGlobalSnapshot(t *testing.T) {
	dir := seedStore(t, "alpha", 12)
	fake := &fakeGlobalSynth{raw: validRaw()}

	sum, err := RunSynthesize(context.Background(), fixedGlobalSynth(fake), Options{MinSessions: 10})
	if err != nil {
		t.Fatalf("RunSynthesize: %v", err)
	}
	if sum.Written != 1 || sum.Repos != 1 {
		t.Fatalf("summary = %+v, want 1 repo bundled / 1 snapshot written", sum)
	}
	if fake.calls != 1 {
		t.Errorf("model called %d times, want exactly one global call", fake.calls)
	}
	if b, ok := fake.bundles["alpha"]; !ok || b.Repo != "alpha" {
		t.Errorf("bundles = %+v, want one keyed alpha whose Repo matches the key", fake.bundles)
	}

	names, err := snapshotJSONNames(filepath.Join(dir, "synthesis", "global"))
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 {
		t.Fatalf("stored %d snapshots, want exactly one", len(names))
	}
	snap, ok, err := LoadLatestGlobal()
	if err != nil || !ok {
		t.Fatalf("LoadLatestGlobal = (ok=%v, err=%v)", ok, err)
	}
	if snap.SchemaVersion != 2 || len(snap.Findings) != 1 || snap.Findings[0].SessionCount != 1 {
		t.Errorf("stored snapshot = %+v, want the verified v2 payload with Go-owned counts", snap)
	}
	if snap.Meta.Model != insights.DefaultSynthesisModel {
		t.Errorf("meta.model = %q, want the configured synthesis model", snap.Meta.Model)
	}
	rs, ok := ReadRunState()
	if !ok || rs.Status != "ok" || rs.Written != 1 {
		t.Errorf("run state = %+v ok=%v, want a written ok run", rs, ok)
	}
}

// TestRunSynthesizeVerifierFailureStoresNothing is the fail-closed path: a
// dangling evidence id must leave no snapshot, and the reason must reach the
// run state verbatim (that is what status --json surfaces as last_run.error).
// TestRunSynthesizeStampsGeneratedAtAtRunStart: generated_at is what the next
// run's due gate compares analyses against, so stamping it at the end would
// permanently swallow every session analyzed during the model call.
func TestRunSynthesizeStampsGeneratedAtAtRunStart(t *testing.T) {
	seedStore(t, "alpha", 12)
	fake := &fakeGlobalSynth{raw: validRaw()}
	var calledAt time.Time
	factory := func(string) GlobalSynthesizer {
		calledAt = time.Now().UTC()
		return fake
	}

	if _, err := RunSynthesize(context.Background(), factory, Options{MinSessions: 10}); err != nil {
		t.Fatal(err)
	}
	snap, ok, err := LoadLatestGlobal()
	if err != nil || !ok {
		t.Fatalf("LoadLatestGlobal = (ok=%v, err=%v)", ok, err)
	}
	if snap.GeneratedAt.After(calledAt) {
		t.Errorf("generated_at %v postdates the model call at %v: work analyzed during the run would never count as new", snap.GeneratedAt, calledAt)
	}
	rs, _ := ReadRunState()
	if !snap.GeneratedAt.Equal(rs.StartedAt) {
		t.Errorf("generated_at %v != run state started_at %v; one run, one instant", snap.GeneratedAt, rs.StartedAt)
	}
}

func TestRunSynthesizeVerifierFailureStoresNothing(t *testing.T) {
	dir := seedStore(t, "alpha", 12)
	raw := validRaw()
	raw.Findings[0].EvidenceIDs = []string{"alpha/F999"}
	fake := &fakeGlobalSynth{raw: raw}

	sum, err := RunSynthesize(context.Background(), fixedGlobalSynth(fake), Options{MinSessions: 10})
	if err == nil {
		t.Fatal("expected the run to fail closed on a dangling evidence id")
	}
	if sum.Written != 0 {
		t.Errorf("Written = %d, want 0", sum.Written)
	}
	if _, err := os.Stat(filepath.Join(dir, "synthesis", "global")); !os.IsNotExist(err) {
		t.Error("a failed run must not create the global snapshot dir")
	}
	rs, ok := ReadRunState()
	if !ok || rs.Status != "failed" {
		t.Fatalf("run state = %+v ok=%v, want a failed run", rs, ok)
	}
	if !strings.Contains(rs.Reason, "alpha/F999") {
		t.Errorf("last-run reason = %q, want the verifier's reason verbatim", rs.Reason)
	}
}

// TestRunSynthesizeFailurePreservesModelOutput covers the post-mortem: the
// workdir dies with the run, so a failed verification must copy the model's
// output into the store and name it in the error.
func TestRunSynthesizeFailurePreservesModelOutput(t *testing.T) {
	dir := seedStore(t, "alpha", 12)
	raw := validRaw()
	raw.Findings[0].EvidenceIDs = []string{"alpha/F999"}
	fake := &fakeGlobalSynth{raw: raw}
	// The real synthesizer writes this file; the fake stands in for it so the
	// preservation path has something to copy.
	factory := func(workDir string) GlobalSynthesizer {
		if err := os.WriteFile(filepath.Join(workDir, globalOutputFile), []byte(`{"schema_version":2}`), 0o644); err != nil {
			t.Fatal(err)
		}
		return fake
	}

	_, err := RunSynthesize(context.Background(), factory, Options{MinSessions: 10})
	if err == nil {
		t.Fatal("expected a verification failure")
	}
	names, nerr := snapshotJSONNames(filepath.Join(dir, "synthesis-diagnostics"))
	if nerr != nil || len(names) != 1 {
		t.Fatalf("diagnostics dir = %v (err %v), want one preserved output", names, nerr)
	}
	if !strings.Contains(err.Error(), "diagnostics") {
		t.Errorf("error = %q, want it to name the preserved output path", err)
	}
	if _, serr := os.Stat(filepath.Join(dir, "synthesis", "diagnostics")); !os.IsNotExist(serr) {
		t.Error("unscanned model output must not land under the synthesis root the eval freeze copies")
	}
	rs, _ := ReadRunState()
	if !strings.Contains(rs.Reason, "diagnostics") {
		t.Errorf("last-run reason = %q, want the preserved path recorded with the failure", rs.Reason)
	}
}

// TestRunSynthesizeDueGateSkipsRun pins --due to the global gate: nothing new
// since the last snapshot means no spend and no new snapshot.
func TestRunSynthesizeDueGateSkipsRun(t *testing.T) {
	seedStore(t, "alpha", 12)
	if _, err := StoreGlobal(globalFixture(time.Now().UTC(), "prior")); err != nil {
		t.Fatal(err)
	}
	fake := &fakeGlobalSynth{raw: validRaw()}

	sum, err := RunSynthesize(context.Background(), fixedGlobalSynth(fake), Options{MinSessions: 10, Due: true})
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls != 0 {
		t.Errorf("model called %d times, want 0 for a run that is not due", fake.calls)
	}
	if sum.Due || sum.Written != 0 {
		t.Errorf("summary = %+v, want not-due and nothing written", sum)
	}
	if _, ok := ReadRunState(); ok {
		t.Error("a not-due run must not overwrite the last-run record")
	}
}

// TestRunSynthesizeDueGateRunsWhenDue is the sibling: with no prior snapshot
// and the threshold cleared, --due proceeds.
func TestRunSynthesizeDueGateRunsWhenDue(t *testing.T) {
	seedStore(t, "alpha", 12)
	fake := &fakeGlobalSynth{raw: validRaw()}

	sum, err := RunSynthesize(context.Background(), fixedGlobalSynth(fake), Options{MinSessions: 10, Due: true})
	if err != nil {
		t.Fatal(err)
	}
	if !sum.Due || sum.Written != 1 {
		t.Fatalf("summary = %+v, want a due run that wrote one snapshot", sum)
	}
}

func TestRunSynthesizeDryRunSpendsNothing(t *testing.T) {
	dir := seedStore(t, "alpha", 12)
	fake := &fakeGlobalSynth{raw: validRaw()}

	stderr := captureStderr(t, func() {
		sum, err := RunSynthesize(context.Background(), fixedGlobalSynth(fake), Options{MinSessions: 10, DryRun: true})
		if err != nil {
			t.Fatal(err)
		}
		if sum.Repos != 1 || sum.Written != 0 {
			t.Errorf("summary = %+v, want 1 repo bundled / 0 written", sum)
		}
	})

	if fake.calls != 0 {
		t.Errorf("model called %d times on a dry run", fake.calls)
	}
	if _, err := os.Stat(filepath.Join(dir, "synthesis")); !os.IsNotExist(err) {
		t.Error("dry-run must not write anything under synthesis/")
	}
	if _, ok := ReadRunState(); ok {
		t.Error("dry-run must not write run state")
	}
	for _, want := range []string{"alpha", "KB", "friction", "due=", "threshold"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("dry-run report %q missing %q (bundle sizes + due reasoning)", stderr, want)
		}
	}
}

// TestRunSynthesizeTimeoutReachesTheModel pins the operator's --timeout to the
// call that spends: an unbounded model call is the documented kill-after-spend
// failure this flag exists to prevent.
func TestRunSynthesizeTimeoutReachesTheModel(t *testing.T) {
	seedStore(t, "alpha", 12)
	fake := &fakeGlobalSynth{raw: validRaw()}

	if _, err := RunSynthesize(context.Background(), fixedGlobalSynth(fake), Options{MinSessions: 10, Timeout: 3 * time.Minute}); err != nil {
		t.Fatal(err)
	}
	if got := time.Until(fake.deadline); got > 3*time.Minute || got < 2*time.Minute {
		t.Errorf("model deadline in %v, want ~3m from the operator's --timeout", got)
	}

	fake2 := &fakeGlobalSynth{raw: validRaw()}
	if _, err := RunSynthesize(context.Background(), fixedGlobalSynth(fake2), Options{MinSessions: 10}); err != nil {
		t.Fatal(err)
	}
	if got := time.Until(fake2.deadline); got < DefaultGlobalTimeout-time.Minute {
		t.Errorf("default deadline in %v, want ~%v", got, DefaultGlobalTimeout)
	}
}

// TestRunSynthesizeBelowFloorSpendsNothing: no repo clears min_sessions, so
// there is nothing to bundle and the run must not reach the model.
func TestRunSynthesizeBelowFloorSpendsNothing(t *testing.T) {
	seedStore(t, "alpha", 3)
	fake := &fakeGlobalSynth{raw: validRaw()}

	sum, err := RunSynthesize(context.Background(), fixedGlobalSynth(fake), Options{MinSessions: 10})
	if err != nil {
		t.Fatal(err)
	}
	if fake.calls != 0 || sum.Repos != 0 || sum.Written != 0 {
		t.Errorf("summary = %+v after %d model calls, want an empty no-spend run", sum, fake.calls)
	}
	rs, ok := ReadRunState()
	if !ok || rs.Status != "ok" {
		t.Errorf("run state = %+v ok=%v, want an ok record (the TUI reads it either way)", rs, ok)
	}
}

func TestRunSynthesizeModelFailureRecordsReason(t *testing.T) {
	seedStore(t, "alpha", 12)
	fake := &fakeGlobalSynth{err: errors.New("claude exit 1: Not logged in")}

	if _, err := RunSynthesize(context.Background(), fixedGlobalSynth(fake), Options{MinSessions: 10}); err == nil {
		t.Fatal("expected the model failure to fail the run")
	}
	rs, ok := ReadRunState()
	if !ok || rs.Status != "failed" || !strings.Contains(rs.Reason, "Not logged in") {
		t.Errorf("run state = %+v ok=%v, want the model's reason verbatim", rs, ok)
	}
}

// TestRunSynthesizeMaterializesSkillWorkdir pins the run's other obligation to
// the model: the nested claude resolves the skill from its cwd.
func TestRunSynthesizeMaterializesSkillWorkdir(t *testing.T) {
	seedStore(t, "alpha", 12)
	fake := &fakeGlobalSynth{raw: validRaw()}
	var skillPresent bool
	factory := func(workDir string) GlobalSynthesizer {
		_, err := os.Stat(filepath.Join(workDir, ".claude", "skills", "synthesizing-workflow-insights"))
		skillPresent = err == nil
		return fake
	}

	if _, err := RunSynthesize(context.Background(), factory, Options{MinSessions: 10}); err != nil {
		t.Fatal(err)
	}
	if !skillPresent {
		t.Error("the run must materialize the synthesis skill into the model's workdir")
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	fn()

	w.Close()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return sb.String()
}
