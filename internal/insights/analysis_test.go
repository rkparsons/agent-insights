package insights

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnalysisJSONShape(t *testing.T) {
	a := AgentSessionAnalysis{
		Stats: AgentSessionStats{SessionID: "s1"},
		JudgedFields: JudgedFields{
			UnderlyingGoal:      "g",
			SessionType:         "single_task",
			Outcome:             "fully_achieved",
			BriefSummary:        "b",
			FrictionIncidents:   []FrictionIncident{},
			StandingPreferences: []StandingPreference{},
		},
	}
	out, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	// Judged fields flatten to top level with snake_case keys; empty arrays as [].
	for _, want := range []string{`"stats":{`, `"underlying_goal":"g"`, `"friction_incidents":[]`, `"standing_preferences":[]`} {
		if !strings.Contains(s, want) {
			t.Errorf("marshal missing %q in %s", want, s)
		}
	}
	if strings.Contains(s, `"JudgedFields"`) {
		t.Errorf("JudgedFields not flattened: %s", s)
	}
	// Stats nests under "stats" with snake_case keys (finalized in step 6).
	if !strings.Contains(s, `"session_id":"s1"`) {
		t.Errorf("nested stats missing: %s", s)
	}
}

func TestJudgedFieldsUnmarshalSnakeCase(t *testing.T) {
	in := `{"underlying_goal":"g","session_type":"exploration","outcome":"unclear",` +
		`"brief_summary":"s","friction_incidents":[{"type":"buggy_code","one_line":"x",` +
		`"evidence_quote":"q","file":"f.go"}],"standing_preferences":[{"rule":"r",` +
		`"evidence_quote":"u","scope":"pkg"}]}`
	var j JudgedFields
	if err := json.Unmarshal([]byte(in), &j); err != nil {
		t.Fatal(err)
	}
	if j.UnderlyingGoal != "g" || j.SessionType != "exploration" || j.Outcome != "unclear" || j.BriefSummary != "s" {
		t.Errorf("scalar fields wrong: %+v", j)
	}
	if len(j.FrictionIncidents) != 1 || j.FrictionIncidents[0].Type != "buggy_code" ||
		j.FrictionIncidents[0].EvidenceQuote != "q" || j.FrictionIncidents[0].File != "f.go" {
		t.Errorf("friction wrong: %+v", j.FrictionIncidents)
	}
	if len(j.StandingPreferences) != 1 || j.StandingPreferences[0].Rule != "r" ||
		j.StandingPreferences[0].EvidenceQuote != "u" || j.StandingPreferences[0].Scope != "pkg" {
		t.Errorf("prefs wrong: %+v", j.StandingPreferences)
	}
}
