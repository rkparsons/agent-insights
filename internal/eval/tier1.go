package eval

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/rkparsons/agent-insights/internal/insights"
	"github.com/rkparsons/agent-insights/internal/synthesis"
)

// Tier-1 thresholds. Fabrication keeps the v1 gate; churn has no established
// gate, so it warns.
const (
	fabricationHardFailRate = 0.15
	churnWarnThreshold      = 0.5
	// substantiveSignalMagnitude restates the bundle's own signal floor, as v1's
	// probe did. Honest accounting: against a real bundle it filters NOTHING —
	// computeSignals sets Magnitude = len(MemberSessions) and emits only signals
	// at or above that floor. It is kept as a defensive restatement so a
	// bundle-side floor change cannot silently widen what this gate calls
	// substantive: the eval owns its own definition.
	substantiveSignalMagnitude = 3
	// prefClusterFloor is the "recurring" bar: a practice stated in fewer P*
	// items than this is a one-off, and skipping it is not a recall miss.
	prefClusterFloor = 3
	// checkableQuotesPerFinding is the raw schema's per-finding quote cap (skill
	// schema: quotes maxItems 3). The verifier trims past the cap BEFORE its
	// pool check, so quotes beyond it were never checkable and must not sit in
	// the fabrication denominator.
	checkableQuotesPerFinding = 3
)

// The two "same practice?" bars, pinned to the frozen preference corpus rather
// than borrowed from the bundle's directive clustering: prose rules score far
// lower than directive clauses, and the borrowed 0.6 yields ZERO clusters at
// the recurrence floor over the frozen pool — see
// TestProbeSimilarityBarsPinnedToFrozenPool for the census and the band
// evidence. They are split because the two questions have opposite failure
// costs: a loose preference bar merges distinct practices into a phantom
// cluster (a false miss, human attention wasted), while a tight churn bar
// leaves same-practice restatements unmatched and scores them maximal churn
// (an unconditional stability warning, signal destroyed).
const (
	prefClusterSimilarity = 0.5  // 1 real cluster (5 forms) over the frozen pool; 0.45 starts merging practices
	churnMatchSimilarity  = 0.45 // inside the 0.40-0.50 band, every inspected pair of which restates one practice
)

// The dropped-suppression cards hang off a pseudo-target: they belong to the
// trust gates, not to any rubric.
const (
	tier1CardTarget    = "tier1"
	tier1CardStatement = "trust gate — evidence the model dropped instead of acting on: is the drop right, or is it laundering a recall miss?"
	droppedSuppression = "dropped_suppression"
)

// quoteDropNote matches the verifier's quote-drop note. meta.validation_notes
// is the specified source for the fabrication signal: it counts what the model
// claimed and Go could not find, before any correction reached the snapshot.
var quoteDropNote = regexp.MustCompile(`dropped (\d+) quote\(s\)`)

// recallProbe is one piece of bundle evidence the contract expects a finding to
// engage with. ID is the committed identity (bundle ids or a repo key — never
// prose); IDs is the citation set that clears it.
type recallProbe struct {
	Kind string // "opportunity" | "preference" | "friction"
	ID   string
	IDs  []string
}

// droppedEntry is one dropped list entry with its resolved session set. IDs and
// Cites are the same citations in the two shapes the probes need: a list to ask
// "did any finding engage this drop's evidence", a set to ask "does this drop
// cite the probe's evidence".
type droppedEntry struct {
	Summary  string
	Reason   string
	IDs      []string
	Cites    map[string]bool
	Sessions []string
}

// key identifies one drop for adjudication: its normalized summary and the
// sessions behind its evidence, stable across samples that repeat it.
func (d droppedEntry) key() AdjKey {
	return AdjKey{TargetID: tier1CardTarget, Statement: normalizeStatement(d.Summary),
		IDSetHash: idSetHash(d.Sessions), Trigger: droppedSuppression}
}

// tier1Sample is one L2 sample reduced to what the trust probes read: RAW
// citations and quotes (the model's own claims, before Go corrected anything),
// the shipped snapshot's emptiness and validation notes, and the Go-computed
// session sets churn compares.
type tier1Sample struct {
	Index        int
	Fresh        bool
	Empty        bool
	FindingCites map[string]bool
	Dropped      []droppedEntry
	// CheckableQuotes is the fabrication denominator: quotes the model cited
	// that the verifier actually pool-checked — raw quotes capped per finding,
	// since it trims past the cap before checking.
	CheckableQuotes int
	QuoteDrops      int
	ProseLeaks      int
	Items           []ScoredItem
}

// ComputeTier1 embeds the trust-property gates in the verdict, recast for the
// v2 contract (spec §Eval adaptation):
//
//   - preference/opportunity/friction recall against findings AND dropped —
//     v1's "prefs present but zero claude_md_rule" inverts under the asset
//     ladder, where zero claude_md_rule is a legal and often better answer;
//   - membership churn over findings' Go-computed session sets, matched across
//     runs by statement similarity;
//   - the raw quote-drop rate from meta.validation_notes.
//
// A dropped citation suppresses a recall probe (else legitimate drops flood the
// gate with false misses) and cards the drop for a human ruling in exchange.
// Returns gates, hard-fail reasons, warnings, and the dropped cards.
func ComputeTier1(record RunRecord, cache *Cache, adj map[string]Adjudication) (Tier1Gates, []string, []string, []PendingCard, error) {
	t1 := Tier1Gates{HardErrorCount: len(record.VerifierRejections)}
	var reasons, warnings []string

	bundles, err := tier1Bundles(record, cache)
	if err != nil {
		return t1, nil, nil, nil, err
	}
	samples, err := tier1Samples(record, cache, bundles)
	if err != nil {
		return t1, nil, nil, nil, err
	}
	probes := recallProbes(bundles)

	misses := map[string]map[string]bool{}
	suppressed := map[string]bool{}
	var cards []PendingCard
	seenCard := map[string]bool{}
	for _, s := range samples {
		if s.Empty {
			reasons = append(reasons, fmt.Sprintf("sample %d: empty synthesis output (fail-closed)", s.Index))
		}
		if s.CheckableQuotes > 0 {
			if rate := float64(s.QuoteDrops) / float64(s.CheckableQuotes); rate > t1.MaxRawFabricationRate {
				t1.MaxRawFabricationRate = rate
			}
		}
		t1.ReportPrivacyLeakCount += s.ProseLeaks
		floorsHeld := map[string][]string{} // drop key hash → the floors it is holding up
		for _, p := range probes {
			if citesAny(s.FindingCites, p.IDs) {
				continue
			}
			suppressing := suppressingDrops(s.Dropped, p.IDs)
			if len(suppressing) == 0 {
				if misses[p.Kind] == nil {
					misses[p.Kind] = map[string]bool{}
				}
				misses[p.Kind][p.ID] = true
				continue
			}
			suppressed[p.Kind+"/"+p.ID] = true // deduped across samples, as misses are
			for _, d := range suppressing {
				h := d.key().Hash()
				floorsHeld[h] = append(floorsHeld[h], p.Kind+" floor "+p.ID)
			}
		}
		// The carding bargain: every drop the findings did not engage goes to
		// the human, whether or not it is holding up a floor. A drop nobody
		// acted on and nobody corroborated is exactly where a recall miss can
		// be laundered through a plausible-sounding reason.
		for _, d := range s.Dropped {
			held := floorsHeld[d.key().Hash()]
			if len(held) == 0 && citesAny(s.FindingCites, d.IDs) {
				continue
			}
			if c, ok := droppedCard(d, held, adj, seenCard); ok {
				cards = append(cards, c)
			}
		}
	}
	t1.DroppedSuppressions = len(suppressed)
	t1.OpportunityRecallMisses = sortedKeys(misses["opportunity"])
	t1.PrefRecallMisses = sortedKeys(misses["preference"])
	t1.FrictionRecallMisses = sortedKeys(misses["friction"])

	churnSum, churnN := 0.0, 0
	for i := 0; i+1 < len(samples); i++ {
		if !samples[i].Fresh || !samples[i+1].Fresh {
			continue
		}
		churnSum += membershipChurn(samples[i].Items, samples[i+1].Items)
		churnN++
	}
	t1.FreshSamplePairs = churnN
	if churnN > 0 {
		churn := churnSum / float64(churnN)
		t1.MembershipChurn = &churn
		if churn > churnWarnThreshold {
			warnings = append(warnings, fmt.Sprintf("membership churn %.2f exceeds %.2f across %d fresh pairs", churn, churnWarnThreshold, churnN))
		}
	}

	if t1.MaxRawFabricationRate > fabricationHardFailRate {
		reasons = append(reasons, fmt.Sprintf("raw fabrication rate %.3f exceeds %.2f", t1.MaxRawFabricationRate, fabricationHardFailRate))
	}
	if t1.ReportPrivacyLeakCount > 0 {
		reasons = append(reasons, fmt.Sprintf("%d privacy leak(s) in raw model prose", t1.ReportPrivacyLeakCount))
	}
	for _, g := range t1.OpportunityRecallMisses {
		warnings = append(warnings, "opportunity recall miss: no finding engages signal "+g)
	}
	for _, p := range t1.PrefRecallMisses {
		warnings = append(warnings, "pref recall miss: no finding engages the recurring preference cluster "+p)
	}
	for _, f := range t1.FrictionRecallMisses {
		warnings = append(warnings, "friction recall miss: no finding cites any friction item from repo "+f)
	}
	if t1.DroppedSuppressions > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"%d recall floor(s) suppressed by a dropped citation; an unjustified drop is a laundered miss",
			t1.DroppedSuppressions))
	}
	if len(cards) > 0 {
		warnings = append(warnings, fmt.Sprintf(
			"%d dropped entr(ies) carded for a ruling — evidence the findings never engaged", len(cards)))
	}
	return t1, reasons, warnings, cards, nil
}

// tier1Bundles loads every bucket's bundle. A missing entry errors: an
// unmeasured gate must never read as a passed one.
func tier1Bundles(record RunRecord, cache *Cache) (map[string]synthesis.EvidenceBundle, error) {
	out := map[string]synthesis.EvidenceBundle{}
	for _, b := range record.Buckets {
		var bundle synthesis.EvidenceBundle
		hit, err := cache.Get("bundle", b.BundleKey, &bundle)
		if err != nil {
			return nil, err
		}
		if !hit {
			return nil, fmt.Errorf("bucket %s: bundle missing from cache — re-run `insights eval outcome`", b.Bucket)
		}
		out[b.Bucket] = bundle
	}
	return out, nil
}

func tier1Samples(record RunRecord, cache *Cache, bundles map[string]synthesis.EvidenceBundle) ([]tier1Sample, error) {
	idx := globalEvidenceIndex(bundles)
	out := make([]tier1Sample, 0, len(record.SampleOutputs))
	for _, so := range record.SampleOutputs {
		var vo VerifiedOutput
		hit, err := cache.Get("verify", so.VerifiedKey, &vo)
		if err != nil {
			return nil, err
		}
		if !hit {
			return nil, fmt.Errorf("sample %d: verified output missing from cache — re-run `insights eval outcome`", so.SampleIndex)
		}
		out = append(out, newTier1Sample(so, vo, bundles, idx))
	}
	return out, nil
}

// newTier1Sample reads citations and quotes from the RAW output — the model's
// own claims, not the fields Go owns or the findings Go removed — falling back
// to the snapshot for cached outputs written before raw was retained.
func newTier1Sample(so SampleOutput, vo VerifiedOutput, bundles map[string]synthesis.EvidenceBundle, idx evidenceIndex) tier1Sample {
	s := tier1Sample{Index: so.SampleIndex, Fresh: so.Fresh,
		FindingCites: map[string]bool{},
		Empty:        len(vo.Snapshot.Findings) == 0 && len(vo.Snapshot.Dropped) == 0,
		Items:        BuildGlobalScoredItems(vo.Snapshot, bundles)}
	var prose []string
	var dropped []insights.DroppedJSON
	if len(vo.Raw.Findings) > 0 || len(vo.Raw.Dropped) > 0 {
		for _, f := range vo.Raw.Findings {
			markCited(s.FindingCites, f.EvidenceIDs)
			s.CheckableQuotes += min(len(f.Quotes), checkableQuotesPerFinding)
			prose = append(prose, f.Title, f.Statement, f.RankRationale, f.Asset.Content)
		}
		dropped = vo.Raw.Dropped
	} else {
		for _, f := range vo.Snapshot.Findings {
			markCited(s.FindingCites, f.EvidenceIDs)
			s.CheckableQuotes += min(len(f.Quotes), checkableQuotesPerFinding)
			prose = append(prose, f.Title, f.Statement, f.RankRationale, f.Asset.Content)
		}
		dropped = vo.Snapshot.Dropped
	}
	for _, d := range dropped {
		sessions, _ := idx.resolve(d.EvidenceIDs)
		e := droppedEntry{Summary: d.Summary, Reason: d.Reason,
			IDs: d.EvidenceIDs, Cites: map[string]bool{}, Sessions: sessions}
		markCited(e.Cites, d.EvidenceIDs)
		s.Dropped = append(s.Dropped, e)
		prose = append(prose, d.Summary, d.Reason)
	}
	// The quote-drop count is a soft correction Go recorded, so it is only ever
	// on the snapshot; the denominator above is the raw claim.
	for _, note := range vo.Snapshot.Meta.ValidationNotes {
		if m := quoteDropNote.FindStringSubmatch(note); m != nil {
			if count, err := strconv.Atoi(m[1]); err == nil {
				s.QuoteDrops += count
			}
		}
	}
	// Only the model's OWN prose is scanned, and with the eval scan's wider
	// pattern set: quotes and excerpts are verbatim copies of user/config
	// material that legitimately carries absolute paths, and asset.target is a
	// path Go normalizes to ~-relative before its own scan runs. Everything
	// here the verifier already blocks on the four shared patterns, so what
	// this adds is the two the pipeline scan lacks (/home/, .worktrees/).
	for _, p := range prose {
		s.ProseLeaks += len(privacyScan([]byte(p)))
	}
	return s
}

func markCited(set map[string]bool, ids []string) {
	for _, id := range ids {
		set[id] = true
	}
}

func citesAny(set map[string]bool, ids []string) bool {
	for _, id := range ids {
		if set[id] {
			return true
		}
	}
	return false
}

func suppressingDrops(dropped []droppedEntry, ids []string) []droppedEntry {
	var out []droppedEntry
	for _, d := range dropped {
		if citesAny(d.Cites, ids) {
			out = append(out, d)
		}
	}
	return out
}

// droppedCard turns one contested drop into a recognition card, deduped by
// adjudication key across samples and skipped once ruled on. floors names the
// recall floors the drop is holding up, if any.
func droppedCard(d droppedEntry, floors []string, adj map[string]Adjudication, seen map[string]bool) (PendingCard, bool) {
	k := d.key()
	h := k.Hash()
	if seen[h] {
		return PendingCard{}, false
	}
	seen[h] = true
	if _, ruled := adj[h]; ruled {
		return PendingCard{}, false
	}
	why := "no finding cites the evidence it names"
	if len(floors) > 0 {
		why = "the only thing holding up the " + strings.Join(sortedSet(floors), ", ")
	}
	return PendingCard{TargetID: tier1CardTarget, Trigger: droppedSuppression, Key: k,
		Adjudicable: true, ItemText: d.Summary, SessionIDs: d.Sessions,
		Note: fmt.Sprintf("dropped as %q — %s; accept if the drop is right, reject if it launders a recall miss",
			d.Reason, why)}, true
}

// recallProbes derives the floors from the bundles alone: every substantive
// signal, every recurring preference cluster (cross-repo — one practice is one
// cluster however many repos state it), and every repo carrying friction.
func recallProbes(bundles map[string]synthesis.EvidenceBundle) []recallProbe {
	var out []recallProbe
	for _, repo := range sortedKeysOfBundles(bundles) {
		b := bundles[repo]
		for _, g := range b.Signals {
			if g.Magnitude >= substantiveSignalMagnitude {
				id := repo + "/" + g.ID
				out = append(out, recallProbe{Kind: "opportunity", ID: id, IDs: []string{id}})
			}
		}
		var frictionIDs []string
		for _, f := range b.Friction {
			frictionIDs = append(frictionIDs, repo+"/"+f.ID)
		}
		if len(frictionIDs) > 0 {
			out = append(out, recallProbe{Kind: "friction", ID: repo, IDs: frictionIDs})
		}
	}
	for _, ids := range prefClusters(bundles) {
		out = append(out, recallProbe{Kind: "preference", ID: strings.Join(ids, ","), IDs: ids})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// prefClusters groups P* items stating the same practice: single-link
// components over token-multiset overlap of the rule text, the same mechanism
// (and threshold) the bundle's own directive clustering is pinned to. Only
// clusters at or above the recurrence floor are returned, each as its sorted
// namespaced id list.
func prefClusters(bundles map[string]synthesis.EvidenceBundle) [][]string {
	type prefItem struct {
		id     string
		tokens map[string]int
	}
	var items []prefItem
	for _, repo := range sortedKeysOfBundles(bundles) {
		for _, p := range bundles[repo].Prefs {
			items = append(items, prefItem{id: repo + "/" + p.ID, tokens: practiceTokens(p.Rule)})
		}
	}
	uf := make([]int, len(items))
	for i := range uf {
		uf[i] = i
	}
	var find func(int) int
	find = func(x int) int {
		for uf[x] != x {
			uf[x] = uf[uf[x]]
			x = uf[x]
		}
		return x
	}
	for i := range items {
		for j := i + 1; j < len(items); j++ {
			if multisetJaccard(items[i].tokens, items[j].tokens) >= prefClusterSimilarity {
				uf[find(i)] = find(j)
			}
		}
	}
	byRoot := map[int][]string{}
	for i, it := range items {
		r := find(i)
		byRoot[r] = append(byRoot[r], it.id)
	}
	var out [][]string
	for _, ids := range byRoot {
		if len(ids) < prefClusterFloor {
			continue
		}
		sort.Strings(ids)
		out = append(out, ids)
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// membershipChurn = 1 - the mean session-set Jaccard of a's findings matched
// into b's by statement similarity. A finding with no counterpart above the
// same-practice bar scores 0 — it is exactly the instability the gate watches
// for. Dropped entries are not findings and never participate.
func membershipChurn(a, b []ScoredItem) float64 {
	af, bf := shippedOnly(a), shippedOnly(b)
	if len(af) == 0 {
		return 0
	}
	bTokens := make([]map[string]int, len(bf))
	for i, ib := range bf {
		bTokens[i] = practiceTokens(ib.Text)
	}
	total := 0.0
	for _, ia := range af {
		ta := practiceTokens(ia.Text)
		best, bestSim := 0.0, churnMatchSimilarity
		for i, ib := range bf {
			if sim := multisetJaccard(ta, bTokens[i]); sim >= bestSim {
				best, bestSim = sessionJaccard(ia.SessionIDs, ib.SessionIDs), sim
			}
		}
		total += best
	}
	return 1 - total/float64(len(af))
}

// practiceTokens tokenizes a statement the way the facts tier does, over the
// matcher's own statement normalization, into a token multiset.
func practiceTokens(s string) map[string]int {
	out := map[string]int{}
	for _, t := range insights.ClauseTokens(normalizeStatement(s)) {
		out[t]++
	}
	return out
}

func multisetJaccard(a, b map[string]int) float64 {
	inter, union := 0, 0
	for t, n := range a {
		m := b[t]
		if m < n {
			inter += m
			union += n
		} else {
			inter += n
			union += m
		}
	}
	for t, m := range b {
		if _, ok := a[t]; !ok {
			union += m
		}
	}
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func sessionJaccard(a, b []string) float64 {
	sa, sb := stringSet(a), stringSet(b)
	inter, union := 0, len(sa)
	for x := range sb {
		if sa[x] {
			inter++
		} else {
			union++
		}
	}
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func sortedKeys(set map[string]bool) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sortedKeysOfBundles gives every bundle walk one deterministic repo order.
func sortedKeysOfBundles(bundles map[string]synthesis.EvidenceBundle) []string {
	return slices.Sorted(maps.Keys(bundles))
}
