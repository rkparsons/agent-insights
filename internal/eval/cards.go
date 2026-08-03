package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/rkparsons/agent-insights/internal/synthesis"
)

// Card is the recognition surface for one contested trigger: rubric statement
// + produced item text + verified quotes (+ pool one_lines for membership
// cards). NO transcripts, NO session ids — cards stay in the local cache and
// are never committed (spec).
type Card struct {
	KeyHash         string   `json:"key_hash"`
	TargetID        string   `json:"target_id"`
	Trigger         string   `json:"trigger"`
	Adjudicable     bool     `json:"adjudicable"`
	Statement       string   `json:"rubric_statement"`
	ItemText        string   `json:"item_text,omitempty"`
	Granularity     string   `json:"granularity,omitempty"`
	Quotes          []string `json:"quotes,omitempty"`
	AddedOneLines   []string `json:"added_one_lines,omitempty"`
	MissingOneLines []string `json:"missing_one_lines,omitempty"`
	Note            string   `json:"note,omitempty"`
	Key             AdjKey   `json:"key"` // id-set as hash only; lets `adjudicate` write the entry
}

const oneLineCap = 6

var sessionUUIDRe = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

// sessionOneLines maps each session to its most recognizable pool line:
// friction one_line, else standing-pref rule, else success summary — the
// membership-card vocabulary (pool-sourced, already privacy-relativized).
func sessionOneLines(b synthesis.EvidenceBundle) map[string]string {
	out := map[string]string{}
	for _, s := range b.Success {
		if _, ok := out[s.SessionID]; !ok {
			out[s.SessionID] = s.Summary
		}
	}
	for _, p := range b.Prefs {
		out[p.SessionID] = p.Rule
	}
	for _, f := range b.Friction {
		out[f.SessionID] = f.OneLine
	}
	return out
}

// BuildCards renders every pending card. Membership triggers (anchor
// shortfall / size-cap breach) get the added/missing sessions as one_lines;
// no card may contain a session id — that is a build error, never a warning.
func BuildCards(results []TargetResult, anchors map[string][]string, oneLines map[string]map[string]string) ([]Card, error) {
	var cards []Card
	for _, res := range results {
		for _, p := range res.Pending {
			c := Card{TargetID: p.TargetID, Trigger: p.Trigger, Adjudicable: p.Adjudicable,
				Statement: res.Rubric.Statement, ItemText: p.ItemText,
				Granularity: p.Granularity, Quotes: p.Quotes, Note: p.Note}
			if p.Adjudicable {
				c.Key = p.Key
				c.KeyHash = p.Key.Hash()
			} else {
				c.KeyHash = cacheKey("card", p.TargetID, p.Trigger)
			}
			if p.Trigger == CorroborationMismatch || p.Trigger == CorroborationSizeCap || p.Trigger == CorroborationCrossBucket {
				lines := oneLines[bucketOf(p.Ref)]
				anchorSet := stringSet(anchors[p.TargetID])
				itemSet := stringSet(p.SessionIDs)
				var added, missing []string
				for _, id := range sortedSet(p.SessionIDs) {
					if !anchorSet[id] {
						added = append(added, lineFor(lines, id))
					}
				}
				if p.Trigger != CorroborationCrossBucket { // cross-bucket sets are anchor-disjoint by construction
					for _, id := range sortedSet(anchors[p.TargetID]) {
						if !itemSet[id] {
							missing = append(missing, lineFor(lines, id))
						}
					}
				}
				c.AddedOneLines = capWithSuffix(added)
				c.MissingOneLines = capWithSuffix(missing)
			}
			raw, err := json.Marshal(c)
			if err != nil {
				return nil, err
			}
			if sessionUUIDRe.Match(raw) {
				return nil, fmt.Errorf("card %s/%s would contain a session id — refusing to build", c.TargetID, c.Trigger)
			}
			cards = append(cards, c)
		}
	}
	return cards, nil
}

func lineFor(lines map[string]string, id string) string {
	if l, ok := lines[id]; ok && l != "" {
		return l
	}
	return "(no pool summary)"
}

func capWithSuffix(lines []string) []string {
	if len(lines) <= oneLineCap {
		return lines
	}
	return append(capStrings(lines, oneLineCap), fmt.Sprintf("… and %d more", len(lines)-oneLineCap))
}

// RenderCardsMarkdown renders the human pass: one section per card with the
// adjudicate command ready to copy.
func RenderCardsMarkdown(cards []Card) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Contested cards — %d\n", len(cards))
	for _, c := range cards {
		fmt.Fprintf(&b, "\n## %s — %s [%s]\n\n", c.TargetID, c.Trigger, short(c.KeyHash))
		fmt.Fprintf(&b, "**Rubric:** %s\n\n", c.Statement)
		if c.ItemText != "" {
			fmt.Fprintf(&b, "**Produced item** (%s): %s\n\n", c.Granularity, c.ItemText)
		}
		for _, q := range c.Quotes {
			fmt.Fprintf(&b, "> %s\n", q)
		}
		if len(c.AddedOneLines) > 0 {
			b.WriteString("\n**Sessions beyond the anchors:**\n")
			for _, l := range c.AddedOneLines {
				fmt.Fprintf(&b, "- %s\n", l)
			}
		}
		if len(c.MissingOneLines) > 0 {
			b.WriteString("\n**Anchor sessions the item missed:**\n")
			for _, l := range c.MissingOneLines {
				fmt.Fprintf(&b, "- %s\n", l)
			}
		}
		if c.Note != "" {
			fmt.Fprintf(&b, "\n_%s_\n", c.Note)
		}
		if c.Adjudicable {
			fmt.Fprintf(&b, "\n`agent-insights eval adjudicate %s accept|reject [--note \"...\"]`\n", short(c.KeyHash))
		} else {
			b.WriteString("\n_informational — no adjudication (resolves via status edit, rubric edit, or the next comparable run)_\n")
		}
	}
	return b.String()
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// WriteCards persists card JSONs + cards.md under <cache>/cards/<ts>/. The
// markdown render is local and regenerable — plain WriteFile, no atomicity
// needed.
func WriteCards(cacheDir, ts string, cards []Card) (string, error) {
	dir := filepath.Join(cacheDir, "cards", ts)
	if err := os.MkdirAll(dir, 0o755); err != nil { // zero-card runs still get cards.md
		return "", err
	}
	for i, c := range cards {
		if err := writeJSON(filepath.Join(dir, fmt.Sprintf("card-%03d-%s.json", i+1, short(c.KeyHash))), c); err != nil {
			return "", err
		}
	}
	return dir, os.WriteFile(filepath.Join(dir, "cards.md"), []byte(RenderCardsMarkdown(cards)), 0o644)
}
