package eval

// Cache-hole mapper: replays the score loop cache-only (matchFromCache,
// never a matcher call) over every rubric × sample of a record and reports
// exactly which reads a re-score would have to buy fresh. 0 holes = a fully
// cache-served re-score. Guarded by CACHE_HOLES.

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDevCacheHoles(t *testing.T) {
	if os.Getenv("CACHE_HOLES") == "" {
		t.Skip("set CACHE_HOLES=1 to run the cache-hole mapper")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	cacheDir := filepath.Join(home, "Library", "Caches", "tmux-ctrl", "insights-eval")
	dataDir := filepath.Join(home, "Developer", "insights-eval-data")
	cache := NewCache(cacheDir)
	rec, err := LoadRunRecord(filepath.Join(cacheDir, "run-records", devCurRecord))
	if err != nil {
		t.Fatal(err)
	}
	rubrics, err := LoadRubrics(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	buckets := loadDevBuckets(t, cache, rec)

	const repeats = 3
	holes, samplesChecked := 0, 0
	for _, r := range rubrics {
		repos := r.Repos
		if r.Part == "negative" {
			repos = sortedBucketNames(buckets)
		}
		bd, ok := buckets[repos[0]]
		if !ok {
			continue
		}
		for _, so := range bd.outputs.Samples {
			items := itemsForSample(buckets, repos, so.SampleIndex)
			payload := BuildMatchPayload(r, items)
			if len(payload.Items) == 0 {
				continue
			}
			samplesChecked++
			var grans []string
			cached := map[int]bool{}
			var results []MatchResult
			for k := 0; k < repeats; k++ {
				res, hit, err := matchFromCache(cache, devCurEnvHash, payload, k)
				if err != nil {
					t.Fatal(err)
				}
				cached[k] = hit
				results = append(results, res)
			}
			if r.Part == "negative" {
				// negatives take every repeat, no early exit
				for k := 0; k < repeats; k++ {
					if !cached[k] {
						holes++
						t.Logf("HOLE negative %s s%d r%d", r.ID, so.SampleIndex, k)
					}
				}
				continue
			}
			byID := map[string]ScoredItem{}
			for _, it := range items {
				byID[it.ID] = it
			}
			anchors, capAnchors := AnchorSets(r, buckets[r.Repos[0]].outputs.Population, nil)
			for k := 0; k < repeats; k++ {
				if medianDecided(grans, repeats) {
					break // cached-side branch: stops at first miss, never fresh
				}
				if !cached[k] {
					holes++
					t.Logf("HOLE %s s%d r%d (grans so far %v, verdict needs this fresh)", r.ID, so.SampleIndex, k, grans)
					break // a fresh read's granularity is unknown; stop simulating this sample
				}
				rep := aggregateRepeat(r, byID, results[k], anchors, capAnchors, nil)
				grans = append(grans, rep.Granularity)
			}
		}
	}
	t.Logf("checked %d (rubric, sample) payloads: %d holes", samplesChecked, holes)
}
