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
	bad := runWith(AgentSessionStats{Cwd: "/work/client-project"}, "friction-long", jf("fully_achieved"), jf("not_achieved"))
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
	clean := runWith(AgentSessionStats{Cwd: "/work/client-project"}, "zero-extra", jf("fully_achieved"), jf("fully_achieved"))
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
