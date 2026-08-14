package eval

import (
	"fmt"
	"strings"
	"testing"

	"github.com/rkparsons/agent-insights/internal/insights"
	"github.com/rkparsons/agent-insights/internal/synthesis"
)

// tier1Case builds a record + cache from per-repo bundles and per-sample
// verified outputs; the first freshCount samples are fresh (churn pairs).
func tier1Case(t *testing.T, bundles map[string]synthesis.EvidenceBundle, outs []VerifiedOutput, freshCount int) (RunRecord, *Cache) {
	t.Helper()
	cache := NewCache(t.TempDir())
	rec := RunRecord{Scope: "l2", Population: "scoring", Samples: len(outs)}
	for _, repo := range sortedKeysOfBundles(bundles) {
		key := "bk-" + repo
		if err := cache.Put("bundle", key, bundles[repo]); err != nil {
			t.Fatal(err)
		}
		rec.Buckets = append(rec.Buckets, BucketOutputs{Bucket: repo, BundleKey: key, BundleHash: "h-" + repo})
	}
	for i, vo := range outs {
		key := fmt.Sprintf("vk%d", i)
		if err := cache.Put("verify", key, vo); err != nil {
			t.Fatal(err)
		}
		rec.SampleOutputs = append(rec.SampleOutputs, SampleOutput{SampleIndex: i,
			Fresh: i < freshCount, VerifiedKey: key})
	}
	return rec, cache
}

// probeBundle carries every kind the recall probes read: a recurring
// preference cluster (3 items, one practice), a substantive G signal, and
// friction items.
//
// G2 is deliberately a shape BuildBundle cannot emit — Magnitude always equals
// len(MemberSessions) and only signals at or above the bundle's own floor are
// emitted, so nothing real ever trips the eval's magnitude filter. It pins that
// filter as a defensive restatement: a bundle-side floor change must not
// silently widen what this gate calls substantive.
func probeBundle(repo string) synthesis.EvidenceBundle {
	return synthesis.EvidenceBundle{Repo: repo,
		Friction: []synthesis.FrictionItem{
			{ID: "F1", OneLine: "re-ran the build by hand", SessionID: "s1"},
			{ID: "F2", OneLine: "lost the scratch dir", SessionID: "s2"},
		},
		Prefs: []synthesis.PrefItem{
			{ID: "P1", Rule: "never add comments that restate the code", SessionID: "s1"},
			{ID: "P2", Rule: "never add comments which restate the code", SessionID: "s2"},
			{ID: "P3", Rule: "never add comments that restate what the code does", SessionID: "s3"},
			{ID: "P4", Rule: "prefer tabs over spaces in makefiles", SessionID: "s1"},
		},
		Signals: []synthesis.OppSignal{
			{ID: "G1", Kind: "high_read", Magnitude: 4, MemberSessions: []string{"s1", "s2", "s3", "s4"}},
			{ID: "G2", Kind: "unskilled_toil", Magnitude: 1, MemberSessions: []string{"s1"}},
		},
	}
}

func findingSnapshot(evidence ...string) VerifiedOutput {
	return VerifiedOutput{
		Snapshot: insights.GlobalSynthesisJSON{SchemaVersion: 2,
			Findings: []insights.FindingJSON{{Rank: 1, Title: "T", Statement: "state the goal first",
				EvidenceIDs: evidence}}},
		Raw: insights.RawGlobalSynthesis{SchemaVersion: 2,
			Findings: []insights.RawFinding{{Rank: 1, Title: "T", Statement: "state the goal first",
				EvidenceIDs: evidence}}},
	}
}

// withDropped attaches a dropped entry citing the given evidence.
func withDropped(vo VerifiedOutput, summary, reason string, evidence ...string) VerifiedOutput {
	d := insights.DroppedJSON{Summary: summary, Reason: reason, EvidenceIDs: evidence}
	vo.Snapshot.Dropped = append(vo.Snapshot.Dropped, d)
	vo.Raw.Dropped = append(vo.Raw.Dropped, d)
	return vo
}

func hasWarning(warnings []string, sub string) bool {
	for _, w := range warnings {
		if strings.Contains(w, sub) {
			return true
		}
	}
	return false
}

// An uncited bundle: the recurring preference cluster, the substantive signal,
// and the repo's friction all go unmentioned by the one finding, which cites a
// success item instead. Every recall floor must report a miss — and the
// one-off preference (P4) and the sub-threshold signal (G2) must NOT.
func TestComputeTier1RecallMissesOnUncitedEvidence(t *testing.T) {
	b := probeBundle("alpha")
	b.Success = []synthesis.SuccessItem{{ID: "S1", Summary: "shipped", SessionID: "s1"}}
	rec, cache := tier1Case(t, map[string]synthesis.EvidenceBundle{"alpha": b},
		[]VerifiedOutput{findingSnapshot("alpha/S1")}, 0)

	t1, reasons, warnings, cards, err := ComputeTier1(rec, cache, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(reasons) != 0 {
		t.Fatalf("recall misses warn, never hard-fail: %v", reasons)
	}
	if len(t1.OpportunityRecallMisses) != 1 || t1.OpportunityRecallMisses[0] != "alpha/G1" {
		t.Fatalf("opportunity misses = %v, want the substantive signal only (G2 is under the floor)", t1.OpportunityRecallMisses)
	}
	if len(t1.PrefRecallMisses) != 1 || !strings.Contains(t1.PrefRecallMisses[0], "alpha/P1") ||
		strings.Contains(t1.PrefRecallMisses[0], "alpha/P4") {
		t.Fatalf("pref misses = %v, want the recurring cluster (P1-P3) and not the one-off P4", t1.PrefRecallMisses)
	}
	if len(t1.FrictionRecallMisses) != 1 || t1.FrictionRecallMisses[0] != "alpha" {
		t.Fatalf("friction misses = %v, want the repo whose F* nothing cites", t1.FrictionRecallMisses)
	}
	if t1.DroppedSuppressions != 0 || len(cards) != 0 {
		t.Fatalf("nothing was dropped: suppressions=%d cards=%d", t1.DroppedSuppressions, len(cards))
	}
	for _, want := range []string{"opportunity recall miss", "pref recall miss", "friction recall miss"} {
		if !hasWarning(warnings, want) {
			t.Fatalf("warnings must name every miss (%s): %v", want, warnings)
		}
	}
}

// A finding of ANY asset type citing the evidence clears the floor — the v1
// probe's "prefs present but zero claude_md_rule" inversion must not survive
// the asset ladder.
func TestComputeTier1FindingCitationClearsEveryFloor(t *testing.T) {
	rec, cache := tier1Case(t, map[string]synthesis.EvidenceBundle{"alpha": probeBundle("alpha")},
		[]VerifiedOutput{findingSnapshot("alpha/P1", "alpha/G1", "alpha/F2")}, 0)

	t1, _, warnings, cards, err := ComputeTier1(rec, cache, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(t1.OpportunityRecallMisses)+len(t1.PrefRecallMisses)+len(t1.FrictionRecallMisses) != 0 {
		t.Fatalf("cited evidence is never a miss: %+v", t1)
	}
	if len(cards) != 0 || t1.DroppedSuppressions != 0 {
		t.Fatalf("a finding citation needs no human gate: %d cards", len(cards))
	}
	if hasWarning(warnings, "recall miss") {
		t.Fatalf("warnings: %v", warnings)
	}
}

// A dropped citation suppresses the miss (legitimate drops must not flood the
// gate with false misses) — and in exchange the drop is carded for a human
// ruling, so a miss cannot be laundered through a junk reason.
func TestComputeTier1DroppedCitationSuppressesAndCards(t *testing.T) {
	vo := withDropped(findingSnapshot("alpha/S1"), "comment-style nit", "one session only",
		"alpha/P1", "alpha/G1", "alpha/F1")
	rec, cache := tier1Case(t, map[string]synthesis.EvidenceBundle{"alpha": probeBundle("alpha")},
		[]VerifiedOutput{vo}, 0)

	t1, reasons, warnings, cards, err := ComputeTier1(rec, cache, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(reasons) != 0 {
		t.Fatalf("a suppressed miss never hard-fails: %v", reasons)
	}
	if len(t1.OpportunityRecallMisses)+len(t1.PrefRecallMisses)+len(t1.FrictionRecallMisses) != 0 {
		t.Fatalf("a dropped citation suppresses all three recall probes: %+v", t1)
	}
	if t1.DroppedSuppressions != 3 {
		t.Fatalf("suppressions = %d, want one per suppressed probe", t1.DroppedSuppressions)
	}
	if len(cards) != 1 { // one dropped entry, however many probes it suppressed
		t.Fatalf("cards = %+v, want one recognition card for the dropped entry", cards)
	}
	c := cards[0]
	if !c.Adjudicable || c.TargetID != tier1CardTarget || c.Trigger != droppedSuppression {
		t.Fatalf("the drop must reach the human gate as an adjudicable card: %+v", c)
	}
	if !strings.Contains(c.ItemText, "comment-style nit") || !strings.Contains(c.Note, "one session only") {
		t.Fatalf("the card must show the drop and its reason: %+v", c)
	}
	if !hasWarning(warnings, "suppressed by a dropped citation") {
		t.Fatalf("the verdict must say the floors were suppressed: %v", warnings)
	}

	// an accepted ruling retires the card (the drop was right)
	adj := map[string]Adjudication{c.Key.Hash(): {Key: c.Key, KeyHash: c.Key.Hash(), Decision: "accept"}}
	_, _, _, cards2, err := ComputeTier1(rec, cache, adj)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards2) != 0 {
		t.Fatalf("an adjudicated drop must not re-card: %+v", cards2)
	}
}

// The carding bargain is not limited to suppressed floors: a drop whose
// evidence NO finding engages faces the human gate even when every floor is
// clear, because a junk reason over evidence nobody acted on is exactly the
// laundering the gate exists to catch. A drop the findings also cite is not
// laundering anything and stays out of the set.
func TestComputeTier1CardsDropsNoFindingEngages(t *testing.T) {
	b := probeBundle("alpha")
	b.Success = []synthesis.SuccessItem{
		{ID: "S1", Summary: "shipped", SessionID: "s1"},
		{ID: "S2", Summary: "shipped again", SessionID: "s2"},
	}
	bundles := map[string]synthesis.EvidenceBundle{"alpha": b}

	// every floor cleared, but the drop names evidence no finding touches
	vo := withDropped(findingSnapshot("alpha/P1", "alpha/G1", "alpha/F1"),
		"a stray success note", "not actionable", "alpha/S2")
	rec, cache := tier1Case(t, bundles, []VerifiedOutput{vo}, 0)
	t1, _, _, cards, err := ComputeTier1(rec, cache, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(t1.OpportunityRecallMisses)+len(t1.PrefRecallMisses)+len(t1.FrictionRecallMisses) != 0 ||
		t1.DroppedSuppressions != 0 {
		t.Fatalf("every floor is cleared by the finding: %+v", t1)
	}
	if len(cards) != 1 || cards[0].ItemText != "a stray success note" {
		t.Fatalf("an unengaged drop must reach the human gate: %+v", cards)
	}
	if !strings.Contains(cards[0].Note, "no finding") {
		t.Fatalf("the card must say why it is contested: %q", cards[0].Note)
	}

	// same evidence in the finding AND the drop: nothing to rule on
	engaged := withDropped(findingSnapshot("alpha/P1", "alpha/G1", "alpha/F1"),
		"the same friction, partly", "covered by the finding above", "alpha/F1")
	engagedRec, engagedCache := tier1Case(t, bundles, []VerifiedOutput{engaged}, 0)
	_, _, _, cards2, err := ComputeTier1(engagedRec, engagedCache, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards2) != 0 {
		t.Fatalf("a drop the findings engage needs no ruling: %+v", cards2)
	}
}

// Churn is the run-to-run session-set Jaccard of findings matched by statement
// similarity, over FRESH sample pairs only; an empty synthesis fails closed.
func TestComputeTier1ChurnAndEmptyFailClosed(t *testing.T) {
	b := synthesis.EvidenceBundle{Repo: "alpha",
		Friction: []synthesis.FrictionItem{
			{ID: "F1", OneLine: "a", SessionID: "s1"},
			{ID: "F2", OneLine: "b", SessionID: "s2"},
			{ID: "F3", OneLine: "c", SessionID: "s3"},
		}}
	// same practice, different sessions between the two fresh samples: the
	// statement match holds, the session sets disagree → churn > 0
	first := findingSnapshot("alpha/F1", "alpha/F2")
	second := findingSnapshot("alpha/F2", "alpha/F3")
	rec, cache := tier1Case(t, map[string]synthesis.EvidenceBundle{"alpha": b},
		[]VerifiedOutput{first, second}, 2)

	t1, reasons, _, _, err := ComputeTier1(rec, cache, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(reasons) != 0 {
		t.Fatalf("reasons: %v", reasons)
	}
	if t1.FreshSamplePairs != 1 || t1.MembershipChurn == nil {
		t.Fatalf("one fresh pair must produce a churn figure: pairs=%d churn=%v", t1.FreshSamplePairs, t1.MembershipChurn)
	}
	if got := *t1.MembershipChurn; got < 0.66 || got > 0.67 {
		t.Fatalf("churn = %v, want 1 - jaccard({s1,s2},{s2,s3}) = 2/3", got)
	}

	// no fresh pair → no churn figure at all (an all-cached re-run)
	cachedRec, cachedCache := tier1Case(t, map[string]synthesis.EvidenceBundle{"alpha": b},
		[]VerifiedOutput{first, second}, 0)
	t1c, _, _, _, err := ComputeTier1(cachedRec, cachedCache, nil)
	if err != nil {
		t.Fatal(err)
	}
	if t1c.MembershipChurn != nil || t1c.FreshSamplePairs != 0 {
		t.Fatalf("an all-cached re-run has no fresh pairs: %+v", t1c)
	}

	// a sample that produced nothing at all fails closed
	emptyRec, emptyCache := tier1Case(t, map[string]synthesis.EvidenceBundle{"alpha": b},
		[]VerifiedOutput{first, {Snapshot: insights.GlobalSynthesisJSON{SchemaVersion: 2}}}, 2)
	_, emptyReasons, _, _, err := ComputeTier1(emptyRec, emptyCache, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(emptyReasons) != 1 || !strings.Contains(emptyReasons[0], "empty synthesis output") {
		t.Fatalf("an empty synthesis must hard-fail: %v", emptyReasons)
	}
}

// The fabrication signal is the raw quote-drop rate: quotes the model cited
// that the verifier could not find verbatim, over the quotes it actually
// CHECKED — the cap-breaching tail is trimmed before the pool check, so it was
// never checkable and must not dilute the denominator.
func TestComputeTier1QuoteDropRateHardFail(t *testing.T) {
	b := synthesis.EvidenceBundle{Repo: "alpha",
		Friction: []synthesis.FrictionItem{{ID: "F1", OneLine: "a", SessionID: "s1"}}}
	vo := findingSnapshot("alpha/F1")
	vo.Raw.Findings[0].Quotes = []string{"q1", "q2", "q3", "q4"} // one past the cap
	vo.Snapshot.Findings[0].Quotes = []string{"q1", "q2"}
	vo.Snapshot.Meta.ValidationNotes = []string{
		`finding "T": already_adopted downgraded to unknown (no excerpt)`,
		`finding "T": trimmed to 3 quotes`,
		`finding "T": dropped 1 quote(s) not verbatim in the cited evidence`,
	}
	rec, cache := tier1Case(t, map[string]synthesis.EvidenceBundle{"alpha": b}, []VerifiedOutput{vo}, 0)

	t1, reasons, _, _, err := ComputeTier1(rec, cache, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := t1.MaxRawFabricationRate; got < 0.33 || got > 0.34 {
		t.Fatalf("drop rate = %v, want 1 of the 3 CHECKED quotes (the 4th was trimmed unchecked)", got)
	}
	if len(reasons) != 1 || !strings.Contains(reasons[0], "fabrication rate") {
		t.Fatalf("a rate over the gate must hard-fail: %v", reasons)
	}

	clean := findingSnapshot("alpha/F1")
	clean.Raw.Findings[0].Quotes = []string{"q1", "q2", "q3"}
	clean.Snapshot.Findings[0].Quotes = clean.Raw.Findings[0].Quotes
	cleanRec, cleanCache := tier1Case(t, map[string]synthesis.EvidenceBundle{"alpha": b}, []VerifiedOutput{clean}, 0)
	t1c, cleanReasons, _, _, err := ComputeTier1(cleanRec, cleanCache, nil)
	if err != nil {
		t.Fatal(err)
	}
	if t1c.MaxRawFabricationRate != 0 || len(cleanReasons) != 0 {
		t.Fatalf("verbatim quotes must score zero: %v %v", t1c.MaxRawFabricationRate, cleanReasons)
	}
}

// A home path in the model's own prose is a leak the eval gate keeps naming —
// measured on the RAW output, and on prose only: quotes and excerpts are
// verbatim copies of material that legitimately carries paths.
func TestComputeTier1PrivacyLeakInRawProse(t *testing.T) {
	b := synthesis.EvidenceBundle{Repo: "alpha",
		Friction: []synthesis.FrictionItem{{ID: "F1", OneLine: "a", SessionID: "s1"}}}
	vo := findingSnapshot("alpha/F1")
	vo.Raw.Findings[0].Statement = "always run the build from /Users/dev/Developer/alpha"
	rec, cache := tier1Case(t, map[string]synthesis.EvidenceBundle{"alpha": b}, []VerifiedOutput{vo}, 0)

	t1, reasons, _, _, err := ComputeTier1(rec, cache, nil)
	if err != nil {
		t.Fatal(err)
	}
	if t1.ReportPrivacyLeakCount == 0 {
		t.Fatal("a home path in model prose must be counted")
	}
	if len(reasons) != 1 || !strings.Contains(reasons[0], "privacy leak") {
		t.Fatalf("a leak must hard-fail: %v", reasons)
	}

	quoted := findingSnapshot("alpha/F1")
	quoted.Raw.Findings[0].Quotes = []string{"cd /Users/dev/Developer/alpha && make"}
	// asset.target is a path Go normalizes to ~-relative before its own scan
	// runs — an absolute one here is the normal case, not a leak.
	quoted.Raw.Findings[0].Asset = insights.AssetJSON{Type: "repo_doc",
		Target: "/Users/dev/Developer/alpha/docs/build.md", Content: "run make"}
	quoted.Raw.Findings[0].AlreadyAdopted = insights.AdoptedJSON{Verdict: "yes",
		SourcePath: "~/CLAUDE.md", Excerpt: "run from /Users/dev/Developer/alpha"}
	qRec, qCache := tier1Case(t, map[string]synthesis.EvidenceBundle{"alpha": b}, []VerifiedOutput{quoted}, 0)
	t1q, qReasons, _, _, err := ComputeTier1(qRec, qCache, nil)
	if err != nil {
		t.Fatal(err)
	}
	if t1q.ReportPrivacyLeakCount != 0 || len(qReasons) != 0 {
		t.Fatalf("verbatim quotes/excerpts are not model prose: %d %v", t1q.ReportPrivacyLeakCount, qReasons)
	}
}

// A preference cluster spans repos: one practice stated in two repos is still
// one recurring cluster, and citing any member clears it.
func TestComputeTier1PrefClustersAcrossRepos(t *testing.T) {
	alpha := synthesis.EvidenceBundle{Repo: "alpha", Prefs: []synthesis.PrefItem{
		{ID: "P1", Rule: "never add comments that restate the code", SessionID: "s1"},
		{ID: "P2", Rule: "never add comments which restate the code", SessionID: "s2"},
	}}
	beta := synthesis.EvidenceBundle{Repo: "beta", Prefs: []synthesis.PrefItem{
		{ID: "P1", Rule: "never add comments that restate what the code does", SessionID: "s3"},
	}}
	bundles := map[string]synthesis.EvidenceBundle{"alpha": alpha, "beta": beta}
	rec, cache := tier1Case(t, bundles, []VerifiedOutput{findingSnapshot()}, 0)

	t1, _, _, _, err := ComputeTier1(rec, cache, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(t1.PrefRecallMisses) != 1 {
		t.Fatalf("one cross-repo practice is one cluster: %v", t1.PrefRecallMisses)
	}
	if !strings.Contains(t1.PrefRecallMisses[0], "beta/P1") {
		t.Fatalf("the cluster must name every member: %v", t1.PrefRecallMisses)
	}

	citedRec, citedCache := tier1Case(t, bundles, []VerifiedOutput{findingSnapshot("beta/P1")}, 0)
	t1c, _, _, _, err := ComputeTier1(citedRec, citedCache, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(t1c.PrefRecallMisses) != 0 {
		t.Fatalf("citing one member clears the cluster: %v", t1c.PrefRecallMisses)
	}
}

// The probes read the RAW citations: what the model claimed, not what Go left
// standing after its corrections.
func TestComputeTier1ReadsRawCitations(t *testing.T) {
	b := probeBundle("alpha")
	vo := findingSnapshot("alpha/G1")
	// Go removed the finding (e.g. a stale escalation); the raw claim stands.
	vo.Snapshot.Findings[0].EvidenceIDs = nil
	rec, cache := tier1Case(t, map[string]synthesis.EvidenceBundle{"alpha": b}, []VerifiedOutput{vo}, 0)

	t1, _, _, _, err := ComputeTier1(rec, cache, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(t1.OpportunityRecallMisses) != 0 {
		t.Fatalf("the raw citation is what the probe measures: %v", t1.OpportunityRecallMisses)
	}
}

func TestComputeTier1MissingCacheEntriesFailClosed(t *testing.T) {
	rec, cache := tier1Case(t, map[string]synthesis.EvidenceBundle{"alpha": probeBundle("alpha")},
		[]VerifiedOutput{findingSnapshot("alpha/F1")}, 0)
	rec.Buckets[0].BundleKey = "missing"
	if _, _, _, _, err := ComputeTier1(rec, cache, nil); err == nil {
		t.Fatal("a missing bundle must error, never measure nothing and pass")
	}

	rec2, cache2 := tier1Case(t, map[string]synthesis.EvidenceBundle{"alpha": probeBundle("alpha")},
		[]VerifiedOutput{findingSnapshot("alpha/F1")}, 0)
	rec2.SampleOutputs[0].VerifiedKey = "missing"
	if _, _, _, _, err := ComputeTier1(rec2, cache2, nil); err == nil {
		t.Fatal("a missing verified output must error")
	}
}

// findingsSnapshot builds a sample carrying several shipped findings, raw and
// snapshot alike, each citing its own evidence.
func findingsSnapshot(findings ...insights.FindingJSON) VerifiedOutput {
	vo := VerifiedOutput{
		Snapshot: insights.GlobalSynthesisJSON{SchemaVersion: 2, Findings: findings},
		Raw:      insights.RawGlobalSynthesis{SchemaVersion: 2},
	}
	for _, f := range findings {
		vo.Raw.Findings = append(vo.Raw.Findings, insights.RawFinding{Rank: f.Rank, Title: f.Title,
			Statement: f.Statement, EvidenceIDs: f.EvidenceIDs})
	}
	return vo
}

// The spec's two deterministic auto-signals (§Eval adaptation): the total
// meta.validation_notes count and the adopted-verdict downgrades. Both measure
// RAW model output before Go's correction, which is what makes them
// non-tautological — and calibration signals, never hard gates.
func TestComputeTier1ValidationNoteSignals(t *testing.T) {
	b := synthesis.EvidenceBundle{Repo: "alpha",
		Friction: []synthesis.FrictionItem{{ID: "F1", OneLine: "a", SessionID: "s1"}}}
	first := findingSnapshot("alpha/F1")
	first.Snapshot.Meta.ValidationNotes = []string{
		`finding "T": already_adopted downgraded to unknown (cannot read ~/.claude/CLAUDE.md)`,
		`finding "U": already_adopted downgraded to unknown (excerpt is not verbatim in ~/.claude/CLAUDE.md)`,
		`finding "T": trimmed to 3 quotes`,
	}
	second := findingSnapshot("alpha/F1")
	second.Snapshot.Meta.ValidationNotes = []string{
		`finding "T": escalation removed, ~/.claude/CLAUDE.md changed after every cited session`,
	}
	rec, cache := tier1Case(t, map[string]synthesis.EvidenceBundle{"alpha": b},
		[]VerifiedOutput{first, second}, 0)

	t1, reasons, warnings, _, err := ComputeTier1(rec, cache, nil)
	if err != nil {
		t.Fatal(err)
	}
	if t1.ValidationNoteCount != 4 {
		t.Fatalf("validation notes = %d, want every note across every sample", t1.ValidationNoteCount)
	}
	if t1.AdoptedDowngradeCount != 2 {
		t.Fatalf("adopted downgrades = %d, want the two downgrade notes only", t1.AdoptedDowngradeCount)
	}
	if len(reasons) != 0 {
		t.Fatalf("calibration signals warn, never hard-fail: %v", reasons)
	}
	if !hasWarning(warnings, "validation note") || !hasWarning(warnings, "already_adopted") {
		t.Fatalf("both signals must reach the verdict as warnings: %v", warnings)
	}

	clean := findingSnapshot("alpha/F1")
	cleanRec, cleanCache := tier1Case(t, map[string]synthesis.EvidenceBundle{"alpha": b},
		[]VerifiedOutput{clean}, 0)
	t1c, _, cleanWarnings, _, err := ComputeTier1(cleanRec, cleanCache, nil)
	if err != nil {
		t.Fatal(err)
	}
	if t1c.ValidationNoteCount != 0 || t1c.AdoptedDowngradeCount != 0 {
		t.Fatalf("an uncorrected run scores zero on both: %+v", t1c)
	}
	if hasWarning(cleanWarnings, "validation note") || hasWarning(cleanWarnings, "already_adopted") {
		t.Fatalf("no correction, no warning: %v", cleanWarnings)
	}
}

// Cross-repo merge quality is the spec's adjudicated axis: one practice split
// across two findings must reach the human recognition surface. It is a card,
// never a gate and never a recall miss — and two findings stating genuinely
// different practices produce none.
func TestComputeTier1CardsSplitPractice(t *testing.T) {
	b := synthesis.EvidenceBundle{Repo: "alpha",
		Friction: []synthesis.FrictionItem{
			{ID: "F1", OneLine: "a", SessionID: "s1"},
			{ID: "F2", OneLine: "b", SessionID: "s2"},
		}}
	bundles := map[string]synthesis.EvidenceBundle{"alpha": b}
	split := findingsSnapshot(
		insights.FindingJSON{Rank: 1, Title: "Never restate the code",
			Statement: "never add comments that restate the code", EvidenceIDs: []string{"alpha/F1"}},
		insights.FindingJSON{Rank: 2, Title: "Never restate the code",
			Statement: "never add comments which restate what the code does", EvidenceIDs: []string{"alpha/F2"}},
	)
	rec, cache := tier1Case(t, bundles, []VerifiedOutput{split}, 0)

	t1, reasons, warnings, cards, err := ComputeTier1(rec, cache, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(reasons) != 0 {
		t.Fatalf("a split practice is adjudicated, never gated: %v", reasons)
	}
	if len(t1.OpportunityRecallMisses)+len(t1.PrefRecallMisses)+len(t1.FrictionRecallMisses) != 0 {
		t.Fatalf("a split practice is not a recall miss: %+v", t1)
	}
	if len(cards) != 1 {
		t.Fatalf("cards = %+v, want one merge-quality card for the pair", cards)
	}
	c := cards[0]
	if !c.Adjudicable || c.TargetID != tier1CardTarget || c.Trigger != mergeQuality {
		t.Fatalf("the pair must ride the tier-1 pseudo-target as an adjudicable card: %+v", c)
	}
	if !strings.Contains(c.ItemText, "restate the code") ||
		!strings.Contains(c.ItemText, "restate what the code does") {
		t.Fatalf("the card must show both statements: %q", c.ItemText)
	}
	if !hasWarning(warnings, "merge quality") {
		t.Fatalf("the verdict must name the carded pair: %v", warnings)
	}

	// the same pair in a second sample is one ruling, not two
	repeatRec, repeatCache := tier1Case(t, bundles, []VerifiedOutput{split, split}, 0)
	_, _, _, repeatCards, err := ComputeTier1(repeatRec, repeatCache, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(repeatCards) != 1 {
		t.Fatalf("a repeated pair cards once: %+v", repeatCards)
	}

	// an accepted ruling (they really are separable) retires the card
	adj := map[string]Adjudication{c.Key.Hash(): {Key: c.Key, KeyHash: c.Key.Hash(), Decision: "accept"}}
	_, _, _, ruledCards, err := ComputeTier1(rec, cache, adj)
	if err != nil {
		t.Fatal(err)
	}
	if len(ruledCards) != 0 {
		t.Fatalf("an adjudicated pair must not re-card: %+v", ruledCards)
	}

	// two findings stating different practices are not a split
	distinct := findingsSnapshot(
		insights.FindingJSON{Rank: 1, Title: "Never restate the code",
			Statement: "never add comments that restate the code", EvidenceIDs: []string{"alpha/F1"}},
		insights.FindingJSON{Rank: 2, Title: "Indent makefiles with tabs",
			Statement: "prefer tabs over spaces in makefiles", EvidenceIDs: []string{"alpha/F2"}},
	)
	distinctRec, distinctCache := tier1Case(t, bundles, []VerifiedOutput{distinct}, 0)
	_, _, distinctWarnings, distinctCards, err := ComputeTier1(distinctRec, distinctCache, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(distinctCards) != 0 {
		t.Fatalf("separate practices are not a split: %+v", distinctCards)
	}
	if hasWarning(distinctWarnings, "merge quality") {
		t.Fatalf("warnings: %v", distinctWarnings)
	}
}
