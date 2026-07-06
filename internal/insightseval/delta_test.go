package insightseval

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func tupleFixture() ComparisonTuple {
	return ComparisonTuple{Population: "scoring", Scope: "l2", PoolVersion: "v1",
		Models:  map[string]string{"l1": "m1", "l2": "m2", "matcher": "m3"},
		EnvHash: "e1", RubricSetHash: "r1"}
}

func committedVerdict(name string, at time.Time, targets ...TargetVerdict) namedVerdict {
	return namedVerdict{Name: name, V: Verdict{ScoredAt: at, Tuple: tupleFixture(), Targets: targets}}
}

func TestEffectiveTargetOutcome(t *testing.T) {
	plain := TargetVerdict{ID: "C-01", Pass: true, Granularity: "full", PassAt: "full"}
	if pass, _ := EffectiveTargetOutcome(plain, nil); !pass {
		t.Fatal("plain pass")
	}
	prov := TargetVerdict{ID: "C-E1", Pass: false, ProvisionalFail: true, Granularity: "full", PassAt: "full",
		Triggers: []Trigger{{Type: "first_pass_no_anchor", KeyHash: "k1"}}}
	if pass, _ := EffectiveTargetOutcome(prov, nil); pass {
		t.Fatal("unadjudicated provisional-fail stays failed")
	}
	adj := map[string]Adjudication{"k1": {KeyHash: "k1", Decision: "accept"}}
	if pass, gran := EffectiveTargetOutcome(prov, adj); !pass || gran != "full" {
		t.Fatal("accepted provisional-fail is effectively a pass")
	}
	adj["k1"] = Adjudication{KeyHash: "k1", Decision: "reject"}
	if pass, _ := EffectiveTargetOutcome(prov, adj); pass {
		t.Fatal("rejected provisional-fail stays failed")
	}
	// anchor_mismatch acceptance never lifts a committed verdict — it applies
	// only on re-score (no item detail to recompute from)
	mism := TargetVerdict{ID: "C-02", Pass: false, ProvisionalFail: false, Granularity: "absent", PassAt: "full",
		Triggers: []Trigger{{Type: "anchor_mismatch", KeyHash: "k2"}}}
	adj["k2"] = Adjudication{KeyHash: "k2", Decision: "accept"}
	if pass, _ := EffectiveTargetOutcome(mism, adj); pass {
		t.Fatal("membership acceptance applies from the next re-score, not retroactively")
	}

	// multi-trigger: EVERY provisional trigger must be accepted; membership
	// triggers alongside them never gate the lift
	multi := TargetVerdict{ID: "C-G", Pass: false, ProvisionalFail: true, Granularity: "full", PassAt: "full",
		Triggers: []Trigger{
			{Type: "first_pass_no_anchor", KeyHash: "p1"},
			{Type: "flip", KeyHash: "p2"},
			{Type: "anchor_mismatch", KeyHash: "m1"},
		}}
	madj := map[string]Adjudication{"p1": {KeyHash: "p1", Decision: "accept"}}
	if pass, _ := EffectiveTargetOutcome(multi, madj); pass {
		t.Fatal("partial acceptance (one of two provisional triggers) stays failed")
	}
	madj["p2"] = Adjudication{KeyHash: "p2", Decision: "accept"}
	if pass, gran := EffectiveTargetOutcome(multi, madj); !pass || gran != "full" {
		t.Fatal("all provisional triggers accepted lifts despite the unadjudicated membership trigger")
	}
}

func TestComputeDeltaProvisionalBaselineFlip(t *testing.T) {
	// baseline committed C-G as provisional-fail; a later accepted adjudication
	// makes its EFFECTIVE outcome a pass — delta must compare against that
	base := committedVerdict("base.json", time.Now().UTC(),
		TargetVerdict{ID: "C-G", Pass: false, ProvisionalFail: true, Granularity: "full", PassAt: "full",
			Triggers: []Trigger{{Type: "first_pass_no_anchor", KeyHash: "k1"}}})
	adj := map[string]Adjudication{"k1": {KeyHash: "k1", Decision: "accept"}}

	same := []TargetVerdict{{ID: "C-G", Pass: true, Granularity: "full", PassAt: "full"}}
	if d := ComputeDelta(same, &base, adj); len(d.Flips) != 0 {
		t.Fatalf("current pass vs effectively-passing baseline is not a flip: %+v", d.Flips)
	}

	regressed := []TargetVerdict{{ID: "C-G", Pass: false, Granularity: "partial", PassAt: "full"}}
	d := ComputeDelta(regressed, &base, adj)
	want := []Flip{{TargetID: "C-G", From: "full", To: "partial", PassChanged: true}}
	if !reflect.DeepEqual(d.Flips, want) {
		t.Fatalf("effective-baseline flip: %+v", d.Flips)
	}

	// without the adjudication the baseline stays failed: granularity flip only
	d = ComputeDelta(regressed, &base, nil)
	want = []Flip{{TargetID: "C-G", From: "full", To: "partial", PassChanged: false}}
	if !reflect.DeepEqual(d.Flips, want) {
		t.Fatalf("unadjudicated baseline: %+v", d.Flips)
	}
}

func TestFindBaselineAndTupleMatching(t *testing.T) {
	t1, t2 := time.Date(2026, 7, 4, 0, 0, 0, 0, time.UTC), time.Date(2026, 7, 5, 0, 0, 0, 0, time.UTC)
	a := committedVerdict("a.json", t1)
	b := committedVerdict("b.json", t2)
	other := committedVerdict("c.json", t2.Add(time.Hour))
	other.V.Tuple.Population = "as_consumed" // control runs are never comparable
	got := FindBaseline(tupleFixture(), []namedVerdict{a, other, b})
	if got == nil || got.Name != "b.json" {
		t.Fatalf("baseline: %+v", got)
	}
	if FindBaseline(ComparisonTuple{Population: "scoring"}, []namedVerdict{a, b}) != nil {
		t.Fatal("tuple mismatch must yield fresh baseline")
	}
}

func TestComputeDeltaFlipsAndFreshBaseline(t *testing.T) {
	base := committedVerdict("base.json", time.Now().UTC(),
		TargetVerdict{ID: "C-01", Pass: true, Granularity: "full", PassAt: "full"},
		TargetVerdict{ID: "C-02", Pass: false, Granularity: "absent", PassAt: "full"},
		TargetVerdict{ID: "C-03", Pass: true, Granularity: "full", PassAt: "full"},
	)
	current := []TargetVerdict{
		{ID: "C-01", Pass: false, Granularity: "partial", PassAt: "full"}, // regression
		{ID: "C-02", Pass: true, Granularity: "full", PassAt: "full"},     // improvement
		{ID: "C-03", Pass: true, Granularity: "full", PassAt: "full"},     // stable
		{ID: "M9", Pass: false, Granularity: "absent", PassAt: "full"},    // not in baseline
	}
	d := ComputeDelta(current, &base, nil)
	if d.FreshBaseline || d.BaselineRun != "base.json" {
		t.Fatalf("delta: %+v", d)
	}
	want := []Flip{
		{TargetID: "C-01", From: "full", To: "partial", PassChanged: true},
		{TargetID: "C-02", From: "absent", To: "full", PassChanged: true},
	}
	if !reflect.DeepEqual(d.Flips, want) {
		t.Fatalf("flips: %+v", d.Flips)
	}
	fresh := ComputeDelta(current, nil, nil)
	if !fresh.FreshBaseline || len(fresh.Flips) != 0 {
		t.Fatalf("fresh baseline: %+v", fresh)
	}
}

func TestLoadCommittedVerdictsAndEverPassed(t *testing.T) {
	data := t.TempDir()
	prior, err := LoadCommittedVerdicts(data)
	if err != nil || len(prior) != 0 {
		t.Fatalf("no runs dir: %v %v", prior, err)
	}
	v := committedVerdict("2026-07-04T10-00-00Z.json", time.Date(2026, 7, 4, 10, 0, 0, 0, time.UTC),
		TargetVerdict{ID: "C-01", Pass: true, Granularity: "full", PassAt: "full"})
	if err := writeJSON(filepath.Join(data, "runs", v.Name), v.V); err != nil {
		t.Fatal(err)
	}
	prior, err = LoadCommittedVerdicts(data)
	if err != nil || len(prior) != 1 || prior[0].Name != v.Name {
		t.Fatalf("load: %+v %v", prior, err)
	}
	ever := everPassedTargets(prior, nil)
	if !ever["C-01"] || ever["C-02"] {
		t.Fatalf("everPassed: %v", ever)
	}
}
