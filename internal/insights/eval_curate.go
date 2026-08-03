package insights

import (
	"sort"
	"strings"

	"github.com/rkparsons/agent-insights/internal/transcript"
)

// sessionStat pairs a transcript ref with its deterministic stats for curation.
type sessionStat struct {
	Ref   transcript.TranscriptRef
	Stats AgentSessionStats
	Bytes int64
}

// curatedSession is one selected eval session: the ref, its stats, the stratum cell
// it filled, and how many repeats it gets (5 for the dangerous zero-friction
// direction, 3 otherwise).
type curatedSession struct {
	Ref     transcript.TranscriptRef
	Stats   AgentSessionStats
	Cell    string
	Repeats int
}

func isZeroFriction(s AgentSessionStats) bool {
	return s.ToolErrors == 0 && s.Interrupts == 0 && s.Rejections == 0
}

func isFrictionful(s AgentSessionStats) bool { return !isZeroFriction(s) }

// IsMeta reports whether a session is about the insights pipeline itself (cwd/repo
// mentions insights/facet, or it ran the analysis skill). Meta sessions invert
// eval findings, so benchmark scoring populations exclude them.
func IsMeta(s AgentSessionStats) bool {
	hay := strings.ToLower(s.Cwd + " " + s.Repo)
	if strings.Contains(hay, "insights") || strings.Contains(hay, "facet") {
		return true
	}
	for _, sk := range s.Skills {
		if sk == "analyzing-agent-sessions" {
			return true
		}
	}
	return false
}

// shape is a deterministic proxy for the judged session_type, used only to spread
// the sample (never to grade the model — that would be circular).
func shape(s AgentSessionStats) string {
	switch {
	case s.Edits+s.Writes > 0:
		return "implementation"
	case s.AssistantTurns >= 10:
		return "exploration"
	default:
		return "quick_question"
	}
}

func lengthBucket(s AgentSessionStats) string {
	switch {
	case s.AssistantTurns < 5:
		return "short"
	case s.AssistantTurns < 50:
		return "medium"
	default:
		return "long"
	}
}

func repeatsFor(s AgentSessionStats) int {
	if isZeroFriction(s) {
		return 5
	}
	return 3
}

type cellSpec struct {
	name  string
	n     int
	match func(AgentSessionStats) bool
}

func all(ps ...func(AgentSessionStats) bool) func(AgentSessionStats) bool {
	return func(s AgentSessionStats) bool {
		for _, p := range ps {
			if !p(s) {
				return false
			}
		}
		return true
	}
}

func isShort(s AgentSessionStats) bool     { return lengthBucket(s) == "short" }
func isMedium(s AgentSessionStats) bool    { return lengthBucket(s) == "medium" }
func isLong(s AgentSessionStats) bool      { return lengthBucket(s) == "long" }
func isQuickQ(s AgentSessionStats) bool    { return shape(s) == "quick_question" }
func isExplore(s AgentSessionStats) bool   { return shape(s) == "exploration" }
func isImpl(s AgentSessionStats) bool      { return shape(s) == "implementation" }
func isUnmatched(s AgentSessionStats) bool { return s.Repo == "" }
func anyStats(AgentSessionStats) bool      { return true }

// evalCells is the ordered stratum list. Greedy fill in this priority order, after
// the global-max outlier, makes the selected 16-set a deterministic function of the
// corpus snapshot.
func evalCells() []cellSpec {
	return []cellSpec{
		{"meta", 1, IsMeta},
		{"zero-short", 1, all(isZeroFriction, isShort)},
		{"zero-quickq", 1, all(isZeroFriction, isQuickQ)},
		{"zero-explore", 1, all(isZeroFriction, isExplore)},
		{"zero-impl", 1, all(isZeroFriction, isImpl)},
		{"zero-long", 1, all(isZeroFriction, isLong)},
		{"zero-extra", 2, isZeroFriction},
		{"friction-medium", 2, all(isFrictionful, isMedium)},
		{"friction-long", 2, all(isFrictionful, isLong)},
		{"repo-unmatched", 1, isUnmatched},
		{"gap-fill", 2, anyStats},
	}
}

// curate selects the eval set: the global-turn outlier, then a greedy total-order
// fill of the stratum cells. Candidates are considered in ascending session-id, so
// selection is fully deterministic for a fixed pool. A session fills exactly one cell.
func curate(pool []sessionStat) []curatedSession {
	sorted := append([]sessionStat(nil), pool...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Ref.SessionID < sorted[j].Ref.SessionID
	})

	used := map[string]bool{}
	var out []curatedSession
	pick := func(ss sessionStat, cell string) {
		used[ss.Ref.SessionID] = true
		out = append(out, curatedSession{Ref: ss.Ref, Stats: ss.Stats, Cell: cell, Repeats: repeatsFor(ss.Stats)})
	}

	if len(sorted) > 0 {
		best := 0
		for i := 1; i < len(sorted); i++ {
			a, b := sorted[i], sorted[best]
			switch {
			case a.Stats.AssistantTurns != b.Stats.AssistantTurns:
				if a.Stats.AssistantTurns > b.Stats.AssistantTurns {
					best = i
				}
			case a.Bytes != b.Bytes:
				if a.Bytes > b.Bytes {
					best = i
				}
			}
		}
		pick(sorted[best], "outlier")
	}

	for _, cell := range evalCells() {
		filled := 0
		for _, ss := range sorted {
			if filled >= cell.n {
				break
			}
			if used[ss.Ref.SessionID] {
				continue
			}
			if cell.match(ss.Stats) {
				pick(ss, cell.name)
				filled++
			}
		}
	}
	return out
}

// CurateIDs runs the deterministic stratified curation over bare stats and
// returns session-id → stratum cell. Exported for the outcome-eval --l1-sample
// loop, which curates from the frozen corpus rather than a live walk.
func CurateIDs(stats []AgentSessionStats, sizes map[string]int64) map[string]string {
	pool := make([]sessionStat, 0, len(stats))
	for _, s := range stats {
		pool = append(pool, sessionStat{
			Ref:   transcript.TranscriptRef{SessionID: s.SessionID},
			Stats: s,
			Bytes: sizes[s.SessionID],
		})
	}
	out := make(map[string]string)
	for _, c := range curate(pool) {
		out[c.Ref.SessionID] = c.Cell
	}
	return out
}
