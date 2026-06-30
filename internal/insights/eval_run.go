package insights

import (
	"context"
	"strings"

	"tmux-ctrl/internal/sources/claude"
)

// quoteCheck records, per raw (pre-validation) evidence quote, whether it is a
// verbatim substring of the transcript. This is the model-fabrication signal — the
// only place a hallucinated quote is visible, since validateQuotes clears/drops it.
type quoteCheck struct {
	Kind     string // "friction" | "preference"
	Quote    string
	Verbatim bool
}

// RepeatResult is one Judge call split into raw + validated output, with the
// raw-quote fabrication checks. One LLM call per RepeatResult.
type RepeatResult struct {
	Raw       JudgedFields
	Validated JudgedFields
	Report    ValidationReport
	RawQuotes []quoteCheck
}

// sessionRun is everything the scorers need for one curated session.
type sessionRun struct {
	Stats         AgentSessionStats
	Cell          string
	Repeats       []RepeatResult
	Verbatim      VerbatimIndex
	FirstUserTurn string
	ZeroFriction  bool
	Frictionful   bool
}

func rawVerbatim(kind, quote string, vi VerbatimIndex) quoteCheck {
	var ok bool
	if kind == "preference" {
		ok = vi.ContainsUser(quote) || vi.ContainsUserNormalized(quote)
	} else {
		ok = vi.ContainsAny(quote) || vi.ContainsAnyNormalized(quote)
	}
	return quoteCheck{Kind: kind, Quote: quote, Verbatim: ok}
}

// runRepeat performs one Judge call and captures raw + validated output plus the
// raw-quote fabrication checks. It reconstructs Analyze's internal pipeline so the
// raw (pre-validation) fields — where fabrication is visible — are observable.
func runRepeat(ctx context.Context, ext SessionExtraction, judge Judge) (RepeatResult, error) {
	raw, err := judge.Judge(ctx, ext.Reduced)
	if err != nil {
		return RepeatResult{}, err
	}
	validated, report := validateQuotes(raw, ext.Verbatim)
	var checks []quoteCheck
	for _, inc := range raw.FrictionIncidents {
		if inc.EvidenceQuote != "" {
			checks = append(checks, rawVerbatim("friction", inc.EvidenceQuote, ext.Verbatim))
		}
	}
	for _, p := range raw.StandingPreferences {
		if p.EvidenceQuote != "" {
			checks = append(checks, rawVerbatim("preference", p.EvidenceQuote, ext.Verbatim))
		}
	}
	return RepeatResult{Raw: raw, Validated: validated, Report: report, RawQuotes: checks}, nil
}

// runSession extracts once, runs `repeats` Judge calls, and assembles the sessionRun.
func runSession(ctx context.Context, events []claude.TranscriptEvent, canary claude.Canary, sessionID string, repo RepoResolver, cell string, repeats int, judge Judge) (sessionRun, error) {
	ext := Extract(events, canary, sessionID, repo)
	sr := sessionRun{
		Stats:         ext.Stats,
		Cell:          cell,
		Verbatim:      ext.Verbatim,
		FirstUserTurn: firstGenuineUserTurn(events),
		ZeroFriction:  isZeroFriction(ext.Stats),
		Frictionful:   isFrictionful(ext.Stats),
	}
	for i := 0; i < repeats; i++ {
		rr, err := runRepeat(ctx, ext, judge)
		if err != nil {
			return sessionRun{}, err
		}
		sr.Repeats = append(sr.Repeats, rr)
	}
	return sr, nil
}

const openingMaxRunes = 280

// firstGenuineUserTurn returns the user's first real prose turn, verbatim and
// truncated — skipping injected/synthetic content, interrupts, rejections, and
// task-notifications (the same predicates the reducer uses) so a card opening is
// never an interrupt marker or a subagent dump. Mirrors verbatim.go's user case.
func firstGenuineUserTurn(events []claude.TranscriptEvent) string {
	for _, ev := range events {
		if ev.Type != "user" || ev.Message == nil {
			continue
		}
		var parts []string
		for _, blk := range ev.Message.Content {
			if blk.Type == "text" {
				parts = append(parts, blk.Text)
			}
		}
		joined := strings.TrimSpace(strings.Join(parts, "\n"))
		if joined == "" {
			continue
		}
		if isTaskNotification(joined) || isRejectionText(joined) ||
			isSyntheticUserText(joined) || isInterruptText(joined) {
			continue
		}
		return truncateRunes(joined, openingMaxRunes)
	}
	return ""
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
