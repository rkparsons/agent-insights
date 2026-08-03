package eval

import (
	"strings"
	"testing"
)

func nuanceSample(idx int, gran string, nuances []bool) SampleScore {
	s := sample(idx, gran)
	s.NuancePasses = nuances
	return s
}

func TestAggregateTargetNuancePassMedian(t *testing.T) {
	r := scoreRubric()
	samples := []SampleScore{
		nuanceSample(0, "partial", []bool{true, false, false}),
		nuanceSample(1, "partial", []bool{true, true, false}),
		nuanceSample(2, "partial", []bool{false, false, false}),
	}
	tv, _ := AggregateTarget(r, "must_pass", samples, 2, nil, true)
	if tv.NuancePassMedian != 1 {
		t.Fatalf("median of counts 1,2,0 must be 1, got %d", tv.NuancePassMedian)
	}

	// an absent sample (no counted item, nil NuancePasses) counts as 0
	samples = []SampleScore{
		nuanceSample(0, "partial", []bool{true, true}),
		{SampleIndex: 1, Granularity: "absent"},
		{SampleIndex: 2, Granularity: "absent"},
	}
	tv, _ = AggregateTarget(r, "must_pass", samples, 2, nil, true)
	if tv.NuancePassMedian != 0 {
		t.Fatalf("median of counts 2,0,0 must be 0, got %d", tv.NuancePassMedian)
	}

	// even count takes the conservative lower-middle, like medianGranularity
	samples = []SampleScore{
		nuanceSample(0, "partial", []bool{true, false}),
		nuanceSample(1, "full", []bool{true, true}),
	}
	tv, _ = AggregateTarget(r, "must_pass", samples, 2, nil, true)
	if tv.NuancePassMedian != 1 {
		t.Fatalf("even-count median of 1,2 must take lower-middle 1, got %d", tv.NuancePassMedian)
	}
}

func TestComposeVerdictNuanceWatermarkWarning(t *testing.T) {
	res := targetResult("C-01", "must_pass", true, "partial")
	res.Verdict.PassAt = "partial"
	for i := range res.Samples {
		res.Samples[i].NuancePasses = []bool{true, false, false}
	}
	res.Verdict.NuancePassMedian = 1

	in, cache := composeInputs(t, []TargetResult{res}, nil)

	// below watermark → warning
	in.Watermarks = map[string]int{"C-01": 2}
	v, _, err := ComposeVerdict(in, cache)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range v.Warnings {
		if strings.Contains(w, "C-01") && strings.Contains(w, "watermark") {
			found = true
		}
	}
	if !found {
		t.Fatalf("median 1 < watermark 2 must warn, warnings: %v", v.Warnings)
	}

	// at watermark → no warning
	in.Watermarks = map[string]int{"C-01": 1}
	v, _, err = ComposeVerdict(in, cache)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range v.Warnings {
		if strings.Contains(w, "watermark") {
			t.Fatalf("median at watermark must not warn: %v", w)
		}
	}

	// no watermark entry → no warning
	in.Watermarks = nil
	v, _, err = ComposeVerdict(in, cache)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range v.Warnings {
		if strings.Contains(w, "watermark") {
			t.Fatalf("target without watermark must not warn: %v", w)
		}
	}
}
