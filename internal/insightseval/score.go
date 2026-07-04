package insightseval

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"
)

const matcherAttempts = 3

// matchOnce serves one (payload, repeat) from the match cache or the matcher,
// retrying transient failures. Output is validated against the payload before
// caching — an inconsistent read is a failed attempt, never a cached lie. The
// key hashes the exact stdin payload: anything the matcher can see re-keys,
// nothing else does (see plan decision 4).
func matchOnce(ctx context.Context, cache *Cache, m Matcher, envHash string, p MatchPayload, repeat int) (MatchResult, bool, error) {
	pj, err := json.Marshal(p)
	if err != nil {
		return MatchResult{}, false, err
	}
	key := cacheKey("match", sha256hex(pj), MatcherModel, MatcherCodeVersion(), envHash, strconv.Itoa(repeat))
	var res MatchResult
	hit, err := cache.Get("match", key, &res)
	if err != nil {
		return res, false, err
	}
	if hit {
		return res, true, nil
	}
	var lastErr error
	for attempt := 0; attempt < matcherAttempts; attempt++ {
		mctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		res, lastErr = m.Match(mctx, p)
		cancel()
		if lastErr == nil {
			lastErr = validateMatchResult(p, res)
		}
		if lastErr == nil {
			return res, false, cache.Put("match", key, res)
		}
	}
	return MatchResult{}, false, fmt.Errorf("matcher failed after %d attempts: %w", matcherAttempts, lastErr)
}

// medianGranularity picks the middle of the ordered granularity scale: the
// majority whenever one exists, the conservative lower-middle on even counts.
func medianGranularity(grans []string) string {
	if len(grans) == 0 {
		return "absent"
	}
	sorted := append([]string(nil), grans...)
	sort.Slice(sorted, func(i, j int) bool { return granularityRank[sorted[i]] < granularityRank[sorted[j]] })
	return sorted[(len(sorted)-1)/2]
}

// SideMatch is an uncounted match worth carding: a corroboration failure that
// would outrank the counted outcome, or any non-expected-bucket match.
type SideMatch struct {
	Ref           string
	Text          string
	Granularity   string
	Corroboration string
	SessionIDs    []string
}

// SampleScore is one target's outcome on one L2 sample, with the local-only
// detail cards and adjudication keys need. Only refs and numbers reach
// committed verdicts.
type SampleScore struct {
	SampleIndex     int
	Granularity     string
	RepeatAgreement float64
	Corroboration   string
	ItemRef         string
	ItemText        string
	ItemSessionIDs  []string
	ItemQuotes      []string
	NuancePasses    []bool
	SideMatches     []SideMatch
	AdjApplied      []string
}

type repeatScore struct {
	Granularity    string
	Corroboration  string
	ItemRef        string
	ItemText       string
	ItemSessionIDs []string
	ItemQuotes     []string
	NuancePasses   []bool
	SideMatches    []SideMatch
	AdjApplied     []string
}

// aggregateRepeat folds one matcher read into a target outcome: forbidden-form
// cap first (one bad item caps the whole target at over_generalized — spec),
// then deterministic corroboration; the best counted match decides
// (corroborated, anchorless, or human-accepted via adjudication). Uncounted
// matches that outrank the decision — and every cross-bucket match — are kept
// as side matches for cards.
func aggregateRepeat(r Rubric, items map[string]ScoredItem, res MatchResult, anchors []string, adj map[string]Adjudication) repeatScore {
	out := repeatScore{Granularity: "absent"}
	type cand struct {
		gran, corro string
		item        ScoredItem
		nuances     []bool
		counted     bool
		adjHash     string
	}
	forbidden := false
	var cands []cand
	for _, m := range res.Matches {
		it := items[m.ItemID]
		gran := m.Granularity
		if len(m.ForbiddenFormsMatched) > 0 {
			gran = "over_generalized"
			forbidden = true
		}
		if gran == "full" && !allTrue(m.NuanceResults) {
			gran = "partial" // full requires every nuance; enforce even if the matcher slipped
		}
		c := cand{gran: gran, corro: Corroborate(it, r.Repos[0], anchors), item: it, nuances: m.NuanceResults}
		switch c.corro {
		case CorroborationOK, CorroborationNoAnchors:
			c.counted = true
		case CorroborationMismatch, CorroborationSizeCap:
			k := AdjKey{TargetID: r.ID, Statement: normalizeStatement(it.Text),
				IDSetHash: idSetHash(it.SessionIDs), RubricHash: r.Hash, Trigger: c.corro}
			if a, ok := adj[k.Hash()]; ok && a.Decision == "accept" {
				c.counted = true
				c.adjHash = k.Hash()
			}
		}
		cands = append(cands, c)
	}
	best := -1
	for i, c := range cands {
		if !c.counted {
			continue
		}
		if best < 0 || granularityRank[c.gran] > granularityRank[cands[best].gran] ||
			(granularityRank[c.gran] == granularityRank[cands[best].gran] && c.item.ID < cands[best].item.ID) {
			best = i
		}
	}
	if best >= 0 {
		b := cands[best]
		out.Granularity = b.gran
		out.Corroboration = b.corro
		out.ItemRef = b.item.ID
		out.ItemText = b.item.Text
		out.ItemSessionIDs = b.item.SessionIDs
		out.ItemQuotes = b.item.Quotes
		out.NuancePasses = b.nuances
		if b.adjHash != "" {
			out.AdjApplied = append(out.AdjApplied, b.adjHash)
		}
	}
	if forbidden && granularityRank[out.Granularity] > granularityRank["over_generalized"] {
		out.Granularity = "over_generalized"
	}
	for _, c := range cands {
		if c.counted {
			continue
		}
		if c.corro == CorroborationCrossBucket || granularityRank[c.gran] > granularityRank[out.Granularity] {
			out.SideMatches = append(out.SideMatches, SideMatch{
				Ref: c.item.ID, Text: c.item.Text, Granularity: c.gran,
				Corroboration: c.corro, SessionIDs: c.item.SessionIDs,
			})
		}
	}
	return out
}

// scoreTargetSample scores one (rubric, sample): k matcher repeats stabilize
// the read (median = majority when one exists), and the first repeat voting
// the median carries the detail forward. An empty payload — no items after
// surface/bucket filtering — is absent without an LLM call (fail-closed
// against wasted spend, not against detection: nothing to detect).
func scoreTargetSample(ctx context.Context, cache *Cache, m Matcher, envHash string, r Rubric, items []ScoredItem, anchors []string, adj map[string]Adjudication, sampleIndex, repeats int) (SampleScore, error) {
	if repeats < 1 {
		return SampleScore{}, fmt.Errorf("%s sample %d: repeats must be >= 1, got %d", r.ID, sampleIndex, repeats)
	}
	payload := BuildMatchPayload(r, items)
	if len(payload.Items) == 0 {
		return SampleScore{SampleIndex: sampleIndex, Granularity: "absent", RepeatAgreement: 1}, nil
	}
	byID := map[string]ScoredItem{}
	for _, it := range items {
		byID[it.ID] = it
	}
	var reps []repeatScore
	for k := 0; k < repeats; k++ {
		res, _, err := matchOnce(ctx, cache, m, envHash, payload, k)
		if err != nil {
			return SampleScore{}, fmt.Errorf("%s sample %d repeat %d: %w", r.ID, sampleIndex, k, err)
		}
		reps = append(reps, aggregateRepeat(r, byID, res, anchors, adj))
	}
	grans := make([]string, len(reps))
	for i, rep := range reps {
		grans[i] = rep.Granularity
	}
	median := medianGranularity(grans)
	agree, pick := 0, -1
	for i, rep := range reps {
		if rep.Granularity == median {
			agree++
			if pick < 0 {
				pick = i
			}
		}
	}
	out := SampleScore{SampleIndex: sampleIndex, Granularity: median,
		RepeatAgreement: float64(agree) / float64(len(reps))}
	rep := reps[pick]
	out.Corroboration = rep.Corroboration
	out.ItemRef = rep.ItemRef
	out.ItemText = rep.ItemText
	out.ItemSessionIDs = rep.ItemSessionIDs
	out.ItemQuotes = rep.ItemQuotes
	out.NuancePasses = rep.NuancePasses
	out.SideMatches = rep.SideMatches
	out.AdjApplied = rep.AdjApplied
	return out, nil
}

// scoreNegativeSample flags violations of one negative rubric on one sample:
// a majority of repeats reporting any match is a violation (soft-fail —
// surfaced in the verdict, never a hard fail). Negatives have no expected
// bucket and no corroboration channel; the negative-recall probe guards this
// path's detection power.
func scoreNegativeSample(ctx context.Context, cache *Cache, m Matcher, envHash string, r Rubric, items []ScoredItem, repeats int) (bool, []string, error) {
	if repeats < 1 {
		return false, nil, fmt.Errorf("%s: repeats must be >= 1, got %d", r.ID, repeats)
	}
	payload := BuildMatchPayload(r, items)
	if len(payload.Items) == 0 {
		return false, nil, nil
	}
	violations := 0
	var refs []string
	for k := 0; k < repeats; k++ {
		res, _, err := matchOnce(ctx, cache, m, envHash, payload, k)
		if err != nil {
			return false, nil, fmt.Errorf("%s repeat %d: %w", r.ID, k, err)
		}
		if len(res.Matches) == 0 {
			continue
		}
		violations++
		if refs == nil {
			for _, mm := range res.Matches {
				refs = append(refs, mm.ItemID)
			}
			refs = sortedSet(refs)
		}
	}
	if violations*2 <= repeats {
		return false, nil, nil
	}
	return true, refs, nil
}
