package eval

import (
	"context"
	"testing"

	"github.com/rkparsons/agent-insights/internal/insights"
	"github.com/rkparsons/agent-insights/internal/synthesis"
)

type fakeJudge struct {
	jf    insights.JudgedFields
	calls int
}

func (f *fakeJudge) Judge(ctx context.Context, in insights.ReducedInput) (insights.JudgedFields, error) {
	f.calls++
	return f.jf, nil
}

// TestRunOutcomeFullScopeJudgesAndCaches covers the full-scope L1 half: every
// session is re-judged, cached, and fed to the bundle stage as fresh analyses.
//
// Task 8-10: the assertions past the bundle — that the verify-stage cache holds
// a synthesis built from the fresh judged fields — belonged to the v1 L2 stage
// removed in plan Task 7, and return with the global run in plan Task 8. The
// run itself now fails closed there, so the L1 evidence is read from the cache.
func TestRunOutcomeFullScopeJudgesAndCaches(t *testing.T) {
	_, opts := buildOutcomeFixture(t)
	fj := &fakeJudge{jf: insights.JudgedFields{
		UnderlyingGoal: "fresh-goal", Outcome: "fully_achieved", SessionType: "single_task",
	}}
	opts.Scope = "full"
	opts.Judge = fj

	rec, err := RunOutcome(context.Background(), opts)
	if err == nil {
		t.Fatal("Task 8-10: the L2 stage must fail closed until the v2 rework")
	}
	if fj.calls != 2 { // s1 + s2
		t.Fatalf("judge calls = %d", fj.calls)
	}
	if len(rec.Buckets) != 1 {
		t.Fatalf("buckets: %+v", rec.Buckets)
	}
	// The cached bundle must carry the FRESH judged fields, not the pool's
	// ("goal-s1"/"goal-s2") — this is what distinguishes scope=full output.
	cache := NewCache(opts.CacheDir)
	var bundle synthesis.EvidenceBundle
	if hit, err := cache.Get("bundle", rec.Buckets[0].BundleKey, &bundle); err != nil || !hit {
		t.Fatalf("bundle: hit=%v err=%v", hit, err)
	}
	if len(bundle.Success) == 0 || bundle.Success[0].Goal != "fresh-goal" {
		t.Fatalf("bundle not built from fresh judged fields: %+v", bundle.Success)
	}

	if _, err := RunOutcome(context.Background(), opts); err == nil {
		t.Fatal("Task 8-10: the L2 stage must fail closed until the v2 rework")
	}
	if fj.calls != 2 {
		t.Fatalf("second run must serve L1 from cache, calls = %d", fj.calls)
	}
}

func TestRunOutcomeL1SampleRunsSubsetOnly(t *testing.T) {
	_, opts := buildOutcomeFixture(t)
	fj := &fakeJudge{jf: insights.JudgedFields{Outcome: "unclear", SessionType: "single_task"}}
	opts.L1Sample = true
	opts.Judge = fj

	rec, err := RunOutcome(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if rec.L1Sample == nil || rec.L1Sample.Analyzed == 0 || len(rec.L1Sample.Cells) == 0 {
		t.Fatalf("l1 sample result: %+v", rec.L1Sample)
	}
	if fj.calls != rec.L1Sample.Analyzed {
		t.Fatalf("judge calls %d != analyzed %d", fj.calls, rec.L1Sample.Analyzed)
	}
	if len(rec.Buckets) != 0 {
		t.Fatal("l1-sample records no bucket outputs")
	}
}
