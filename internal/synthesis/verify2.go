package synthesis

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
)

// The verifier absorbs the raw contract's schema constraints
// (skills/synthesizing-workflow-insights/schema.json) as explicit Go checks
// rather than validating the decoded synthesis against the schema file: a JSON
// Schema validator would be a new dependency enforcing the same handful of
// rules, and encoding/json enforces none of them. Absorbed here: the
// schema_version const, non-empty title, evidence_ids minItems, the
// already_adopted verdict enum, the audience enum, the asset-type enum (via
// the grounding table) and the three-quote cap — each marked "absorbed schema
// constraint" at its check. A schema.json change must be mirrored here.

// RuleDateFunc reports when the rule file at sourcePath last changed, and
// whether that date could be determined at all. Injected so the recency
// arbitration is testable without a git repo; production builds it from
// cfg.DotfilesRepo (see gitRuleDate).
type RuleDateFunc func(sourcePath string) (time.Time, bool)

// maxFindings mirrors the skill's N <= 10 ranking bound.
const maxFindings = 10

// maxQuotes is the raw schema's per-finding quote cap.
const maxQuotes = 3

// rawSchemaVersion is the only raw contract this verifier understands.
const rawSchemaVersion = 2

// dateLayout is the bundle's session-date format (see sessionDate), the only
// granularity the recency arbitration has on either side of its comparison.
const dateLayout = "2006-01-02"

// VerifyGlobal validates a raw synthesis against the bundles it was produced
// from and returns the snapshot to store. Hard failures return an error and no
// snapshot; soft corrections are applied and recorded in
// meta.validation_notes.
func VerifyGlobal(raw insights.RawGlobalSynthesis, bundles map[string]EvidenceBundle, cfg insights.Config, generatedAt time.Time) (insights.GlobalSynthesisJSON, error) {
	return verifyGlobal(raw, bundles, cfg, generatedAt, gitRuleDate(cfg.DotfilesRepo))
}

// evidenceItem is one bundle item flattened for lookup by its namespaced id.
type evidenceItem struct {
	repo       string
	kind       byte     // F, P, S or G
	signalKind string   // G only: the OppSignal.Kind
	sessions   []string // one for F/P/S, member_sessions for G
	quotes     []string // quote field for F/P, detail lines for G
}

type verifier struct {
	bundles  map[string]EvidenceBundle
	items    map[string]evidenceItem
	cfg      insights.Config
	ruleDate RuleDateFunc
	home     string // "" when undeterminable: path rewriting then degrades to a no-op
	hard     []string
	notes    []string
}

func verifyGlobal(raw insights.RawGlobalSynthesis, bundles map[string]EvidenceBundle, cfg insights.Config, generatedAt time.Time, ruleDate RuleDateFunc) (insights.GlobalSynthesisJSON, error) {
	home, _ := os.UserHomeDir()
	v := &verifier{bundles: bundles, items: indexItems(bundles), cfg: cfg, ruleDate: ruleDate, home: home}

	if raw.SchemaVersion != rawSchemaVersion { // absorbed schema constraint
		v.fail(fmt.Sprintf("raw schema_version is %d, want %d", raw.SchemaVersion, rawSchemaVersion))
	}
	v.checkRanks(raw.Findings)

	findings := make([]insights.FindingJSON, 0, len(raw.Findings))
	for i, rf := range raw.Findings {
		if f, keep := v.finding(i, rf); keep {
			findings = append(findings, f)
		}
	}
	orderByRank(findings)
	dropped := v.dropped(raw.Dropped)
	for i, n := range v.notes {
		v.notes[i] = v.tilde(n)
	}
	v.scanPrivacy(findings, dropped)

	if len(v.hard) > 0 {
		return insights.GlobalSynthesisJSON{}, fmt.Errorf("verification failed: %s", strings.Join(v.hard, "; "))
	}
	return insights.GlobalSynthesisJSON{
		SchemaVersion: 2,
		GeneratedAt:   generatedAt,
		Window:        windowOf(bundles),
		Repos:         repoStats(bundles),
		Findings:      findings,
		Dropped:       dropped,
		Meta:          insights.GlobalMetaJSON{Model: cfg.SynthesisModel, ValidationNotes: v.notes},
	}, nil
}

// indexItems flattens every bundle into a namespaced-id lookup. Ids are keyed
// exactly as WriteBundleFiles wrote them ("<repo>/<id>"), which is the only
// form the model ever saw.
func indexItems(bundles map[string]EvidenceBundle) map[string]evidenceItem {
	items := map[string]evidenceItem{}
	for repo, b := range bundles {
		for _, f := range b.Friction {
			items[namespaceID(repo, f.ID)] = evidenceItem{repo: repo, kind: 'F', sessions: []string{f.SessionID}, quotes: nonEmpty(f.Quote)}
		}
		for _, p := range b.Prefs {
			items[namespaceID(repo, p.ID)] = evidenceItem{repo: repo, kind: 'P', sessions: []string{p.SessionID}, quotes: nonEmpty(p.Quote)}
		}
		for _, s := range b.Success {
			items[namespaceID(repo, s.ID)] = evidenceItem{repo: repo, kind: 'S', sessions: []string{s.SessionID}}
		}
		for _, g := range b.Signals {
			items[namespaceID(repo, g.ID)] = evidenceItem{repo: repo, kind: 'G', signalKind: g.Kind, sessions: g.MemberSessions, quotes: g.Detail}
		}
	}
	return items
}

func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}

// checkRanks enforces the 1..N permutation with N <= maxFindings.
func (v *verifier) checkRanks(findings []insights.RawFinding) {
	if len(findings) > maxFindings {
		v.fail(fmt.Sprintf("%d findings exceeds the maximum of %d", len(findings), maxFindings))
		return
	}
	seen := map[int]bool{}
	for _, f := range findings {
		if f.Rank < 1 || f.Rank > len(findings) {
			v.fail(fmt.Sprintf("rank %d is outside 1..%d", f.Rank, len(findings)))
			continue
		}
		if seen[f.Rank] {
			v.fail(fmt.Sprintf("rank %d appears more than once", f.Rank))
		}
		seen[f.Rank] = true
	}
}

// groundingKinds mirrors the skill's grounding table exactly: the evidence
// kinds each asset type may cite. An asset type missing from the table is
// itself a fail-closed reason — the model invented a rung of the ladder.
var groundingKinds = map[string]string{
	"claude_md_rule": "PF",
	"repo_doc":       "PF",
	"hook":           "FG",
	"setting":        "FG",
	"new_skill":      "GP",
	"habit":          "FS",
	"placement_fix":  "PF",
}

// retypingSignalKinds are the only signals a new_skill may cite: the skill
// ladder reserves its most expensive rung for a retyped ritual.
var retypingSignalKinds = map[string]bool{"retyped_directives": true, "retyped_kickoffs": true}

// audienceRequired lists the asset types whose asset cannot bind without
// naming who must see it.
var audienceRequired = map[string]bool{"claude_md_rule": true, "repo_doc": true}

// adoptedVerdicts is the raw schema's already_adopted enum.
var adoptedVerdicts = map[string]bool{"yes": true, "no": true, "unknown": true}

// finding verifies one raw finding and returns its snapshot form. keep is
// false when a soft correction removed the finding outright.
func (v *verifier) finding(i int, rf insights.RawFinding) (f insights.FindingJSON, keep bool) {
	where := fmt.Sprintf("findings[%d]", i)
	if rf.Title == "" { // absorbed schema constraint
		v.fail(where + " has an empty title")
	}
	if !adoptedVerdicts[rf.AlreadyAdopted.Verdict] { // absorbed schema constraint
		// A "Yes"/"adopted" variant would read as not-adopted to every
		// consumer, silently un-filtering a finding the model marked done.
		v.fail(fmt.Sprintf("%s has invalid already_adopted verdict %q", where, rf.AlreadyAdopted.Verdict))
	}
	v.checkCitations(where, rf.EvidenceIDs)
	v.checkGrounding(where, rf.Asset.Type, rf.EvidenceIDs)
	v.checkAudience(where, rf.Asset.Type, rf.Audience)
	v.checkNoNumbers(where, map[string]string{
		"title": rf.Title, "statement": rf.Statement, "rank_rationale": rf.RankRationale,
	})

	f = insights.FindingJSON{
		Rank: rf.Rank, Title: rf.Title, Statement: rf.Statement, RankRationale: rf.RankRationale,
		Asset: rf.Asset, Audience: rf.Audience, EvidenceIDs: rf.EvidenceIDs, Quotes: rf.Quotes,
		AlreadyAdopted: rf.AlreadyAdopted, EscalatedFrom: rf.EscalatedFrom,
	}
	v.fillGoOwned(&f)
	v.filterQuotes(&f)
	v.checkAdopted(&f)
	keep = v.checkEscalation(where, &f)
	v.normalizePaths(&f)
	return f, keep
}

// normalizePaths rewrites every path-bearing field to ~-relative, and the two
// excerpt fields' embedded home paths with them. It runs last on purpose: the
// adopted and escalated-from excerpts are matched against their files as the
// model copied them, and rewriting first would fail every excerpt taken from a
// config file that names an absolute path.
func (v *verifier) normalizePaths(f *insights.FindingJSON) {
	f.Asset.Target = v.tilde(f.Asset.Target)
	f.AlreadyAdopted.SourcePath = v.tilde(f.AlreadyAdopted.SourcePath)
	f.AlreadyAdopted.Excerpt = v.tilde(f.AlreadyAdopted.Excerpt)
	if f.EscalatedFrom != nil {
		// Copied, not mutated: the pointer is shared with the caller's raw
		// synthesis, which the verifier must leave untouched.
		from := *f.EscalatedFrom
		from.SourcePath = v.tilde(from.SourcePath)
		from.Excerpt = v.tilde(from.Excerpt)
		f.EscalatedFrom = &from
	}
}

// checkEscalation verifies a placement_fix's escalated_from — the existing
// rule must be real and quoted verbatim — and then arbitrates recency, which
// the model is forbidden to judge because it never sees session dates. keep is
// false when the rule postdates every cited violation: it never had a chance
// to work, so escalating it is premature.
func (v *verifier) checkEscalation(where string, f *insights.FindingJSON) (keep bool) {
	if f.Asset.Type != "placement_fix" {
		return true
	}
	from := f.EscalatedFrom
	if from == nil {
		v.fail(where + " placement_fix is missing escalated_from")
		return true
	}
	if from.SourcePath == "" || from.Excerpt == "" {
		// An empty excerpt is "in" every file, so this must fail before the
		// verbatim check rather than downgrade the way an adopted verdict does:
		// an unevidenced escalation is the fail-closed class.
		v.fail(where + " placement_fix escalated_from is missing source_path or excerpt")
		return true
	}
	switch found, err := excerptInFile(from.SourcePath, from.Excerpt, v.home); {
	case err != nil:
		v.fail(fmt.Sprintf("%s placement_fix escalated_from is unverifiable: cannot read %s", where, v.tilde(from.SourcePath)))
		return true
	case !found:
		v.fail(fmt.Sprintf("%s placement_fix escalated_from excerpt is not verbatim in %s", where, v.tilde(from.SourcePath)))
		return true
	}

	// Without a dotfiles repo there is no history to date the rule against, so
	// the check is skipped entirely rather than guessed at — a documented blind
	// spot, not a silent one.
	if v.cfg.DotfilesRepo == "" || v.ruleDate == nil {
		return true
	}
	ruleDate, ok := v.ruleDate(from.SourcePath)
	if !ok {
		v.note(fmt.Sprintf("finding %q: recency check skipped, no change date for %s", f.Title, v.tilde(from.SourcePath)))
		return true
	}
	if f.LastSeen == "" {
		v.note(fmt.Sprintf("finding %q: recency check skipped, cited evidence has no session dates", f.Title))
		return true
	}
	// Dates are day-granular on both sides, so a same-day violation counts as
	// postdating the rule: dropping a real escalation costs more than keeping a
	// borderline one.
	if f.LastSeen < ruleDate.Format(dateLayout) {
		v.note(fmt.Sprintf("finding %q: escalation removed, %s changed after every cited session", f.Title, v.tilde(from.SourcePath)))
		return false
	}
	return true
}

// orderByRank sorts the findings array by rank and re-normalizes ranks to a
// gapless 1..N. Consumers are contracted to preserve the array's order as the
// model's ranking, so the array and the rank field must agree; only a recency
// removal can open a gap, since the input permutation is verified.
func orderByRank(findings []insights.FindingJSON) {
	sort.SliceStable(findings, func(i, j int) bool { return findings[i].Rank < findings[j].Rank })
	for i := range findings {
		findings[i].Rank = i + 1
	}
}

// checkAdopted holds an "already in place" verdict to its evidence: a yes
// needs a source path and an excerpt that is really in that file. Anything
// less downgrades to unknown rather than failing the run — an over-confident
// adopted verdict costs one filtered finding, not a synthesis.
func (v *verifier) checkAdopted(f *insights.FindingJSON) {
	a := &f.AlreadyAdopted
	if a.Verdict != "yes" {
		return
	}
	if a.SourcePath == "" || a.Excerpt == "" {
		v.downgradeAdopted(f, "no source_path and excerpt to check")
		return
	}
	found, err := excerptInFile(a.SourcePath, a.Excerpt, v.home)
	switch {
	case err != nil:
		v.downgradeAdopted(f, "cannot read "+v.tilde(a.SourcePath))
	case !found:
		v.downgradeAdopted(f, "excerpt is not verbatim in "+v.tilde(a.SourcePath))
	}
}

func (v *verifier) downgradeAdopted(f *insights.FindingJSON, reason string) {
	f.AlreadyAdopted.Verdict = "unknown"
	v.note(fmt.Sprintf("finding %q: already_adopted downgraded to unknown (%s)", f.Title, reason))
}

// excerptInFile reports whether excerpt appears in the file at path, exactly
// or modulo whitespace — the same tolerance the quote index applies, so a
// re-wrapped copy of a real rule is not treated as invented.
func excerptInFile(path, excerpt, home string) (bool, error) {
	data, err := os.ReadFile(expandHome(path, home))
	if err != nil {
		return false, err
	}
	content := string(data)
	if strings.Contains(content, excerpt) {
		return true, nil
	}
	return strings.Contains(normalizeWS(content), normalizeWS(excerpt)), nil
}

// expandHome turns a ~- or $HOME-relative path back into an absolute one for
// reading. The model is told to emit ~-relative paths, and Go normalizes them
// on the way out, so both forms reach the existence checks.
func expandHome(path, home string) string {
	if home == "" {
		return path
	}
	for _, prefix := range []string{"~", "$HOME"} {
		if path == prefix {
			return home
		}
		if strings.HasPrefix(path, prefix+"/") {
			return filepath.Join(home, path[len(prefix)+1:])
		}
	}
	return path
}

// tilde rewrites the user's home directory (literal or $HOME) to "~"
// anywhere in s. Applied to every path-bearing structured field and to every
// note Go authors, so the stored snapshot never carries a real home path.
func (v *verifier) tilde(s string) string {
	if v.home != "" {
		s = strings.ReplaceAll(s, v.home, "~")
	}
	return strings.ReplaceAll(s, "$HOME", "~")
}

// filterQuotes drops every quote that is not verbatim in a cited item's quote
// pool — the pool being that item's own quote, or a cited signal's detail
// lines. A drop is a soft correction, not a failure: the note count is the
// eval's fabrication signal, and dropping keeps the fabricated text out of the
// snapshot.
func (v *verifier) filterQuotes(f *insights.FindingJSON) {
	if len(f.Quotes) > maxQuotes { // absorbed schema constraint (maxItems)
		// Trimmed before the pool check, keeping the model's own ordering: the
		// quotes past the cap were never part of a legal finding.
		v.note(fmt.Sprintf("finding %q: trimmed to %d quotes", f.Title, maxQuotes))
		f.Quotes = f.Quotes[:maxQuotes]
	}
	var pool []string
	for _, id := range f.EvidenceIDs {
		if item, ok := v.items[id]; ok {
			pool = append(pool, item.quotes...)
		}
	}
	kept, dropped := newQuoteIndex(pool).filter(f.Quotes)
	f.Quotes = kept
	if dropped > 0 {
		// The dropped text itself is deliberately not quoted back into the
		// note: it is unverified model output, and notes are stored.
		v.note(fmt.Sprintf("finding %q: dropped %d quote(s) not verbatim in the cited evidence", f.Title, dropped))
	}
}

// fillGoOwned computes the four fields the model never authors, from the cited
// evidence alone: which repos back the finding, how many distinct sessions do
// (a cited signal contributes all its member sessions), the latest of their
// dates, and the acted key.
func (v *verifier) fillGoOwned(f *insights.FindingJSON) {
	repos := map[string]bool{}
	sessions := map[string]bool{}
	lastSeen := ""
	for _, id := range f.EvidenceIDs {
		item, ok := v.items[id]
		if !ok {
			continue
		}
		repos[item.repo] = true
		dates := v.bundles[item.repo].SessionDates
		for _, sid := range item.sessions {
			// Session ids are only unique within a repo, so the distinct-session
			// set is keyed by repo too.
			sessions[item.repo+"\x00"+sid] = true
			if d := dates[sid]; d > lastSeen {
				lastSeen = d // "2006-01-02": lexical max == chronological max
			}
		}
	}
	f.Repos = sortedKeys(repos)
	f.SessionCount = len(sessions)
	f.LastSeen = lastSeen
	f.ActedKey = ActedKeyV2(f.Asset.Type, f.Statement)
}

// ActedKeyV2 is the v2 acted-store key: v1's hash mechanism (ActedKey) over
// asset type + normalized statement, with no source repo — a cross-repo
// finding has none. This orphans all v1 acted state at cutover, accepted
// without migration because the store is empty (spec §Verification).
func ActedKeyV2(assetType, statement string) string {
	norm := strings.Join(strings.Fields(strings.ToLower(statement)), " ")
	sum := sha256.Sum256([]byte(assetType + "\x00" + norm))
	return hex.EncodeToString(sum[:])[:16]
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func (v *verifier) dropped(entries []insights.DroppedJSON) []insights.DroppedJSON {
	out := make([]insights.DroppedJSON, 0, len(entries))
	for i, d := range entries {
		where := fmt.Sprintf("dropped[%d]", i)
		v.checkCitations(where, d.EvidenceIDs)
		v.checkNoNumbers(where, map[string]string{"summary": d.Summary, "reason": d.Reason})
		out = append(out, d)
	}
	return out
}

// checkNoNumbers is v1 Finalize's quantitative-claim guard at v2's widened
// scope: Go owns every number describing the evidence, so a model-authored one
// in prose is a fabrication risk. asset.content is deliberately not passed in —
// a deliverable may state a bound that is part of the practice itself. The
// offending text is never echoed into the reason: fail-closed reasons are
// stored in last_run.error, and this text is unscanned model output.
func (v *verifier) checkNoNumbers(where string, fields map[string]string) {
	for _, name := range sortedFieldNames(fields) {
		if hasQuantitativeClaim(fields[name]) {
			v.fail(fmt.Sprintf("%s %s contains a quantitative claim", where, name))
		}
	}
}

// sortedFieldNames keeps multi-field checks deterministic across runs, since
// map iteration order is not.
func sortedFieldNames(fields map[string]string) []string {
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// checkCitations fails closed on any id that does not resolve to a bundle item
// — an invented id is the failure mode every downstream count would silently
// inherit — and on citing nothing at all, which would otherwise pass every
// evidence check vacuously and land a finding with no support.
func (v *verifier) checkCitations(where string, ids []string) {
	if len(ids) == 0 { // absorbed schema constraint (minItems)
		v.fail(where + " cites no evidence")
	}
	for _, id := range ids {
		if _, ok := v.items[id]; !ok {
			v.fail(fmt.Sprintf("%s cites unknown evidence id %q", where, id))
		}
	}
}

// checkGrounding enforces the skill's grounding table. Dangling ids are
// skipped here — checkCitations already failed the run over them, and a second
// reason for the same id adds noise, not information.
func (v *verifier) checkGrounding(where, assetType string, ids []string) {
	allowed, ok := groundingKinds[assetType]
	if !ok {
		v.fail(fmt.Sprintf("%s has unknown asset type %q", where, assetType))
		return
	}
	for _, id := range ids {
		item, known := v.items[id]
		if !known {
			continue
		}
		if !strings.ContainsRune(allowed, rune(item.kind)) {
			v.fail(fmt.Sprintf("%s asset type %s cites out-of-kind evidence id %q", where, assetType, id))
			continue
		}
		if assetType == "new_skill" && item.kind == 'G' && !retypingSignalKinds[item.signalKind] {
			v.fail(fmt.Sprintf("%s new_skill cites non-retyping signal %q (kind %s)", where, id, item.signalKind))
		}
	}
}

func (v *verifier) checkAudience(where, assetType, audience string) {
	if audience != "" && !validAudiences[audience] {
		v.fail(fmt.Sprintf("%s has invalid audience %q", where, audience))
	}
	if audience == "" && audienceRequired[assetType] {
		v.fail(fmt.Sprintf("%s asset type %s is missing audience", where, assetType))
	}
}

// scanPrivacy is the v1 blocking privacy scan (leakPatterns) over every
// free-text field the snapshot would publish, run after path normalization so
// only genuinely un-normalizable leaks remain. The two excerpt fields are
// exempt: they are verbatim copies of config files that legitimately name
// absolute paths, and Go has already rewritten $HOME inside them.
func (v *verifier) scanPrivacy(findings []insights.FindingJSON, dropped []insights.DroppedJSON) {
	for i, f := range findings {
		fields := map[string]string{
			"title": f.Title, "statement": f.Statement, "rank_rationale": f.RankRationale,
			"asset.target": f.Asset.Target, "asset.content": f.Asset.Content,
		}
		for qi, q := range f.Quotes {
			fields[fmt.Sprintf("quotes[%d]", qi)] = q
		}
		v.checkNoLeaks(fmt.Sprintf("findings[%d]", i), fields)
	}
	for i, d := range dropped {
		v.checkNoLeaks(fmt.Sprintf("dropped[%d]", i), map[string]string{"summary": d.Summary, "reason": d.Reason})
	}
	for i, n := range v.notes {
		v.checkNoLeaks(fmt.Sprintf("meta.validation_notes[%d]", i), map[string]string{"note": n})
	}
}

// checkNoLeaks fails closed on any field tripping a leak pattern. The matched
// text is never repeated in the reason — the reason is stored in
// last_run.error, so echoing the leak would leak it again.
func (v *verifier) checkNoLeaks(where string, fields map[string]string) {
	for _, name := range sortedFieldNames(fields) {
		for _, re := range leakPatterns {
			if re.MatchString(fields[name]) {
				v.fail(fmt.Sprintf("%s %s trips the privacy scan", where, name))
				break
			}
		}
	}
}

func (v *verifier) fail(reason string) { v.hard = append(v.hard, reason) }

// note records a soft correction. Notes are Go-authored, so the model can
// never inject one: raw.Meta is discarded wholesale.
func (v *verifier) note(text string) { v.notes = append(v.notes, text) }

func windowOf(bundles map[string]EvidenceBundle) insights.WindowBoundsJSON {
	var w insights.WindowBoundsJSON
	for _, b := range bundles {
		if b.From != "" && (w.From == "" || b.From < w.From) {
			w.From = b.From
		}
		if b.To > w.To {
			w.To = b.To
		}
	}
	return w
}

func repoStats(bundles map[string]EvidenceBundle) []insights.RepoStatsJSON {
	out := make([]insights.RepoStatsJSON, 0, len(bundles))
	for repo, b := range bundles {
		out = append(out, insights.RepoStatsJSON{
			Key:    repo,
			Window: insights.WindowBoundsJSON{From: b.From, To: b.To},
			// SessionCount/AnalyzedCount come from the bundle, never from the
			// model: the model never saw either number.
			SessionCount: b.SessionCount, AnalyzedCount: b.AnalyzedCount,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// gitRuleDate dates a rule file from the dotfiles repo's git history. An empty
// dotfilesRepo yields nil: the recency arbitration is skipped entirely rather
// than guessed at (spec §Verification). Anything that stops git answering —
// a path outside the repo, an untracked file, no git at all — reports
// "undatable" rather than a wrong date, which keeps the escalation.
func gitRuleDate(dotfilesRepo string) RuleDateFunc {
	if dotfilesRepo == "" {
		return nil
	}
	home, _ := os.UserHomeDir()
	return func(sourcePath string) (time.Time, bool) {
		rel, err := filepath.Rel(dotfilesRepo, expandHome(sourcePath, home))
		if err != nil || strings.HasPrefix(rel, "..") {
			return time.Time{}, false
		}
		out, err := exec.Command("git", "-C", dotfilesRepo, "log", "-1", "--format=%cI", "--", rel).Output()
		if err != nil {
			return time.Time{}, false
		}
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(string(out)))
		if err != nil {
			return time.Time{}, false // includes the empty output of an untracked path
		}
		return t, true
	}
}
