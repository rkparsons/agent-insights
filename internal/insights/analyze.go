package insights

import (
	"context"

	"tmux-ctrl/internal/transcript"
)

// Analyze produces one complete AgentSessionAnalysis for a session: it extracts the
// deterministic stats + reduced input, asks the Judge for the model-judged fields,
// validates evidence quotes against the transcript, and merges. A Judge error aborts
// (no partial artifact). The caller's ctx governs the subprocess timeout; a context
// with no deadline means no timeout — the step-6 caller must set one.
func Analyze(
	ctx context.Context,
	events []transcript.TranscriptEvent, canary transcript.Canary,
	sessionID string, repo RepoResolver, judge Judge,
) (AgentSessionAnalysis, ValidationReport, error) {
	ext := Extract(events, canary, sessionID, repo)
	judged, err := judge.Judge(ctx, ext.Reduced)
	if err != nil {
		return AgentSessionAnalysis{}, ValidationReport{}, err
	}
	validated, report := validateQuotes(judged, ext.Verbatim)
	return AgentSessionAnalysis{Stats: ext.Stats, JudgedFields: validated}, report, nil
}
