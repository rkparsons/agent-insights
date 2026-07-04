package insightseval

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"tmux-ctrl/internal/insights"
	"tmux-ctrl/internal/sources/claude"
)

// SessionFacts is the cached output of the decode→stats→reduce stage for one
// frozen transcript.
type SessionFacts struct {
	Stats   insights.AgentSessionStats
	Reduced insights.ReducedInput
}

type FactsResult struct {
	Analyses      []insights.AgentSessionAnalysis
	Reduced       map[string]insights.ReducedInput
	GapFallbacks  []string
	FactsHash     string
	PoolSliceHash string
	CacheHits     int
	CacheMisses   int
}

func loadPoolAnalysis(path string) (insights.AgentSessionAnalysis, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return insights.AgentSessionAnalysis{}, err
	}
	var a insights.AgentSessionAnalysis
	if err := json.Unmarshal(raw, &a); err != nil {
		return insights.AgentSessionAnalysis{}, fmt.Errorf("%s: %w", path, err)
	}
	return a, nil
}

// RecomputeFacts implements the pool contract split: judged fields from the
// baseline pool, deterministic stats recomputed from the frozen corpus (cached
// by corpus sha + Go code version). Gap ids — benchmark sessions whose
// transcript was pruned before the freeze — fall back to their pool stats
// wholesale and are reported, never silently blended. The substantiality gate
// is deliberately never consulted: the benchmark id list is authoritative.
func RecomputeFacts(c *Corpus, cache *Cache, factsCodeVersion, poolDir string, ids []string) (FactsResult, error) {
	res := FactsResult{Reduced: map[string]insights.ReducedInput{}}
	sorted := slices.Clone(ids)
	slices.Sort(sorted)
	statsHash := sha256.New()
	poolHash := sha256.New()
	for _, id := range sorted {
		poolRaw, err := os.ReadFile(filepath.Join(poolDir, id+".json"))
		if err != nil {
			return res, fmt.Errorf("pool analysis %s: %w", id, err)
		}
		poolHash.Write([]byte(id))
		poolHash.Write([]byte{0})
		poolHash.Write(poolRaw)
		var pool insights.AgentSessionAnalysis
		if err := json.Unmarshal(poolRaw, &pool); err != nil {
			return res, fmt.Errorf("pool analysis %s: %w", id, err)
		}

		if !c.Has(id) {
			res.Analyses = append(res.Analyses, pool)
			res.GapFallbacks = append(res.GapFallbacks, id)
			writeStatsHash(statsHash, pool.Stats)
			continue
		}

		entry, _ := c.Entry(id)
		key := cacheKey("facts", entry.SHA256, factsCodeVersion)
		var sf SessionFacts
		hit, err := cache.Get("facts", key, &sf)
		if err != nil {
			return res, err
		}
		if hit {
			res.CacheHits++
		} else {
			res.CacheMisses++
			ref, err := c.Ref(id)
			if err != nil {
				return res, err
			}
			events, canary, _, err := claude.LoadTranscript(ref.Path)
			if err != nil {
				return res, fmt.Errorf("decode %s: %w", id, err)
			}
			repo := pool.Stats.Repo // freeze-verified identity; the live resolver is env-dependent
			ext := insights.Extract(events, canary, id, func(string) string { return repo })
			sf = SessionFacts{Stats: ext.Stats, Reduced: ext.Reduced}
			if err := cache.Put("facts", key, sf); err != nil {
				return res, err
			}
		}

		merged := pool
		merged.Stats = sf.Stats
		merged.TranscriptMtime = entry.Mtime
		res.Analyses = append(res.Analyses, merged)
		res.Reduced[id] = sf.Reduced
		writeStatsHash(statsHash, sf.Stats)
	}
	res.FactsHash = hex.EncodeToString(statsHash.Sum(nil))
	res.PoolSliceHash = hex.EncodeToString(poolHash.Sum(nil))
	return res, nil
}

func writeStatsHash(h interface{ Write([]byte) (int, error) }, s insights.AgentSessionStats) {
	raw, _ := json.Marshal(s)
	h.Write(raw)
	h.Write([]byte{0})
}
