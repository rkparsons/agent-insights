package eval

// Two-sided corroboration (spec): a matched item must contain enough of the
// effective anchors AND stay within a size cap, so over-generalization is
// never rewarded — a mega-finding containing everything fails the cap.
//
// Every v2 finding's session set is recovered from its cited evidence ids, so
// every finding gets the grounding alternative to the recall bar (v1's
// rec-surface corroboration amendment 2026-07-09, whose rationale was exactly
// an evidence-derived session set): it is checked for being drawn FROM the
// anchors (precision), not for covering them.
const (
	anchorThreshold = 0.5 // item must contain ≥ this share of effective anchors
	groundPrecision = 0.5 // grounding alternative: ≥ this share of item sessions are anchors
	sizeCapFactor   = 3   // |item set| ≤ sizeCapFactor×|anchors| + sizeCapSlack
	sizeCapSlack    = 2
)

// Corroboration outcomes; the failure values double as card trigger types.
// Grounded is a counted outcome distinct from OK so verdicts show which path
// corroborated and the HIGH-tier oversight card can fire on grounding-only
// passes (amendment review C1).
//
// v1's non_expected_bucket rejection is gone: scoring is global now, and a
// finding that merges two repos' evidence is the contract working, not
// cross-bucket contamination. CorroborationDropped takes its place as the
// never-counted-but-always-carded outcome.
const (
	CorroborationOK        = "corroborated"
	CorroborationGrounded  = "grounded"
	CorroborationMismatch  = "anchor_mismatch"
	CorroborationSizeCap   = "size_cap"
	CorroborationNoAnchors = "no_anchors"
	CorroborationDropped   = "dropped"
)

// EffectiveAnchors intersects the rubric's anchors with the active population
// (the union of every bucket's population — gap ids survive in l2 scope because
// the pool carries them; full scope already stripped them). The union is what
// lets a rubric anchored across repos keep all its anchors. preStrip, when
// non-nil, replaces the rubric anchors wholesale (the run-0 as_consumed control
// scores against pre-strip ground-truth ids). Duplicates never survive: anchors
// are compared as sets everywhere.
func EffectiveAnchors(r Rubric, population []string, preStrip []string) []string {
	anchors := r.AnchorSessionIDs
	if preStrip != nil {
		anchors = preStrip
	}
	pop := stringSet(population)
	var out []string
	for _, id := range sortedSet(anchors) {
		if pop[id] {
			out = append(out, id)
		}
	}
	return out
}

// AnchorSets computes one rubric's two effective anchor denominators: hits
// count against the kept (post-QA) anchors, the size cap against the pre-QA
// source ids, so an anchor-QA removal can never tighten the cap (spec:
// anchor-QA denominator split). Both are combined sets under v2 — a rubric
// anchored on a cross-repo finding carries every repo's sessions — so the cap a
// merged finding is measured against is the combined one. preStrip (the
// as_consumed control) replaces both wholesale.
func AnchorSets(r Rubric, population []string, preStrip []string) (anchors, capAnchors []string) {
	anchors = EffectiveAnchors(r, population, preStrip)
	capSource := preStrip
	if capSource == nil {
		capSource = r.SourceThemeSessionIDs
	}
	if capSource == nil {
		return anchors, anchors
	}
	return anchors, EffectiveAnchors(r, population, capSource)
}

// Corroborate classifies one matched item's session set: anchor hits against
// the kept anchors, the size cap against capAnchors (the effective pre-QA
// source set; nil falls back to anchors). Repo membership is not a criterion —
// a merged finding is measured against the same combined anchors as any other.
// A dropped entry is never counted whatever its sessions say; the caller cards
// it, which is how a wrongly-dropped finding surfaces as a recall miss.
func Corroborate(item ScoredItem, anchors, capAnchors []string) string {
	if item.Dropped {
		return CorroborationDropped
	}
	if len(anchors) == 0 {
		return CorroborationNoAnchors
	}
	if len(capAnchors) == 0 {
		capAnchors = anchors
	}
	set := stringSet(item.SessionIDs)
	hit := 0
	for _, a := range anchors {
		if set[a] {
			hit++
		}
	}
	recall := float64(hit) >= anchorThreshold*float64(len(anchors))
	grounded := item.Surface == surfaceFinding && hit >= 1 &&
		float64(hit) >= groundPrecision*float64(len(set))
	if !recall && !grounded {
		return CorroborationMismatch
	}
	if len(set) > sizeCapFactor*len(capAnchors)+sizeCapSlack {
		return CorroborationSizeCap
	}
	if !recall {
		return CorroborationGrounded
	}
	return CorroborationOK
}
