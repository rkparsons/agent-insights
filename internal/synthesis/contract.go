package synthesis

import "github.com/rkparsons/agent-insights/internal/insights"

// BuildShowJSON returns `insights show --json`'s stdout payload: the stored
// global snapshot itself, with the contract's required arrays normalized so
// the payload never carries `null` where a consumer expects a list. found is
// false when no run has stored a snapshot yet, which yields an empty envelope
// stamped with the current contract version rather than an error.
//
// A stored snapshot keeps its own schema_version — never restamped — so a
// consumer reading a snapshot written by a different binary can name the skew
// instead of mis-rendering it as current.
//
// This lives in synthesis (not insights/contract.go, where the type
// definitions live) because insights cannot import synthesis — synthesis
// already imports insights, and Go forbids the cycle.
func BuildShowJSON(snap insights.GlobalSynthesisJSON, found bool) insights.GlobalSynthesisJSON {
	if !found {
		return insights.GlobalSynthesisJSON{
			SchemaVersion: insights.ContractVersion,
			Repos:         []insights.RepoStatsJSON{},
			Findings:      []insights.FindingJSON{},
			Dropped:       []insights.DroppedJSON{},
		}
	}
	snap.Repos = nonNil(snap.Repos)
	snap.Findings = nonNil(snap.Findings)
	snap.Dropped = nonNil(snap.Dropped)
	for i := range snap.Findings {
		snap.Findings[i].EvidenceIDs = nonNil(snap.Findings[i].EvidenceIDs)
		snap.Findings[i].Repos = nonNil(snap.Findings[i].Repos)
	}
	for i := range snap.Dropped {
		snap.Dropped[i].EvidenceIDs = nonNil(snap.Dropped[i].EvidenceIDs)
	}
	return snap
}

// nonNil normalizes a nil slice to empty: the contract's array fields are
// always-required arrays, never null.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
