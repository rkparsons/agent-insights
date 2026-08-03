package eval

import (
	"context"
	"fmt"
)

// Fixed probe sources: the safest cross-corroborated Part-A anchor, the
// canonical negative, and the canonical over-generalization (C-04's blanket
// anti-parallel form). Changing a pick or a probe's shape re-keys only the
// probe payloads.
const (
	probeRecallRubricID   = "C-01"
	probeNegativeRubricID = "N-01"
	probeNearMissRubricID = "C-04"
)

type ProbeResult struct {
	Class         string   `json:"class"` // recall | negative_recall | near_miss
	RubricID      string   `json:"rubric_id"`
	Granularities []string `json:"granularities"` // one per repeat
	Majority      string   `json:"majority"`
	Pass          bool     `json:"pass"`
}

// RunProbes runs the three matcher-integrity probes as separate matcher
// invocations (probe item + rubric only), synthetic, 3-repeat majority,
// sharing the match cache (probe payload hashes give distinct entries).
// Excluded from verdict aggregates and cards; fail-closed enforcement is the
// caller's duty. The near-miss probe is the drift detector for the direction
// that inflates scores: a maximally generous matcher passes the other two.
func RunProbes(ctx context.Context, cache *Cache, m Matcher, envHash string, rubrics []Rubric, repeats int) ([]ProbeResult, error) {
	byID := map[string]Rubric{}
	for _, r := range rubrics {
		byID[r.ID] = r
	}
	probes := []struct {
		class, rubricID, surface string
		text                     func(Rubric) (string, error)
		pass                     func(majority string) bool
	}{
		{"recall", probeRecallRubricID, "theme",
			func(r Rubric) (string, error) { return r.Statement, nil },
			func(maj string) bool { return granularityRank[maj] >= granularityRank["partial"] }},
		{"negative_recall", probeNegativeRubricID, "recommendation",
			firstForbidden,
			func(maj string) bool { return maj != "absent" }},
		{"near_miss", probeNearMissRubricID, "theme",
			firstForbidden,
			func(maj string) bool { return granularityRank[maj] <= granularityRank["over_generalized"] }},
	}
	var out []ProbeResult
	for _, p := range probes {
		r, ok := byID[p.rubricID]
		if !ok {
			return nil, fmt.Errorf("probe %s: rubric %s not loaded", p.class, p.rubricID)
		}
		text, err := p.text(r)
		if err != nil {
			return nil, fmt.Errorf("probe %s: %w", p.class, err)
		}
		item := ScoredItem{ID: "probe/" + p.class, Bucket: "probe", Surface: p.surface, Text: text}
		payload := BuildMatchPayload(r, []ScoredItem{item})
		if len(payload.Items) != 1 {
			return nil, fmt.Errorf("probe %s: item filtered out (rubric surface %q vs probe surface %q)", p.class, r.Surface, p.surface)
		}
		grans := make([]string, 0, repeats)
		for k := 0; k < repeats; k++ {
			res, _, err := matchOnce(ctx, cache, m, envHash, payload, k)
			if err != nil {
				return nil, fmt.Errorf("probe %s repeat %d: %w", p.class, k, err)
			}
			g := "absent"
			for _, mm := range res.Matches {
				if mm.ItemID == item.ID && granularityRank[mm.Granularity] > granularityRank[g] {
					g = mm.Granularity
				}
			}
			grans = append(grans, g)
		}
		maj := medianGranularity(grans)
		out = append(out, ProbeResult{Class: p.class, RubricID: p.rubricID,
			Granularities: grans, Majority: maj, Pass: p.pass(maj)})
	}
	return out, nil
}

func firstForbidden(r Rubric) (string, error) {
	if len(r.ForbiddenGeneralizations) == 0 {
		return "", fmt.Errorf("rubric %s has no forbidden_generalizations", r.ID)
	}
	return r.ForbiddenGeneralizations[0], nil
}
