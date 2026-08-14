package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// frozenPrefRules reads every standing-preference rule out of the frozen
// baseline pool — the same text BuildBundle turns into P* items.
func frozenPrefRules(t *testing.T) []string {
	t.Helper()
	home, _ := os.UserHomeDir()
	dataDir := filepath.Join(home, "Developer", "insights-eval-data")
	if _, err := os.Stat(filepath.Join(dataDir, "manifest.json")); err != nil {
		t.Skip("insights-eval-data not present")
	}
	pool := filepath.Join(dataDir, "baseline-pool", "v1")
	entries, err := os.ReadDir(pool)
	if err != nil {
		t.Fatal(err)
	}
	var rules []string
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		a, err := loadPoolAnalysis(filepath.Join(pool, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range a.StandingPreferences {
			rules = append(rules, p.Rule)
		}
	}
	if len(rules) == 0 {
		t.Fatal("frozen pool carries no standing preferences — the pin has no data")
	}
	return rules
}

// clusterSizes runs the pref-clustering mechanism over plain rule texts and
// returns each component's size, largest first.
func clusterSizes(rules []string, bar float64) ([]int, [][]string) {
	toks := make([]map[string]int, len(rules))
	for i, r := range rules {
		toks[i] = practiceTokens(r)
	}
	uf := make([]int, len(rules))
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
	for i := range rules {
		for j := i + 1; j < len(rules); j++ {
			if multisetJaccard(toks[i], toks[j]) >= bar {
				uf[find(i)] = find(j)
			}
		}
	}
	byRoot := map[int][]string{}
	for i, r := range rules {
		byRoot[find(i)] = append(byRoot[find(i)], r)
	}
	var sizes []int
	var biggest []string
	for _, members := range byRoot {
		sizes = append(sizes, len(members))
		if len(members) > len(biggest) {
			biggest = members
		}
	}
	for i := range sizes { // insertion sort, descending — a handful of entries
		for j := i; j > 0 && sizes[j] > sizes[j-1]; j-- {
			sizes[j], sizes[j-1] = sizes[j-1], sizes[j]
		}
	}
	return sizes, [][]string{biggest}
}

func capInts(xs []int, n int) []int {
	if len(xs) <= n {
		return xs
	}
	return xs[:n]
}

func ruleContaining(t *testing.T, rules []string, sub string) string {
	t.Helper()
	for _, r := range rules {
		if strings.Contains(r, sub) {
			return r
		}
	}
	t.Fatalf("frozen pool carries no rule containing %q — re-pin the bars against the current pool", sub)
	return ""
}

// TestProbeSimilarityBarsPinnedToFrozenPool pins both similarity bars to the
// frozen preference corpus (191 rules at time of pinning) instead of borrowing
// the bundle's directive-clustering threshold, which review showed is wrong for
// prose rules: at 0.6 the corpus yields ZERO clusters at the recurrence floor
// (largest component: 2), which would make PrefRecallMisses structurally dead
// and MembershipChurn warn near-unconditionally.
//
//	bar   components >= 3
//	0.60  0        (the borrowed directive-clause bar — dead)
//	0.55  1  (size 3)
//	0.50  1  (size 5)   <- prefClusterSimilarity
//	0.45  2  (6, 3)
//	0.40  4  (8, 4, 3, 3)
//
// Genuine same-practice restatements in this corpus score 0.40-0.61, and every
// pair inspected in the 0.40-0.50 band restates one practice — no cross-practice
// pair was observed there, which is what makes churn's looser bar safe.
func TestProbeSimilarityBarsPinnedToFrozenPool(t *testing.T) {
	rules := frozenPrefRules(t)
	t.Logf("frozen pool: %d standing-preference rules", len(rules))

	// 1. the preference bar must still find the corpus's one recurring practice
	sizes, biggest := clusterSizes(rules, prefClusterSimilarity)
	if len(sizes) == 0 || sizes[0] < prefClusterFloor {
		t.Fatalf("prefClusterSimilarity %.2f yields no cluster at the recurrence floor %d (sizes %v) — the probe is structurally dead",
			prefClusterSimilarity, prefClusterFloor, sizes)
	}
	t.Logf("prefClusterSimilarity %.2f: largest component sizes %v", prefClusterSimilarity, capInts(sizes, 8))
	joined := strings.ToLower(strings.Join(biggest[0], " | "))
	if !strings.Contains(joined, "adversarial") && !strings.Contains(joined, "critically review") {
		t.Fatalf("the largest cluster is not the corpus's known recurring practice (spec review before implementing): %v", biggest[0])
	}

	// 2. the churn bar must MATCH same-practice restatements: an unmatched
	// finding scores maximal churn, so a bar above the restatement band makes
	// the stability gate warn unconditionally.
	a := ruleContaining(t, rules, "Run an adversarial review with an Opus subagent on design specs")
	b := ruleContaining(t, rules, "Run multi-pronged adversarial subagent review")
	if sim := multisetJaccard(practiceTokens(a), practiceTokens(b)); sim < churnMatchSimilarity {
		t.Fatalf("same-practice restatements score %.3f, below churnMatchSimilarity %.2f — every run would read as maximal churn",
			sim, churnMatchSimilarity)
	}

	// 3. …without matching plainly different practices.
	other := ruleContaining(t, rules, "Stay consistent with the existing conventions of sibling files")
	if sim := multisetJaccard(practiceTokens(a), practiceTokens(other)); sim >= churnMatchSimilarity {
		t.Fatalf("churnMatchSimilarity %.2f matches two different practices (%.3f) — the bar is in the noise",
			churnMatchSimilarity, sim)
	}
}
