package synthesis

type EvalResult struct {
	SchemaOK                bool
	RawFabricationRate      float64
	HardErrors              []string
	OpportunityRecallMisses []string // high-mag G with no referencing theme
	PrefRecallMisses        []string // coarse: prefs present but zero claude_md_rule (finer clustering is Tier-2)
	DominantTypePresent     bool     // soft floor: friction exists but no friction theme
	MembershipChurn         float64  // 1 - mean best session-set Jaccard across two runs
	PrivacyLeaks            []string
}

// EvaluateRun scores run `a` against evidence bundle and validation report; `b` is the
// comparison run (e.g. a prior/re-run) used for membership-churn stability.
func EvaluateRun(a, b RepoSynthesis, report ValidationReport, bundle EvidenceBundle) EvalResult {
	res := EvalResult{
		RawFabricationRate: report.RawQuoteDropRate,
		HardErrors:         report.HardErrors,
	}
	referenced := map[string]bool{}
	for _, t := range a.Themes {
		for _, g := range t.SignalRefs {
			referenced[g] = true
		}
	}
	for _, g := range bundle.Signals {
		if g.Magnitude >= signalFloor && !referenced[g.ID] {
			res.OpportunityRecallMisses = append(res.OpportunityRecallMisses, g.ID)
		}
	}
	if len(bundle.Prefs) >= signalFloor {
		hasRule := false
		for _, r := range a.Recommendations {
			if r.Type == "claude_md_rule" {
				hasRule = true
			}
		}
		if !hasRule {
			res.PrefRecallMisses = append(res.PrefRecallMisses, "standing-prefs present but no claude_md_rule surfaced")
		}
	}
	hasFrictionTheme := false
	for _, t := range a.Themes {
		if t.Kind == "friction" {
			hasFrictionTheme = true
		}
	}
	res.DominantTypePresent = len(bundle.Friction) == 0 || hasFrictionTheme

	res.MembershipChurn = membershipChurn(a, b)
	res.PrivacyLeaks = scanReport(Render(a))
	res.SchemaOK = len(report.HardErrors) == 0
	return res
}

// membershipChurn = 1 - mean best session-set Jaccard of a's themes matched into b's.
func membershipChurn(a, b RepoSynthesis) float64 {
	if len(a.Themes) == 0 {
		return 0
	}
	total := 0.0
	for _, ta := range a.Themes {
		best := 0.0
		for _, tb := range b.Themes {
			if j := jaccard(ta.SessionIDs, tb.SessionIDs); j > best {
				best = j
			}
		}
		total += best
	}
	return 1 - total/float64(len(a.Themes))
}

// jaccard treats a and b as sets (session ids repeat within a theme when a session
// contributes multiple evidence items) — dedup both sides before comparing.
func jaccard(a, b []string) float64 {
	sa := map[string]bool{}
	for _, x := range a {
		sa[x] = true
	}
	sb := map[string]bool{}
	for _, x := range b {
		sb[x] = true
	}
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
