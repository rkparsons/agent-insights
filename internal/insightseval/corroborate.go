package insightseval

// Two-sided corroboration (spec): a matched item must contain enough of the
// effective anchors AND stay within a size cap, so over-generalization is
// never rewarded — a mega-theme containing everything fails the cap.
const (
	anchorThreshold = 0.5 // item must contain ≥ this share of effective anchors
	sizeCapFactor   = 3   // |item set| ≤ sizeCapFactor×|anchors| + sizeCapSlack
	sizeCapSlack    = 2
)

// Corroboration outcomes; the failure values double as card trigger types.
const (
	CorroborationOK          = "corroborated"
	CorroborationMismatch    = "anchor_mismatch"
	CorroborationSizeCap     = "size_cap"
	CorroborationNoAnchors   = "no_anchors"
	CorroborationCrossBucket = "non_expected_bucket"
)

// EffectiveAnchors intersects the rubric's anchors with the active population
// (BucketOutputs.Population — gap ids survive in l2 scope because the pool
// carries them; full scope already stripped them). preStrip, when non-nil,
// replaces the rubric anchors wholesale (the run-0 as_consumed control scores
// against pre-strip ground-truth ids). Duplicates never survive: anchors are
// compared as sets everywhere.
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
// source-theme ids, so an anchor-QA removal can never tighten the cap (spec:
// anchor-QA denominator split). preStrip (the as_consumed control) replaces
// both wholesale.
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
// source theme; nil falls back to anchors). Matches outside the expected
// bucket skip anchor corroboration entirely (their session sets are disjoint
// from expected-bucket anchors by construction) and are always carded by the
// caller.
func Corroborate(item ScoredItem, expectedBucket string, anchors, capAnchors []string) string {
	if item.Bucket != expectedBucket {
		return CorroborationCrossBucket
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
	if float64(hit) < anchorThreshold*float64(len(anchors)) {
		return CorroborationMismatch
	}
	if len(set) > sizeCapFactor*len(capAnchors)+sizeCapSlack {
		return CorroborationSizeCap
	}
	return CorroborationOK
}
