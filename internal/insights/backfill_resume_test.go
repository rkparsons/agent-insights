package insights

import (
	"context"
	"errors"
	"testing"
	"time"
)

// sequenceJudge fails on the given 1-based call indices and succeeds otherwise.
// Failures are keyed by call position (not session-id, which the Judge never sees),
// so tests script "the Nth analyzed session fails" independent of scan order.
type sequenceJudge struct {
	fields JudgedFields
	failOn map[int]bool
	calls  int
}

func (j *sequenceJudge) Judge(ctx context.Context, in ReducedInput) (JudgedFields, error) {
	j.calls++
	if j.failOn[j.calls] {
		return JudgedFields{}, errors.New("judge failed")
	}
	return j.fields, nil
}

// A window-interrupted (previously-errored) session has no analysis file, so a plain
// re-run of the identical command must reprocess it — no flag, no --retry-errored.
func TestRunBackfillRetriesErroredOnReRun(t *testing.T) {
	projects := t.TempDir()
	t.Setenv("TMUX_CTRL_CLAUDE_PROJECTS_DIR", projects)
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	ids := []string{"a", "b", "c", "d", "e"}
	for _, id := range ids {
		writeSession(t, projects, "proj", id, 6)
	}
	opts := Options{MinAssistantTurns: DefaultMinAssistantTurns, Timeout: time.Minute}

	// First run: calls 4 and 5 fail (two errored, never 3 in a row -> no park).
	j1 := &sequenceJudge{fields: substantialJudged(), failOn: map[int]bool{4: true, 5: true}}
	sum, err := RunBackfill(context.Background(), noRepo, j1, opts)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Analyzed != 3 || sum.Errored != 2 {
		t.Fatalf("run1: want 3 analyzed + 2 errored, got %+v", sum)
	}

	// Second run, identical command; judge now succeeds. The two errored sessions lack an
	// analysis file -> retried; the three done ones are skipped.
	j2 := &sequenceJudge{fields: substantialJudged()}
	sum, err = RunBackfill(context.Background(), noRepo, j2, opts)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Analyzed != 2 || sum.SkippedIncremental != 3 {
		t.Fatalf("run2: want 2 retried + 3 skipped-incremental, got %+v", sum)
	}
	for _, id := range ids {
		if _, ok := ReadAnalysisMtime(id); !ok {
			t.Errorf("%s should have an analysis file after resume", id)
		}
	}
}

// Hitting the usage window makes every claude -p call fail: the run must park after 3
// consecutive failures instead of grinding through every remaining session's timeout.
// The parked run is clean (nil error), leaves the rest unprocessed, and a re-run finishes.
func TestRunBackfillParksAfterConsecutiveFailures(t *testing.T) {
	projects := t.TempDir()
	t.Setenv("TMUX_CTRL_CLAUDE_PROJECTS_DIR", projects)
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	ids := []string{"a", "b", "c", "d", "e", "f"}
	for _, id := range ids {
		writeSession(t, projects, "proj", id, 6)
	}
	opts := Options{MinAssistantTurns: DefaultMinAssistantTurns, Timeout: time.Minute}

	sum, err := RunBackfill(context.Background(), noRepo, fakeJudge{err: errors.New("window hit")}, opts)
	if err != nil {
		t.Fatalf("a parked run is not an error: %v", err)
	}
	if !sum.Parked {
		t.Fatalf("want parked after 3 consecutive failures, got %+v", sum)
	}
	if sum.Analyzed != 0 || sum.Errored != 3 {
		t.Fatalf("want stop at exactly 3 errored, 0 analyzed, got %+v", sum)
	}
	if sum.Remaining != 6 {
		t.Fatalf("want 6 remaining (3 errored + 3 unvisited), got %d", sum.Remaining)
	}

	// Window recovered: the same command finishes every remaining session.
	sum, err = RunBackfill(context.Background(), noRepo, &sequenceJudge{fields: substantialJudged()}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Analyzed != 6 || sum.Parked {
		t.Fatalf("run2: want all 6 analyzed, not parked, got %+v", sum)
	}
	for _, id := range ids {
		if _, ok := ReadAnalysisMtime(id); !ok {
			t.Errorf("%s not analyzed after recovery", id)
		}
	}
}

// A single failure whose neighbours succeed must NOT trip the consecutive-failure stop;
// it is recorded as errored and retried on the next run.
func TestRunBackfillIsolatedFailureDoesNotPark(t *testing.T) {
	projects := t.TempDir()
	t.Setenv("TMUX_CTRL_CLAUDE_PROJECTS_DIR", projects)
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	ids := []string{"a", "b", "c", "d", "e"}
	for _, id := range ids {
		writeSession(t, projects, "proj", id, 6)
	}
	opts := Options{MinAssistantTurns: DefaultMinAssistantTurns, Timeout: time.Minute}

	// ok, fail, ok, fail, ok -> at most one consecutive failure -> no park.
	j1 := &sequenceJudge{fields: substantialJudged(), failOn: map[int]bool{2: true, 4: true}}
	sum, err := RunBackfill(context.Background(), noRepo, j1, opts)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Parked {
		t.Fatalf("isolated failures must not park, got %+v", sum)
	}
	if sum.Analyzed != 3 || sum.Errored != 2 {
		t.Fatalf("want 3 analyzed + 2 errored, got %+v", sum)
	}

	j2 := &sequenceJudge{fields: substantialJudged()}
	sum, err = RunBackfill(context.Background(), noRepo, j2, opts)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Analyzed != 2 || sum.SkippedIncremental != 3 {
		t.Fatalf("run2: want 2 retried + 3 skipped-incremental, got %+v", sum)
	}
}

// BackfillPlan reports the pre-run split without a Judge, so it spends nothing: how many
// sessions are done, gated, and still to process. Checked between usage windows.
func TestBackfillPlanCounts(t *testing.T) {
	projects := t.TempDir()
	t.Setenv("TMUX_CTRL_CLAUDE_PROJECTS_DIR", projects)
	t.Setenv("TMUX_CTRL_INSIGHTS_DIR", t.TempDir())
	writeSession(t, projects, "proj", "done1", 6)  // substantial -> analyzed (done)
	writeSession(t, projects, "proj", "gated1", 2) // trivial -> gated
	opts := Options{MinAssistantTurns: DefaultMinAssistantTurns, Timeout: time.Minute}
	if _, err := RunBackfill(context.Background(), noRepo, &sequenceJudge{fields: substantialJudged()}, opts); err != nil {
		t.Fatal(err)
	}
	writeSession(t, projects, "proj", "pending1", 6) // fresh substantial -> to process

	plan, err := BackfillPlan(opts)
	if err != nil {
		t.Fatal(err)
	}
	if plan.ToProcess != 1 || plan.Done != 1 || plan.Gated != 1 {
		t.Fatalf("want 1 to-process, 1 done, 1 gated; got %+v", plan)
	}
}
