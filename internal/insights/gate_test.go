package insights

import "testing"

func TestSubstantialGate(t *testing.T) {
	cases := []struct {
		name string
		s    AgentSessionStats
		want bool
	}{
		{"below threshold, no friction", AgentSessionStats{AssistantTurns: 4}, false},
		{"at threshold", AgentSessionStats{AssistantTurns: 5}, true},
		{"trivial but tool error", AgentSessionStats{AssistantTurns: 1, ToolErrors: 1}, true},
		{"trivial but interrupt", AgentSessionStats{AssistantTurns: 1, Interrupts: 1}, true},
		{"trivial but rejection", AgentSessionStats{AssistantTurns: 1, Rejections: 1}, true},
		{"empty", AgentSessionStats{}, false},
	}
	for _, c := range cases {
		if got := Substantial(c.s, DefaultMinAssistantTurns); got != c.want {
			t.Errorf("%s: Substantial=%v want %v", c.name, got, c.want)
		}
	}
}
