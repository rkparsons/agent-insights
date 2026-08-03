package eval

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rkparsons/agent-insights/internal/insights"
)

func qaAnalysis(goal string, oneLines ...string) insights.AgentSessionAnalysis {
	var a insights.AgentSessionAnalysis
	a.Stats.Cwd = "/Users/x/Developer/secret"
	a.Stats.GitBranch = "secret-branch"
	a.UnderlyingGoal = goal
	a.SessionType = "single_task"
	a.Outcome = "achieved"
	a.BriefSummary = "summary of " + goal
	for _, l := range oneLines {
		a.FrictionIncidents = append(a.FrictionIncidents, insights.FrictionIncident{
			Type: "user_rejected_action", OneLine: l, EvidenceQuote: "quote for " + l})
	}
	a.StandingPreferences = []insights.StandingPreference{{Rule: "rule-1", EvidenceQuote: "q1"}}
	return a
}

func TestBuildJudgeInputCarriesFullPoolRecordAndStaysBlind(t *testing.T) {
	r := Rubric{ID: "C-77", Statement: "the behavior", RequiredNuances: []string{"n1", "n2"},
		AnchorSessionIDs:      []string{"s1"},
		SourceThemeSessionIDs: []string{"s1", "s2"}}
	entries := map[string]insights.AgentSessionAnalysis{
		"s1": qaAnalysis("goal-1", "incident-1a", "incident-1b"),
		"s2": qaAnalysis("goal-2", "incident-2a"),
	}
	in, err := buildJudgeInput(r, entries)
	if err != nil {
		t.Fatal(err)
	}
	if in.Rubric.ID != "C-77" || in.Rubric.Statement != "the behavior" || len(in.Rubric.RequiredNuances) != 2 {
		t.Fatalf("rubric payload: %+v", in.Rubric)
	}
	// the judge re-selects from the full pre-QA source theme, not the kept set
	if len(in.Sessions) != 2 || in.Sessions[0].SessionID != "s1" || in.Sessions[1].SessionID != "s2" {
		t.Fatalf("sessions: %+v", in.Sessions)
	}
	s1 := in.Sessions[0]
	if len(s1.FrictionIncidents) != 2 || s1.FrictionIncidents[1].OneLine != "incident-1b" {
		t.Fatalf("full incident list must survive: %+v", s1.FrictionIncidents)
	}
	if len(s1.StandingPreferences) != 1 || s1.BriefSummary == "" || s1.UnderlyingGoal != "goal-1" {
		t.Fatalf("pool-side record incomplete: %+v", s1)
	}
	// blinding: nothing outside the judged pool fields may reach the judge
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"secret", "stats", "cwd", "branch"} {
		if strings.Contains(string(raw), leak) {
			t.Fatalf("judge input leaks %q: %s", leak, raw)
		}
	}
	// a source-theme id with no pool entry is a hard error, never a silent skip
	r.SourceThemeSessionIDs = []string{"s1", "s2", "s3"}
	if _, err := buildJudgeInput(r, entries); err == nil || !strings.Contains(err.Error(), "s3") {
		t.Fatalf("missing pool entry must fail loudly: %v", err)
	}
}

func TestValidateQAVerdictsCoverageAndValues(t *testing.T) {
	in := qaInput{Sessions: []qaSession{{SessionID: "s1"}, {SessionID: "s2"}}}
	ok := qaResult{Verdicts: []qaVerdict{
		{SessionID: "s1", Verdict: "keep", Rationale: "evidences the behavior"},
		{SessionID: "s2", Verdict: "remove", Rationale: "bycatch of a broader theme"},
	}}
	if err := validateQAVerdicts(in, ok); err != nil {
		t.Fatal(err)
	}
	cases := map[string]qaResult{
		"missing session": {Verdicts: ok.Verdicts[:1]},
		"unknown session": {Verdicts: append(append([]qaVerdict(nil), ok.Verdicts...), qaVerdict{SessionID: "s9", Verdict: "keep", Rationale: "r"})},
		"duplicate session": {Verdicts: []qaVerdict{ok.Verdicts[0], {SessionID: "s1", Verdict: "remove", Rationale: "r"}}},
		"bad verdict": {Verdicts: []qaVerdict{ok.Verdicts[0], {SessionID: "s2", Verdict: "maybe", Rationale: "r"}}},
		"empty rationale": {Verdicts: []qaVerdict{ok.Verdicts[0], {SessionID: "s2", Verdict: "remove", Rationale: ""}}},
	}
	for name, res := range cases {
		if err := validateQAVerdicts(in, res); err == nil {
			t.Errorf("%s must fail validation", name)
		}
	}
}
