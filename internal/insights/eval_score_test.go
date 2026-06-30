package insights

import "testing"

func jf(outcome string, friction ...FrictionIncident) JudgedFields {
	return JudgedFields{
		UnderlyingGoal: "g", SessionType: "single_task", Outcome: outcome, BriefSummary: "s",
		FrictionIncidents: friction, StandingPreferences: []StandingPreference{},
	}
}

func repeat(j JudgedFields) RepeatResult { return RepeatResult{Raw: j, Validated: j} }

func TestScoreOutcomeStability(t *testing.T) {
	// fully vs partially = ordinal 4 vs 2 = 2-class jump.
	jump := sessionRun{Repeats: []RepeatResult{repeat(jf("fully_achieved")), repeat(jf("partially_achieved"))}}
	if !scoreSession(jump).TwoClassJump {
		t.Error("fully+partially should be a 2-class jump")
	}
	// mostly vs partially = ordinal 3 vs 2 = adjacent, no jump.
	adj := sessionRun{Repeats: []RepeatResult{repeat(jf("mostly_achieved")), repeat(jf("partially_achieved"))}}
	if scoreSession(adj).TwoClassJump {
		t.Error("mostly+partially is adjacent, not a jump")
	}
	// unclear is off-scale → never a jump.
	unc := sessionRun{Repeats: []RepeatResult{repeat(jf("unclear")), repeat(jf("fully_achieved"))}}
	if scoreSession(unc).TwoClassJump {
		t.Error("unclear is off-scale, not a jump")
	}
}

func TestScoreFalseFrictionAndRecall(t *testing.T) {
	inc := FrictionIncident{Type: "wrong_approach", OneLine: "x"}
	// Zero-friction session that produced an incident → false-friction candidate.
	ff := sessionRun{ZeroFriction: true, Repeats: []RepeatResult{repeat(jf("fully_achieved", inc))}}
	if !scoreSession(ff).FalseFriction {
		t.Error("incident on zero-friction session → false-friction")
	}
	// Frictionful session reporting zero friction in a majority of repeats → recall miss.
	rm := sessionRun{Frictionful: true, Repeats: []RepeatResult{repeat(jf("fully_achieved")), repeat(jf("fully_achieved")), repeat(jf("fully_achieved", inc))}}
	if !scoreSession(rm).RecallMiss {
		t.Error("frictionful session mostly reporting no friction → recall miss")
	}
}

func TestScoreTypeStabilityAndFabrication(t *testing.T) {
	a := FrictionIncident{Type: "wrong_approach", OneLine: "phrased one way"}
	b := FrictionIncident{Type: "wrong_approach", OneLine: "phrased differently"}
	// Same type both repeats (different one_line) → no churn, Jaccard 1.
	stable := sessionRun{Repeats: []RepeatResult{repeat(jf("fully_achieved", a)), repeat(jf("fully_achieved", b))}}
	ss := scoreSession(stable)
	if ss.TypeChurn || ss.TypeJaccard != 1 {
		t.Errorf("same-type incidents should be stable: churn=%v jaccard=%v", ss.TypeChurn, ss.TypeJaccard)
	}
	// A type present in one repeat, absent in the other → churn.
	churn := sessionRun{Repeats: []RepeatResult{repeat(jf("fully_achieved", a)), repeat(jf("fully_achieved"))}}
	if !scoreSession(churn).TypeChurn {
		t.Error("appearing/vanishing type → churn")
	}
	// Raw fabrication: one verbatim, one not.
	fab := sessionRun{Repeats: []RepeatResult{{RawQuotes: []quoteCheck{{Verbatim: true}, {Verbatim: false}}}}}
	fs := scoreSession(fab)
	if fs.RawQuotes != 2 || fs.RawFabricated != 1 {
		t.Errorf("fabrication: quotes=%d fabricated=%d", fs.RawQuotes, fs.RawFabricated)
	}
}

func TestSchemaValid(t *testing.T) {
	if !schemaValid(jf("fully_achieved")) {
		t.Error("valid fields should pass")
	}
	if schemaValid(jf("not_a_real_outcome")) {
		t.Error("bad outcome enum should fail")
	}
	bad := jf("fully_achieved", FrictionIncident{Type: "nonsense", OneLine: "x"})
	if schemaValid(bad) {
		t.Error("bad friction type should fail")
	}
}
