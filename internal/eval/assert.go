package eval

import (
	"encoding/json"
	"os"
	"sort"
	"time"
)

type FreezeIssues struct {
	Gaps            []string `json:"gaps"`
	Skews           []string `json:"skews"`
	CountMismatches []string `json:"count_mismatches"`
}

func (i FreezeIssues) Clean() bool {
	return len(i.Gaps) == 0 && len(i.Skews) == 0 && len(i.CountMismatches) == 0
}

// Blocking reports whether the baseline pool must be withheld. Gaps
// (transcripts pruned before the freeze ever ran) are recorded but never
// blocking — a no-gaps gate can never pass once a transcript is gone.
func (i FreezeIssues) Blocking() bool {
	return len(i.Skews) > 0 || len(i.CountMismatches) > 0
}

// AssertFrozen verifies every benchmark id has a frozen corpus entry (gaps) and
// that the frozen transcript's mtime equals the pool analysis's stamped mtime
// (skews — the judged fields never saw content appended after analysis).
// poolMtime looks up the stamped transcript_mtime for an id — the caller
// passes a lookup over baseline-pool/v1 once it's canonical, or over the live
// analyses pool before v1 exists (see RunFreeze). Resolution for a skew:
// `tmux-ctrl insights analyze <id>`, then re-freeze.
//
// Known-unresolvable case: once a session is frozen, FreezeCorpus never
// re-reads its live transcript (the frozen file and its manifest Mtime are
// canonical on every re-run), so the frozen mtime is fixed forever at
// whatever the live file's mtime was at first freeze. If the live transcript
// keeps growing afterward, every re-judge stamps the transcript's new (grown)
// mtime, which can never again equal that immutable frozen mtime — the skew
// is permanent for that id. The only fix is excluding the id from the bucket
// via a new baseline-pool version; that re-baseline mechanism is deferred to
// a later design.
func AssertFrozen(b Benchmark, m Manifest, countProblems []string, poolMtime func(id string) (time.Time, bool)) FreezeIssues {
	entries := map[string]ManifestEntry{}
	for _, e := range m.Entries {
		entries[e.SessionID] = e
	}
	iss := FreezeIssues{CountMismatches: countProblems}
	for repo, bp := range b.Buckets {
		for _, id := range bp.AsConsumed {
			e, ok := entries[id]
			if !ok {
				iss.Gaps = append(iss.Gaps, repo+"/"+id)
				continue
			}
			if mt, ok := poolMtime(id); ok && !mt.Equal(e.Mtime) {
				iss.Skews = append(iss.Skews, repo+"/"+id)
			}
		}
	}
	sort.Strings(iss.Gaps)
	sort.Strings(iss.Skews)
	return iss
}

// readStampedMtime reads the transcript_mtime field out of a stored analysis
// JSON at path — the same shape insights.ReadAnalysisMtime reads from the
// live pool, generalized to any path so AssertFrozen can be pointed at
// baseline-pool/v1 once it's canonical.
func readStampedMtime(path string) (time.Time, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, false
	}
	var stamp struct {
		TranscriptMtime time.Time `json:"transcript_mtime"`
	}
	if err := json.Unmarshal(data, &stamp); err != nil {
		return time.Time{}, false
	}
	return stamp.TranscriptMtime, true
}
