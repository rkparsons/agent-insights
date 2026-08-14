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
// session is re-judged, cached, and fed to the bundle stage as fresh analyses —
// which is what the one global synthesis then reads.
func TestRunOutcomeFullScopeJudgesAndCaches(t *testing.T) {
	_, opts := buildOutcomeFixture(t)
	fj := &fakeJudge{jf: insights.JudgedFields{
		UnderlyingGoal: "fresh-goal", Outcome: "fully_achieved", SessionType: "single_task",
	}}
	opts.Scope = "full"
	opts.Judge = fj

	rec, err := RunOutcome(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if fj.calls != 2 { // s1 + s2, one per bucket
		t.Fatalf("judge calls = %d", fj.calls)
	}
	if len(rec.Buckets) != 2 {
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
	if len(rec.SampleOutputs) != 3 {
		t.Fatalf("full scope still samples the global synthesis: %+v", rec.SampleOutputs)
	}

	if _, err := RunOutcome(context.Background(), opts); err != nil {
		t.Fatal(err)
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
	if len(rec.SampleOutputs) != 0 || opts.Synth.(*fakeGlobalSynth).calls != 0 {
		t.Fatal("l1-sample mode must never reach the global synthesis")
	}
}
