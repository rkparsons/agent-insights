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

func TestRunOutcomeFullScopeJudgesAndCaches(t *testing.T) {
	_, opts := buildOutcomeFixture(t)
	fj := &fakeJudge{jf: insights.JudgedFields{
		UnderlyingGoal: "fresh-goal", Outcome: "fully_achieved", SessionType: "single_task",
	}}
	fs := &fakeSynth{}
	opts.Scope = "full"
	opts.Judge = fj
	opts.Synth = fs

	rec, err := RunOutcome(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if fj.calls != 2 { // s1 + s2
		t.Fatalf("judge calls = %d", fj.calls)
	}
	if len(rec.Buckets) != 1 {
		t.Fatalf("buckets: %+v", rec.Buckets)
	}
	// bundle must be built from FRESH judged fields, not the pool's
	cache := NewCache(opts.CacheDir)
	var vo VerifiedOutput
	if hit, err := cache.Get("verify", rec.Buckets[0].Samples[0].VerifiedKey, &vo); err != nil || !hit {
		t.Fatalf("verify output: hit=%v err=%v", hit, err)
	}
	if vo.Synthesis.Window.AnalyzedCount != 2 {
		t.Fatalf("analyzed = %d", vo.Synthesis.Window.AnalyzedCount)
	}
	// the cached bundle must carry the FRESH judged fields, not the pool's
	// ("goal-s1"/"goal-s2") — this is what distinguishes scope=full output
	var bundle synthesis.EvidenceBundle
	if hit, err := cache.Get("bundle", rec.Buckets[0].BundleKey, &bundle); err != nil || !hit {
		t.Fatalf("bundle: hit=%v err=%v", hit, err)
	}
	if len(bundle.Success) == 0 || bundle.Success[0].Goal != "fresh-goal" {
		t.Fatalf("bundle not built from fresh judged fields: %+v", bundle.Success)
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
	fs := &fakeSynth{}
	opts.L1Sample = true
	opts.Judge = fj
	opts.Synth = fs

	rec, err := RunOutcome(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if fs.calls != 0 {
		t.Fatal("l1-sample must not run L2")
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

var _ synthesis.Synthesizer = (*fakeSynth)(nil)
