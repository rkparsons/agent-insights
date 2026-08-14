package eval

// Shared fixtures for the env-gated dev probes (env-hash probe, cache-hole
// mapper). Pinned to the current effective baseline — update both constants
// when a new record becomes the baseline (values come from the verdict's
// provenance, never from ledger prose).

import (
	"testing"

	"github.com/rkparsons/agent-insights/internal/synthesis"
)

const (
	// devCurRecord is the Phase-3 epoch record behind the effective baseline
	// runs/2026-07-10T18-44-11Z.json. It is v1-shaped: the mapper below reads
	// the v2 record shape, so it reports nothing until a v2 record is the
	// baseline (the v1 0.62 baseline is historical — spec §Eval adaptation).
	devCurRecord = "2026-07-10T06-13-21Z-626084000.json"
	// devCurEnvHash is that verdict's provenance.matcher_env_hash
	// (claude 2.1.206, snapshot 7fe1f6cd).
	devCurEnvHash = "e81b08e6b88e296eaf6082242b39b5165d84382db6cd4921f7f248e82d80848b"
)

// loadDevSamples is the cache-only twin of scoreSession.loadSamples: builds
// the record's global card set per sample purely from cached bundle/verify
// entries, plus the union population the anchors intersect. A cache miss fails
// the test rather than re-running outcome — dev probes must never spend.
func loadDevSamples(t *testing.T, cache *Cache, rec RunRecord) (map[int][]ScoredItem, []string) {
	t.Helper()
	bundles := map[string]synthesis.EvidenceBundle{}
	var population []string
	for _, b := range rec.Buckets {
		var bundle synthesis.EvidenceBundle
		hit, err := cache.Get("bundle", b.BundleKey, &bundle)
		if err != nil {
			t.Fatal(err)
		}
		if !hit {
			t.Fatalf("bucket %s: bundle missing from cache", b.Bucket)
		}
		bundles[b.Bucket] = bundle
		population = append(population, b.Population...)
	}
	items := map[int][]ScoredItem{}
	for _, smp := range rec.SampleOutputs {
		var vo VerifiedOutput
		hit, err := cache.Get("verify", smp.VerifiedKey, &vo)
		if err != nil {
			t.Fatal(err)
		}
		if !hit {
			t.Fatalf("sample %d: verified output missing from cache", smp.SampleIndex)
		}
		items[smp.SampleIndex] = BuildGlobalScoredItems(vo.Snapshot, bundles)
	}
	return items, sortedSet(population)
}
