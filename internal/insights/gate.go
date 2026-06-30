package insights

// DefaultMinAssistantTurns is the substantial-session cut (per-message AssistantTurns,
// recalibrated from the spike's per-line >=12). A cost/triviality cut, not a quality
// guard — short sessions analyze cleanly and return no friction.
const DefaultMinAssistantTurns = 5

// Substantial reports whether a session is worth an LLM analysis: enough assistant
// turns, or any friction signal regardless of length.
func Substantial(s AgentSessionStats, minAssistantTurns int) bool {
	if s.AssistantTurns >= minAssistantTurns {
		return true
	}
	return s.ToolErrors > 0 || s.Interrupts > 0 || s.Rejections > 0
}
