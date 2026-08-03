package insights

import (
	"strings"
	"testing"
)

func runWith(stats AgentSessionStats, cell string, js ...JudgedFields) sessionRun {
	sr := sessionRun{Stats: stats, Cell: cell, ZeroFriction: isZeroFriction(stats), Frictionful: isFrictionful(stats)}
	for _, j := range js {
		sr.Repeats = append(sr.Repeats, repeat(j))
	}
	return sr
}

func TestAssembleReportHardFailNonMeta(t *testing.T) {
	// A non-meta session with a 2-class outcome jump → hard fail.
	bad := runWith(AgentSessionStats{Cwd: "/work/alpha"}, "friction-long", jf("fully_achieved"), jf("not_achieved"))
	rep := assembleReport([]sessionRun{bad})
	if !rep.HardFail {
		t.Fatal("2-class jump on a non-meta session should hard-fail")
	}
	if !strings.Contains(rep.Verdict(), "FAIL") {
		t.Errorf("verdict should be FAIL, got %q", rep.Verdict())
	}
}

func TestAssembleReportMetaExempt(t *testing.T) {
	// Same jump on a meta session → reported finding, not a hard fail.
	meta := runWith(AgentSessionStats{Cwd: "/work/insights-gen"}, "meta", jf("fully_achieved"), jf("not_achieved"))
	rep := assembleReport([]sessionRun{meta})
	if rep.HardFail {
		t.Error("a meta-only 2-class jump must not hard-fail the gate")
	}
	if len(rep.MetaFindings) == 0 {
		t.Error("the meta jump should be recorded as a finding")
	}
}

func TestAssembleReportAggregatesAndSpend(t *testing.T) {
	clean := runWith(AgentSessionStats{Cwd: "/work/alpha"}, "zero-extra", jf("fully_achieved"), jf("fully_achieved"))
	rep := assembleReport([]sessionRun{clean})
	if rep.HardFail {
		t.Error("a clean session should not hard-fail")
	}
	if rep.SchemaValidPct != 1 {
		t.Errorf("schema pct=%v want 1", rep.SchemaValidPct)
	}
	if rep.Calls != 2 || rep.NotionalSpendUSD <= 0 {
		t.Errorf("calls=%d spend=%v", rep.Calls, rep.NotionalSpendUSD)
	}
	if !strings.Contains(rep.DetectableEffect, "%") {
		t.Errorf("detectable effect should be reported: %q", rep.DetectableEffect)
	}
}

func TestVerdictSurfacesSoftFloorBreach(t *testing.T) {
	// A run with high raw fabrication: schema-valid (no hard fail) but trips the
	// raw_fabrication soft floor. The verdict headline must surface the concern.
	sr := sessionRun{
		Stats: AgentSessionStats{Cwd: "/work/alpha"}, Cell: "zero-extra", ZeroFriction: true,
		Repeats: []RepeatResult{{
			Raw: jf("fully_achieved"), Validated: jf("fully_achieved"),
			RawQuotes: []quoteCheck{{Kind: "friction", Quote: "x", Verbatim: false}},
		}},
	}
	rep := assembleReport([]sessionRun{sr})
	if rep.HardFail {
		t.Fatal("high fabrication is a soft-floor concern, not a hard fail")
	}
	if rep.RawFabricationRate <= 0.02 {
		t.Fatalf("expected raw_fabrication above the floor, got %v", rep.RawFabricationRate)
	}
	v := rep.Verdict()
	if !strings.Contains(v, "soft-floor concern") || !strings.Contains(v, "raw_fabrication") {
		t.Errorf("verdict should surface the failing soft floor, got %q", v)
	}
}

func TestAssembleReportFoldsMetaButWithholdsMetaCards(t *testing.T) {
	// F3: a contested meta session still folds into Sessions + the aggregate metrics,
	// but its cards are withheld from the human pass (display-only).
	inc := FrictionIncident{Type: "wrong_approach", OneLine: "x", EvidenceQuote: "q"}
	meta := runWith(AgentSessionStats{Cwd: "/work/insights-gen"}, "meta", jf("unclear", inc), jf("unclear", inc))
	rep := assembleReport([]sessionRun{meta})
	if len(rep.Sessions) != 1 || !rep.Sessions[0].IsMeta {
		t.Fatalf("meta session must fold into Sessions (metrics), got %d sessions", len(rep.Sessions))
	}
	if !rep.Sessions[0].Contested {
		t.Fatal("meta session should be scored contested (folded into the metrics)")
	}
	if len(rep.Cards) != 0 {
		t.Errorf("meta cards = %d, want 0 (withheld from the human pass)", len(rep.Cards))
	}
}

func TestAssembleReportEmptyFailsClosed(t *testing.T) {
	rep := assembleReport(nil)
	if !rep.HardFail {
		t.Fatal("an empty run set must fail closed, not pass vacuously")
	}
	if !strings.Contains(rep.Verdict(), "FAIL") {
		t.Errorf("empty run verdict should be FAIL, got %q", rep.Verdict())
	}
}
