// Package synthesis aggregates the pipeline's insights.AgentSessionAnalysis
// records (Layer 1 output) into one cross-repo, ranked GlobalSynthesis: each
// finding names a practice and the cheapest asset that would fix it. Every
// number, date, repo set and key is computed here in Go; a single claude -p
// pass over the per-repo evidence bundles does qualitative-only work against
// Go-assigned typed ids, and a deterministic verifier fails the run closed
// before anything is stored.
package synthesis
