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
	// runs/2026-07-10T18-44-11Z.json.
	devCurRecord = "2026-07-10T06-13-21Z-626084000.json"
	// devCurEnvHash is that verdict's provenance.matcher_env_hash
	// (claude 2.1.206, snapshot 7fe1f6cd).
	devCurEnvHash = "e81b08e6b88e296eaf6082242b39b5165d84382db6cd4921f7f248e82d80848b"
)

// loadDevBuckets is the cache-only twin of scoreSession.loadBuckets: builds
// each bucket's scoring material purely from cached bundle/verify entries. A
// cache miss fails the test rather than re-running outcome — dev probes must
// never spend.
func loadDevBuckets(t *testing.T, cache *Cache, rec RunRecord) map[string]bucketData {
	t.Helper()
	buckets := map[string]bucketData{}
	for _, b := range rec.Buckets {
		var bundle synthesis.EvidenceBundle
		hit, err := cache.Get("bundle", b.BundleKey, &bundle)
		if err != nil {
			t.Fatal(err)
		}
		if !hit {
			t.Fatalf("bucket %s: bundle missing from cache", b.Bucket)
		}
		bd := bucketData{outputs: b, items: map[int][]ScoredItem{}, oneLines: sessionOneLines(bundle)}
		for _, smp := range b.Samples {
			var vo VerifiedOutput
			hit, err := cache.Get("verify", smp.VerifiedKey, &vo)
			if err != nil {
				t.Fatal(err)
			}
			if !hit {
				t.Fatalf("bucket %s sample %d: verified output missing from cache", b.Bucket, smp.SampleIndex)
			}
			bd.items[smp.SampleIndex] = BuildScoredItems(b.Bucket, vo, bundle)
		}
		buckets[b.Bucket] = bd
	}
	return buckets
}
