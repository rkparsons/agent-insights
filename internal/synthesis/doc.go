// Package synthesis aggregates a repo's insights.AgentSessionAnalysis records
// (Layer 1 output) into a ranked, evidence-grounded RepoSynthesis: friction and
// opportunity themes plus typed recommendations. Numbers and ranking are computed
// here in Go; a single claude -p pass does qualitative-only work referencing
// Go-assigned typed ids.
package synthesis
