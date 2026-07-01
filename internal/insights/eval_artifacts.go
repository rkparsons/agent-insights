package insights

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func hashSessionID(id string) string {
	sum := sha256.Sum256([]byte(id))
	return fmt.Sprintf("%x", sum)[:12]
}

// redactRepo reduces the repo (a full absolute path from resolveRepo — producer
// design) to its basename for the committed artifact: the repo identity is
// stratification-relevant (F7) but the home path is not, so only the name is kept.
func redactRepo(repo string) string {
	if repo == "" {
		return ""
	}
	return filepath.Base(repo)
}

type redactedFriction struct {
	Type    string `json:"type"`
	OneLine string `json:"one_line"`
}

// redactedRepeat is the committed, privacy-safe view of one repeat: judged shape +
// the fabrication booleans (re-scoreable), no verbatim quote text (that lives in the
// cards, for the human pass only).
type redactedRepeat struct {
	Outcome          string             `json:"outcome"`
	SessionType      string             `json:"session_type"`
	Friction         []redactedFriction `json:"friction_incidents"`
	PreferenceCount  int                `json:"preference_count"`
	RawQuoteVerbatim []bool             `json:"raw_quote_verbatim"`
	DroppedPrefs     int                `json:"dropped_preferences"`
}

// redactedAnalysis is one curated session's committed artifact. Session-id hashed;
// cwd / branch / files / fingerprints dropped (F7).
type redactedAnalysis struct {
	SessionHash    string           `json:"session_hash"`
	Cell           string           `json:"cell"`
	Repo           string           `json:"repo"`
	AiTitle        string           `json:"ai_title"`
	AssistantTurns int              `json:"assistant_turns"`
	ToolErrors     int              `json:"tool_errors"`
	Interrupts     int              `json:"interrupts"`
	Rejections     int              `json:"rejections"`
	ToolCounts     map[string]int   `json:"tool_counts"`
	WallClockNS    int64            `json:"wall_clock_ns"`
	Repeats        []redactedRepeat `json:"repeats"`
}

func redactRuns(runs []sessionRun) []redactedAnalysis {
	out := make([]redactedAnalysis, 0, len(runs))
	for _, sr := range runs {
		ra := redactedAnalysis{
			SessionHash: hashSessionID(sr.Stats.SessionID), Cell: sr.Cell, Repo: redactRepo(sr.Stats.Repo),
			AiTitle: sr.Stats.AiTitle, AssistantTurns: sr.Stats.AssistantTurns,
			ToolErrors: sr.Stats.ToolErrors, Interrupts: sr.Stats.Interrupts, Rejections: sr.Stats.Rejections,
			ToolCounts: sr.Stats.ToolCounts, WallClockNS: int64(sr.Stats.WallClock),
		}
		for _, rr := range sr.Repeats {
			rep := redactedRepeat{
				Outcome: rr.Validated.Outcome, SessionType: rr.Validated.SessionType,
				PreferenceCount: len(rr.Validated.StandingPreferences), DroppedPrefs: rr.Report.DroppedPreferences,
			}
			for _, inc := range rr.Validated.FrictionIncidents {
				rep.Friction = append(rep.Friction, redactedFriction{Type: inc.Type, OneLine: inc.OneLine})
			}
			for _, qc := range rr.RawQuotes {
				rep.RawQuoteVerbatim = append(rep.RawQuoteVerbatim, qc.Verbatim)
			}
			ra.Repeats = append(ra.Repeats, rep)
		}
		out = append(out, ra)
	}
	return out
}

type manifestEntry struct {
	SessionID      string `json:"session_id"`
	Cell           string `json:"cell"`
	Repeats        int    `json:"repeats"`
	Repo           string `json:"repo"`
	Cwd            string `json:"cwd"`
	AssistantTurns int    `json:"assistant_turns"`
	ToolErrors     int    `json:"tool_errors"`
	Interrupts     int    `json:"interrupts"`
	Rejections     int    `json:"rejections"`
}

func buildManifest(runs []sessionRun) []manifestEntry {
	out := make([]manifestEntry, 0, len(runs))
	for _, sr := range runs {
		out = append(out, manifestEntry{
			SessionID: sr.Stats.SessionID, Cell: sr.Cell, Repeats: len(sr.Repeats),
			Repo: sr.Stats.Repo, Cwd: sr.Stats.Cwd, AssistantTurns: sr.Stats.AssistantTurns,
			ToolErrors: sr.Stats.ToolErrors, Interrupts: sr.Stats.Interrupts, Rejections: sr.Stats.Rejections,
		})
	}
	return out
}

func writeJSONFile(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func writeCardsMarkdown(path string, cards []Card) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Light-eval contested cards (%d)\n\n", len(cards))
	fmt.Fprintln(&b, "Recognition pass: judge each claim from the title + your own opening words. No transcripts.")
	for i, c := range cards {
		fmt.Fprintf(&b, "## %d. %s\n\n", i+1, c.Title)
		fmt.Fprintf(&b, "> %s\n\n", c.Opening)
		fmt.Fprintf(&b, "**Claim:** %s\n\n", c.Claim)
		if c.Quote != "" {
			fmt.Fprintf(&b, "**Quote:** %q\n\n", c.Quote)
		}
		fmt.Fprintf(&b, "_Contested: %s_\n\n", c.ContestedReason)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// writeEvalArtifacts writes the committed report, redacted analyses, and cards to
// dir, and the full (un-redacted) manifest to localManifestPath (kept out of git).
func writeEvalArtifacts(dir, localManifestPath string, rep EvalReport, runs []sessionRun) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := writeJSONFile(filepath.Join(dir, "light-eval-report.json"), rep); err != nil {
		return err
	}
	if err := writeJSONFile(filepath.Join(dir, "light-eval-analyses.json"), redactRuns(runs)); err != nil {
		return err
	}
	if err := writeCardsMarkdown(filepath.Join(dir, "light-eval-cards.md"), rep.Cards); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(localManifestPath), 0o755); err != nil {
		return err
	}
	return writeJSONFile(localManifestPath, buildManifest(runs))
}
