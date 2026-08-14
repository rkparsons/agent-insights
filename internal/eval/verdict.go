package eval

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"time"
)

// VerdictInputs carries everything ComposeVerdict folds together. Results is
// mutated: flip and baseline_miss triggers attach to its verdicts.
type VerdictInputs struct {
	Record         RunRecord
	RecordName     string
	ScoredAt       time.Time
	RubricSetHash  string
	MatcherEnvHash string
	Results        []TargetResult
	Negatives      []NegativeViolation
	Probes         []ProbeResult
	Invalidated    []string
	Warnings       []string
	Adj            map[string]Adjudication
	Prior          []namedVerdict
	// Watermarks holds benchmark.json's per-target nuance_watermarks —
	// recalibrated (pass_at lowered) targets whose depth must stay visible.
	Watermarks map[string]int
}

func baselineTargetVerdict(v Verdict, id string) (TargetVerdict, bool) {
	for _, tv := range v.Targets {
		if tv.ID == id {
			return tv, true
		}
	}
	return TargetVerdict{}, false
}

func refOf(s *SampleScore) string {
	if s == nil {
		return ""
	}
	return s.ItemRef
}

// ComposeVerdict assembles the committed verdict: identity tuple, id-free
// provenance, Tier-1 gates, baseline comparison (flip triggers; fresh-baseline
// miss cards), Part-A/Part-B summaries, hard-fail composition, delta. Returns
// the pending cards its own triggers generate (flip / baseline_miss).
func ComposeVerdict(in VerdictInputs, cache *Cache) (Verdict, []PendingCard, error) {
	v := Verdict{
		ScoredAt:   in.ScoredAt,
		RecordName: filepath.Base(in.RecordName),
		Tuple: ComparisonTuple{
			Population:  in.Record.Population,
			Scope:       in.Record.Scope,
			PoolVersion: in.Record.PoolVersion,
			Models: map[string]string{"l1": in.Record.Models["l1"], "l2": in.Record.Models["l2"],
				"matcher": MatcherModel},
			EnvHash:       in.Record.EnvHash,
			RubricSetHash: in.RubricSetHash,
		},
		PartB:       map[string]string{},
		Probes:      in.Probes,
		Negatives:   in.Negatives,
		Invalidated: in.Invalidated,
		Warnings:    in.Warnings,
	}
	v.Provenance = map[string]string{
		"manifest_hash":          in.Record.ManifestHash,
		"benchmark_hash":         in.Record.BenchmarkHash,
		"config_snapshot_hash":   in.Record.ConfigSnapshotHash,
		"record_rubric_set_hash": in.Record.RubricSetHash,
		"claude_version":         in.Record.ClaudeVersion,
		"matcher_code_version":   MatcherCodeVersion(),
		"matcher_env_hash":       in.MatcherEnvHash,
	}
	for k, cv := range in.Record.CodeVersions {
		v.Provenance["code_version_"+k] = cv
	}
	for k, h := range in.Record.SkillHashes {
		v.Provenance["skill_hash_"+k] = h
	}
	for _, b := range in.Record.Buckets {
		v.Buckets = append(v.Buckets, VerdictBucket{Bucket: b.Bucket, Sessions: len(b.Population),
			GapFallbacks: len(b.GapFallbacks), BundleHash: b.BundleHash})
	}
	v.Samples = len(in.Record.SampleOutputs)
	for _, s := range in.Record.SampleOutputs {
		if s.Fresh {
			v.FreshSamples++
		}
	}

	t1, reasons, t1Warnings, t1Cards, err := ComputeTier1(in.Record, cache, in.Adj)
	if err != nil {
		return v, nil, err
	}
	v.Tier1 = t1
	v.Warnings = append(v.Warnings, t1Warnings...)

	baseline := FindBaseline(v.Tuple, in.Prior)
	extra := t1Cards
	for i := range in.Results {
		res := &in.Results[i]
		tv := &res.Verdict
		if baseline == nil {
			// fresh epochs have no flip trigger and hence no card channel:
			// every miss cards once (spec run-0 semantics)
			if (tv.Status == "must_pass" && !tv.Pass) || (tv.Status == "expected_partial" && !tv.MeetsExpectation) {
				tv.Triggers = append(tv.Triggers, Trigger{Type: "baseline_miss"})
				extra = append(extra, PendingCard{TargetID: tv.ID, Trigger: "baseline_miss",
					ItemText: textOf(decidingSample(res.Samples, tv.Granularity)), Granularity: tv.Granularity,
					Note: "fresh baseline (no comparable prior verdict): every miss is carded once"})
			}
			continue
		}
		bt, ok := baselineTargetVerdict(baseline.V, tv.ID)
		if !ok {
			continue
		}
		basePass, _ := EffectiveTargetOutcome(bt, in.Adj)
		if basePass == tv.Pass {
			continue
		}
		deciding := decidingSample(res.Samples, tv.Granularity)
		k := AdjKey{TargetID: tv.ID, RubricHash: res.Rubric.Hash, Trigger: "flip", IDSetHash: idSetHash(nil)}
		if tv.Pass && deciding != nil {
			k.Statement = normalizeStatement(deciding.ItemText)
			k.IDSetHash = idSetHash(deciding.ItemSessionIDs)
		}
		tr := Trigger{Type: "flip", KeyHash: k.Hash()}
		if a, ok := in.Adj[k.Hash()]; ok {
			tr.Adjudicated = a.Decision
		}
		tv.Triggers = append(tv.Triggers, tr)
		if tr.Adjudicated == "" {
			// Only fail→pass flips are adjudicable (acceptance lifts the
			// provisional-fail); a regression-direction flip has nothing to
			// resolve — the card is pure recognition.
			extra = append(extra, PendingCard{TargetID: tv.ID, Trigger: "flip", Key: k,
				Adjudicable: tv.Pass, Ref: refOf(deciding), ItemText: textOf(deciding),
				Quotes: quotesOf(deciding), Granularity: tv.Granularity,
				Note: fmt.Sprintf("flip vs %s: pass %v → %v", baseline.Name, basePass, tv.Pass)})
		}
		if tv.Pass && tr.Adjudicated != "accept" {
			// a fail→pass flip provisional-fails until confirmed (spec:
			// contested targets score provisional-fail except sample splits).
			// Never a hard fail — the granularity is passing; only a genuine
			// miss (granularity below pass_at) hard-fails a HIGH target.
			tv.Pass = false
			tv.ProvisionalFail = true
			tv.MeetsExpectation = false
		}
	}

	for _, res := range in.Results {
		tv := res.Verdict
		if wm, ok := in.Watermarks[tv.ID]; ok && len(res.Samples) > 0 && tv.NuancePassMedian < wm {
			v.Warnings = append(v.Warnings, fmt.Sprintf(
				"recalibrated target %s nuance-pass median %d below watermark %d — depth regression on a pass_at-lowered target",
				tv.ID, tv.NuancePassMedian, wm))
		}
		v.Targets = append(v.Targets, tv)
		switch tv.Status {
		case "must_pass":
			w := tierWeights[tv.Tier]
			v.PartA.WeightedTotal += w
			v.PartA.Scored++
			if tv.Pass {
				v.PartA.WeightedPassed += w
				v.PartA.Passed++
			}
			if tv.HardFail {
				v.PartA.HardFailTargets = append(v.PartA.HardFailTargets, tv.ID)
				reasons = append(reasons, fmt.Sprintf("must_pass HIGH target %s missed (granularity %s)", tv.ID, tv.Granularity))
			}
			if tv.ProvisionalFail {
				v.PartA.ProvisionalFailTargets = append(v.PartA.ProvisionalFailTargets, tv.ID)
				v.Warnings = append(v.Warnings, fmt.Sprintf("%s would pass (%s) but is provisional-fail pending its card", tv.ID, tv.Granularity))
			}
		case "expected_partial":
			if tv.HardFail {
				reasons = append(reasons, fmt.Sprintf("expected_partial target %s absent (presence regression)", tv.ID))
			} else if tv.Granularity == "full" {
				v.PartA.ExpectedPartialFull = append(v.PartA.ExpectedPartialFull, tv.ID)
			} else {
				v.PartA.ExpectedPartialMet = append(v.PartA.ExpectedPartialMet, tv.ID)
			}
		case "expected_fail", "needs_reconfirmation":
			v.PartB[tv.ID] = tv.Granularity
		}
	}
	if v.PartA.WeightedTotal > 0 {
		v.PartA.WeightedRecall = v.PartA.WeightedPassed / v.PartA.WeightedTotal
	}
	for _, n := range in.Negatives {
		v.Warnings = append(v.Warnings, fmt.Sprintf("negative rubric %s violated on samples %v (refs %v)", n.RubricID, n.SampleIndexes, n.ItemRefs))
	}
	for _, p := range in.Probes {
		if !p.Pass { // defense in depth — the orchestrator aborts on probe failure first
			reasons = append(reasons, fmt.Sprintf("matcher integrity probe %s failed (majority %s)", p.Class, p.Majority))
		}
	}
	d := ComputeDelta(v.Targets, baseline, in.Adj)
	v.Delta = &d
	sort.Strings(reasons)
	v.HardFailReasons = reasons
	v.HardFail = len(reasons) > 0
	return v, extra, nil
}

// PersistVerdict writes the verdict to the local cache always, then to the
// data repo's runs/ only when the privacy scan passes (spec: committed
// verdicts pass the scan before writing). A refused verdict keeps its cache
// copy for debugging and returns an error naming the pattern classes.
func PersistVerdict(dataDir, cacheDir string, v Verdict) (string, error) {
	name := v.ScoredAt.Format("2006-01-02T15-04-05Z") + ".json"
	cachePath := filepath.Join(cacheDir, "verdicts", name)
	if err := writeJSON(cachePath, v); err != nil {
		return "", err
	}
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	if hits := privacyScan(data); len(hits) > 0 {
		return "", fmt.Errorf("verdict failed privacy scan (%v) — NOT committed to runs/; local copy at %s", hits, cachePath)
	}
	runsPath := filepath.Join(dataDir, "runs", name)
	return runsPath, writeJSON(runsPath, v)
}
