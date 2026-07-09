package synthesis

import (
	"fmt"
	"sort"
	"strings"

	"tmux-ctrl/internal/insights"
)

const signalFloor = 3

type FrictionItem struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	OneLine   string `json:"one_line"`
	Quote     string `json:"quote,omitempty"`
	File      string `json:"file,omitempty"`
	SessionID string `json:"session_id"`
}
type PrefItem struct {
	ID        string `json:"id"`
	Rule      string `json:"rule"`
	Quote     string `json:"quote"`
	SessionID string `json:"session_id"`
}
type SuccessItem struct {
	ID          string   `json:"id"`
	Goal        string   `json:"goal"`
	Summary     string   `json:"summary"`
	SessionType string   `json:"session_type"`
	Reads       int      `json:"reads"`
	Skills      []string `json:"skills"`
	SessionID   string   `json:"session_id"`
}
type OppSignal struct {
	ID             string   `json:"id"`
	Kind           string   `json:"kind"`
	Magnitude      int      `json:"magnitude"`
	MemberSessions []string `json:"member_sessions"`
	// Detail: mode names / verbatim exemplar text only, ranked by array order —
	// never counts (the numberClaim guard has holes; if Detail carries no
	// numbers, none can leak into prose).
	Detail []string `json:"detail,omitempty"`
}
type ContextRollup struct {
	Skills       map[string]int `json:"skills"`
	SessionTypes map[string]int `json:"session_types"`
	ToolMix      map[string]int `json:"tool_mix"`
}
type EvidenceBundle struct {
	Repo          string         `json:"repo"`
	SessionCount  int            `json:"session_count"`
	AnalyzedCount int            `json:"analyzed_count"`
	From          string         `json:"from"`
	To            string         `json:"to"`
	Friction      []FrictionItem `json:"friction"`
	Prefs         []PrefItem     `json:"prefs"`
	Success       []SuccessItem  `json:"success"`
	Signals       []OppSignal    `json:"signals"`
	Context       ContextRollup  `json:"context"`
}

// BuildBundle turns a repo's analyses into an EvidenceBundle: typed-id items sorted
// deterministically by Start then session_id, Go-computed inefficiency signals, and
// context rollups, with file paths relativized/redacted for privacy. Sorting by Start
// (session_id only breaks ties) makes From/To — taken from the first/last element — the
// true chronological window bounds, not whatever order session_ids happened to fall in.
func BuildBundle(repoKey string, group []insights.AgentSessionAnalysis) EvidenceBundle {
	sorted := append([]insights.AgentSessionAnalysis(nil), group...)
	sort.Slice(sorted, func(i, j int) bool {
		if !sorted[i].Stats.Start.Equal(sorted[j].Stats.Start) {
			return sorted[i].Stats.Start.Before(sorted[j].Stats.Start)
		}
		return sorted[i].Stats.SessionID < sorted[j].Stats.SessionID
	})

	b := EvidenceBundle{Repo: repoKey, AnalyzedCount: len(sorted), SessionCount: len(sorted)}
	b.Context = ContextRollup{Skills: map[string]int{}, SessionTypes: map[string]int{}, ToolMix: map[string]int{}}

	for _, a := range sorted {
		for _, inc := range a.FrictionIncidents {
			b.Friction = append(b.Friction, FrictionItem{
				ID: fmt.Sprintf("F%d", len(b.Friction)+1), Type: inc.Type, OneLine: inc.OneLine,
				Quote: inc.EvidenceQuote, File: relativizeFile(inc.File), SessionID: a.Stats.SessionID,
			})
		}
		for _, p := range a.StandingPreferences {
			b.Prefs = append(b.Prefs, PrefItem{
				ID: fmt.Sprintf("P%d", len(b.Prefs)+1), Rule: p.Rule, Quote: p.EvidenceQuote, SessionID: a.Stats.SessionID,
			})
		}
		if a.Outcome == "fully_achieved" || a.Outcome == "mostly_achieved" {
			b.Success = append(b.Success, SuccessItem{
				ID: fmt.Sprintf("S%d", len(b.Success)+1), Goal: a.UnderlyingGoal, Summary: a.BriefSummary,
				SessionType: a.SessionType, Reads: a.Stats.ToolCounts["Read"], Skills: a.Stats.Skills, SessionID: a.Stats.SessionID,
			})
		}
		b.Context.SessionTypes[a.SessionType]++
		for _, s := range a.Stats.Skills {
			b.Context.Skills[s]++
		}
		for tool, n := range a.Stats.ToolCounts {
			b.Context.ToolMix[tool] += n
		}
	}
	if len(sorted) > 0 {
		b.From = sorted[0].Stats.Start.Format("2006-01-02")
		b.To = sorted[len(sorted)-1].Stats.Start.Format("2006-01-02")
	}
	b.Signals = computeSignals(sorted)
	return b
}

// relativizeFile makes a file path repo-relative and redacts any residual home path.
func relativizeFile(file string) string {
	if file == "" {
		return ""
	}
	if i := strings.Index(file, "/.worktrees/"); i >= 0 {
		rest := file[i+len("/.worktrees/"):]
		if j := strings.IndexByte(rest, '/'); j >= 0 {
			return rest[j+1:] // strip the worktree-name segment → repo-relative
		}
	}
	if strings.HasPrefix(file, "/Users/") || strings.HasPrefix(file, "/home/") {
		return "[redacted]"
	}
	return file
}

// computeSignals derives Go inefficiency signals (one per kind), each covering the
// sessions in the >= p90 tail of its metric, emitted only when >= signalFloor sessions qualify.
func computeSignals(group []insights.AgentSessionAnalysis) []OppSignal {
	reads := make([]int, len(group))
	for i, a := range group {
		reads[i] = a.Stats.ToolCounts["Read"]
	}
	readP90 := percentile(reads, 90)

	dens := make([]float64, len(group))
	for i, a := range group {
		turns := a.Stats.AssistantTurns
		if turns < 1 {
			turns = 1
		}
		dens[i] = float64(a.Stats.Interrupts+a.Stats.Rejections+a.Stats.ToolErrors) / float64(turns)
	}
	densP90 := percentileF(dens, 90)

	var highRead, fricDensity, unskilled []string
	for _, a := range group {
		r := a.Stats.ToolCounts["Read"]
		turns := a.Stats.AssistantTurns
		if turns < 1 {
			turns = 1
		}
		d := float64(a.Stats.Interrupts+a.Stats.Rejections+a.Stats.ToolErrors) / float64(turns)
		if r >= readP90 && r > 0 {
			highRead = append(highRead, a.Stats.SessionID)
		}
		if d >= densP90 && d > 0 {
			fricDensity = append(fricDensity, a.Stats.SessionID)
		}
		if a.SessionType == "single_task" && len(a.Stats.Skills) == 0 && r >= readP90 && r > 0 {
			unskilled = append(unskilled, a.Stats.SessionID)
		}
	}

	var out []OppSignal
	add := func(kind string, members, detail []string) {
		if len(members) >= signalFloor {
			out = append(out, OppSignal{ID: fmt.Sprintf("G%d", len(out)+1), Kind: kind,
				Magnitude: len(members), MemberSessions: members, Detail: detail})
		}
	}
	add("high_read", highRead, nil)
	add("friction_density", fricDensity, nil)
	add("unskilled_toil", unskilled, nil)
	mm, md := mechanicalFrictionMembers(group)
	add("mechanical_friction", mm, md)
	return out
}

// percentile returns the p-th percentile value (nearest-rank) of ints.
func percentile(xs []int, p int) int {
	if len(xs) == 0 {
		return 0
	}
	s := append([]int(nil), xs...)
	sort.Ints(s)
	idx := (p*len(s) + 99) / 100 // ceil(p/100 * n)
	if idx < 1 {
		idx = 1
	}
	if idx > len(s) {
		idx = len(s)
	}
	return s[idx-1]
}

// percentileF is percentile for float64 (friction density).
func percentileF(xs []float64, p int) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	idx := (p*len(s) + 99) / 100
	if idx < 1 {
		idx = 1
	}
	if idx > len(s) {
		idx = len(s)
	}
	return s[idx-1]
}
