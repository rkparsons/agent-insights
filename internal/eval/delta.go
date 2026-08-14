package eval

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ComparisonTuple is the spec's delta-comparability tuple: verdicts compare
// only against effective verdicts with an identical tuple; anything else is a
// fresh baseline. EnvHash is the PIPELINE env hash (the record's); matcher
// env drift is a warning, not a comparability break (models are in the tuple).
type ComparisonTuple struct {
	Population    string            `json:"population"`
	Scope         string            `json:"scope"`
	PoolVersion   string            `json:"pool_version"`
	Models        map[string]string `json:"models"` // l1, l2, matcher
	EnvHash       string            `json:"env_hash"`
	RubricSetHash string            `json:"rubric_set_hash"`
}

// VerdictBucket summarizes one bucket's evidence contribution without any id
// lists (populations stay in the run record; verdicts are structurally
// id-free). Sample counts are not per bucket under v2 — one synthesis covers
// every bucket — so they live on the Verdict.
type VerdictBucket struct {
	Bucket       string `json:"bucket"`
	Sessions     int    `json:"sessions"`
	GapFallbacks int    `json:"gap_fallbacks"`
	BundleHash   string `json:"bundle_hash"`
}

// Tier1Gates embeds the existing trust-property gates in every verdict —
// tuning toward the 28 targets must not regress them (spec).
type Tier1Gates struct {
	MaxRawFabricationRate   float64  `json:"max_raw_fabrication_rate"`
	HardErrorCount          int      `json:"hard_error_count"`
	OpportunityRecallMisses []string `json:"opportunity_recall_misses,omitempty"` // G ids — safe
	PrefRecallMisses        []string `json:"pref_recall_misses,omitempty"`
	DominantTypePresent     bool     `json:"dominant_type_present"`
	MembershipChurn         *float64 `json:"membership_churn"` // nil: <2 fresh samples (all-cached re-run)
	FreshSamplePairs        int      `json:"fresh_sample_pairs"`
	ReportPrivacyLeakCount  int      `json:"report_privacy_leak_count"`
}

type PartASummary struct {
	WeightedRecall         float64  `json:"weighted_recall"`
	WeightedPassed         float64  `json:"weighted_passed"`
	WeightedTotal          float64  `json:"weighted_total"`
	Passed                 int      `json:"passed"`
	Scored                 int      `json:"scored"`
	HardFailTargets        []string `json:"hard_fail_targets,omitempty"`
	ProvisionalFailTargets []string `json:"provisional_fail_targets,omitempty"`
	ExpectedPartialMet     []string `json:"expected_partial_met,omitempty"`
	ExpectedPartialFull    []string `json:"expected_partial_full,omitempty"`
}

type NegativeViolation struct {
	RubricID      string   `json:"rubric_id"`
	SampleIndexes []int    `json:"sample_indexes"`
	ItemRefs      []string `json:"item_refs,omitempty"`
}

type Flip struct {
	TargetID    string `json:"target_id"`
	From        string `json:"from"`
	To          string `json:"to"`
	PassChanged bool   `json:"pass_changed"`
}

type Delta struct {
	BaselineRun   string `json:"baseline_run"` // runs/ basename; "" on fresh baseline
	FreshBaseline bool   `json:"fresh_baseline"`
	Flips         []Flip `json:"flips,omitempty"`
}

// Verdict is the committed scoring result: a pure function of (run record,
// cache, rubrics, statuses, adjudications, prior committed verdicts).
type Verdict struct {
	ScoredAt        time.Time           `json:"scored_at"`
	Tuple           ComparisonTuple     `json:"tuple"`
	RecordName      string              `json:"record_name"` // basename only, never a path
	Provenance      map[string]string   `json:"provenance"`
	Buckets         []VerdictBucket     `json:"buckets"`
	Samples         int                 `json:"samples"`       // global L2 samples scored
	FreshSamples    int                 `json:"fresh_samples"` // of those, cache misses this run
	Targets         []TargetVerdict     `json:"targets"`
	Probes          []ProbeResult       `json:"probes"`
	PartA           PartASummary        `json:"part_a"`
	PartB           map[string]string   `json:"part_b"` // gap target id → granularity
	Negatives       []NegativeViolation `json:"negatives,omitempty"`
	Invalidated     []string            `json:"invalidated,omitempty"`
	Tier1           Tier1Gates          `json:"tier1"`
	HardFail        bool                `json:"hard_fail"`
	HardFailReasons []string            `json:"hard_fail_reasons,omitempty"`
	Warnings        []string            `json:"warnings,omitempty"`
	Delta           *Delta              `json:"delta,omitempty"`
	CardCount       int                 `json:"card_count"`
}

type namedVerdict struct {
	Name string
	V    Verdict
}

// LoadCommittedVerdicts reads every runs/*.json, sorted by name (timestamp
// names sort chronologically). An absent runs/ dir means no history.
func LoadCommittedVerdicts(dataDir string) ([]namedVerdict, error) {
	dir := filepath.Join(dataDir, "runs")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []namedVerdict
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var v Verdict
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("runs/%s: %w", e.Name(), err)
		}
		out = append(out, namedVerdict{Name: e.Name(), V: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func tupleEqual(a, b ComparisonTuple) bool {
	if a.Population != b.Population || a.Scope != b.Scope || a.PoolVersion != b.PoolVersion ||
		a.EnvHash != b.EnvHash || a.RubricSetHash != b.RubricSetHash || len(a.Models) != len(b.Models) {
		return false
	}
	for k, v := range a.Models {
		if b.Models[k] != v {
			return false
		}
	}
	return true
}

// FindBaseline picks the most recent committed verdict with an identical
// tuple; nil means fresh baseline (run-0 card semantics, never flips).
func FindBaseline(tuple ComparisonTuple, prior []namedVerdict) *namedVerdict {
	var best *namedVerdict
	for i := range prior {
		if !tupleEqual(prior[i].V.Tuple, tuple) {
			continue
		}
		if best == nil || prior[i].V.ScoredAt.After(best.V.ScoredAt) {
			best = &prior[i]
		}
	}
	return best
}

// provisionalTrigger names the trigger types whose acceptance lifts a
// committed provisional-fail retroactively. Membership triggers are absent by
// design: their acceptance changes which item counts, which only a re-score
// can recompute (spec: adjudications apply from the next run).
func provisionalTrigger(typ string) bool {
	return typ == "first_pass_no_anchor" || typ == "flip"
}

// EffectiveTargetOutcome applies later adjudications to a committed target:
// a provisional-fail whose provisional triggers were all accepted is
// effectively a pass. This is how "compared against effective verdicts" works
// without ever rewriting runs/.
func EffectiveTargetOutcome(tv TargetVerdict, adj map[string]Adjudication) (bool, string) {
	if !tv.ProvisionalFail {
		return tv.Pass, tv.Granularity
	}
	for _, tr := range tv.Triggers {
		if !provisionalTrigger(tr.Type) {
			continue
		}
		a, ok := adj[tr.KeyHash]
		if !ok || a.Decision != "accept" {
			return false, tv.Granularity
		}
	}
	return granularityRank[tv.Granularity] >= granularityRank[tv.PassAt], tv.Granularity
}

// everPassedTargets reports which targets have EVER effectively passed in any
// committed verdict (any tuple) — the "first-ever pass of a no-anchor target"
// trigger fires only when this is false for the target.
func everPassedTargets(prior []namedVerdict, adj map[string]Adjudication) map[string]bool {
	out := map[string]bool{}
	for _, nv := range prior {
		for _, tv := range nv.V.Targets {
			if pass, _ := EffectiveTargetOutcome(tv, adj); pass {
				out[tv.ID] = true
			}
		}
	}
	return out
}

// ComputeDelta lists per-target flips vs the baseline's EFFECTIVE outcomes.
// Targets absent from the baseline (new rubrics) are not flips.
func ComputeDelta(targets []TargetVerdict, base *namedVerdict, adj map[string]Adjudication) Delta {
	if base == nil {
		return Delta{FreshBaseline: true}
	}
	baseByID := map[string]TargetVerdict{}
	for _, tv := range base.V.Targets {
		baseByID[tv.ID] = tv
	}
	d := Delta{BaselineRun: base.Name}
	for _, cur := range targets {
		bt, ok := baseByID[cur.ID]
		if !ok {
			continue
		}
		basePass, baseGran := EffectiveTargetOutcome(bt, adj)
		if basePass == cur.Pass && baseGran == cur.Granularity {
			continue
		}
		d.Flips = append(d.Flips, Flip{TargetID: cur.ID, From: baseGran, To: cur.Granularity,
			PassChanged: basePass != cur.Pass})
	}
	return d
}
