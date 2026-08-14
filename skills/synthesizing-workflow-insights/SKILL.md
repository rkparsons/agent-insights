---
name: synthesizing-workflow-insights
description: Use when the working directory holds per-repo EvidenceBundle files (`<repo>-bundle.json`, ids namespaced like `alpha/F3`) plus a `manifest.md` naming existing-asset locations, and the task is one global cross-repo synthesis of ranked workflow-improvement findings. Writes RawGlobalSynthesis JSON to ./synthesis.json — qualitative only, no numbers.
---

# Synthesizing Workflow Insights

One global pass over every repository's evidence. You cluster practices across repos,
check the user's existing assets before proposing anything, rank by leverage, and
write one JSON file.

## Inputs and output

- The working directory holds one `<repo>-bundle.json` per repository: numbered
  friction items (`F*`), standing-preference items (`P*`), success items (`S*`),
  Go-computed inefficiency signals (`G*`), and context rollups. Every item id is
  namespaced with its repo key (`alpha/F3`) — cite ids exactly in that form.
- `manifest.md` states the global evidence window (as prose) and names where the
  user's existing assets live: the global CLAUDE.md, each repo's checkout root
  (repo CLAUDE.mds and docs), the skills directory, settings.json, and the dotfiles
  git history when available.
- Write one `RawGlobalSynthesis` JSON to `./synthesis.json` — a file, not stdout.
  The exact shape is this skill's `schema.json`:
  `{"schema_version": 2, "findings": [...], "dropped": [...]}`. Omit `meta` — Go
  stamps it.

## Conduct — read-only run

- Read/Glob/Grep freely over the bundle files and every asset location the
  manifest names.
- `./synthesis.json` is the only file you write. Never create or edit anything
  else; no network access.
- Bash exists for one purpose: `git log` under the manifest's dotfiles path, to
  confirm an existing rule's history. If the manifest lists no dotfiles history,
  an existing rule simply "exists now" — proceed without dating it.
- You never see per-session dates (bundles carry none; the manifest states only
  the window bounds). Never claim, estimate, or reason from when evidence
  occurred — Go holds the dates and arbitrates every recency question (see
  Escalation).

## Hard rules

- **Reference evidence by id only.** Every finding and every dropped entry cites bundle ids (`F*/P*/S*/G*`) in their namespaced form exactly as the bundle files carry them. Never invent an id.
- **No evidence counts.** Do not write counts, rates, "N sessions", "X times", or percentages describing the evidence in any `title`, `statement`, `rank_rationale`, `asset.content`, or `dropped` summary/reason. Go computes all numbers. A bound that is part of the recommended practice itself (e.g. "at most four options") is content, not a count: keep it exactly as the evidence states it.
- **Quotes must be copied verbatim** from a cited item's `quote` field (or a cited signal's `detail` line) into `quotes` — at most three per finding. Do not paraphrase: a quote that is not byte-for-byte in a cited item's quote pool is dropped by the verifier.
- **Grounding is typed.** Each `asset.type` may cite only the evidence kinds the grounding table below allows; one out-of-kind id fails the entire synthesis closed.
- **Every path is `~`-relative.** Write `~/CLAUDE.md`, `~/Developer/<repo>/docs/x.md` — never an absolute home path (`/Users/<name>/…`, `/home/<name>/…`, `$HOME/…`), and never in ANY field: `asset.target`, `already_adopted.source_path`, `escalated_from.source_path`, and equally the free text of `statement`, `asset.content`, `rank_rationale`, `title`, and `dropped` summaries/reasons. Structured path fields are rewritten for you; free text is NOT — one absolute home path in prose fails the entire synthesis closed. The manifest names every asset location `~`-relative already: copy that form. Two kinds of copied text are the exceptions, because both stay byte-for-byte: a quote — so when the only verbatim form of a quote carries an absolute home path, quote a different line instead — and an excerpt (`already_adopted.excerpt`, `escalated_from.excerpt`), which you copy exactly as the source file has it, absolute paths and all. Never "tidy" a path inside an excerpt: that makes it no longer verbatim, which downgrades an `already_adopted` verdict and fails a `placement_fix` closed. Go rewrites excerpts for you, after it has checked them against the file.
- **Empty arrays are valid** and common. Do not manufacture findings.

## Statement fidelity (findings)

- **Carry every qualifier the evidence states — not just the practice.** Orderings ("fetch, then diff"), conditions ("only on explicit request"), scope gates ("on heavy work, not small tasks"), limits ("at most four options"), and required follow-ups ("re-run the review after a rewrite") are what make a practice correct. Before emitting a statement, re-read its evidence items and carry each qualifier, bound, ordering, and follow-up they state; a statement that names the practice but omits any evidenced qualifier is wrong, not shorter.
- **Two-sided boundaries stay two-sided.** When the evidence separates a sanctioned case from a forbidden one, state both sides. Never collapse a boundary into a blanket "always/never" that outlaws the sanctioned case.
- **Permission gates are act-specific.** When evidence gates several related acts (e.g. committing / pushing / opening a PR / merging / writing to memory), state each act at its own evidenced strictness — never let one act's stricter gate bleed onto a neighboring act the evidence gates more loosely, and never merge the acts into one blanket ban. When one item states an absolute-sounding ban on an act that other evidence in the same bundle shows being performed as a normal part of requested work, scope the ban to its evidenced trigger (the unrequested or out-of-scope instances), not the widest reading. The adjacent absolutes are unchanged: acts the evidence does gate stay gated at their stated strength — an "only on explicit request" qualifier is never softened — and every qualifier the evidence states is still carried (Statement fidelity).
- **One practice per finding.** Separable practices get separate findings, each grounded in its own evidence — do not fold a neighboring practice into an existing statement just because the same sessions evidence both. This cuts both ways: when several separable practices govern the same act (e.g. when to perform it, and what must be true before performing it), each surfaces as its own finding — the best-evidenced practice for an act must not absorb or displace the others.

## Cross-repo clustering

- **One practice = one finding, across every repo.** When the same practice shows up in several bundles, emit exactly one finding and cite every repo's evidence for it — never per-repo variants of one practice. A cross-repo recurrence is a stronger finding, not several weaker ones.
- **Cite exhaustively.** Cluster near-identical standing preferences across sessions and repos into one finding, citing the cluster exhaustively — every `P*` that states the practice and every `F*` the proposed asset would have prevented, subject to the grounding table for the `asset.type` you chose; an undercited finding understates its real support. A practice evidenced only by `P*` cannot be a `hook`/`setting`: either it has `F*`/`G*` evidence the guard would have prevented, or it belongs at rung 2 of the ladder. Do not pad either: an id belongs in `evidence_ids` only if it genuinely evidences this practice.
- **Signal `detail` lines are evidence qualifiers.** When a `G*` signal carries `detail` (named mechanical modes, exemplar error texts, or exemplar retyped directives), carry those named modes / exemplar directives into the finding statement — statement fidelity applies to them exactly as to item qualifiers, and a mechanical-friction finding names the concrete modes, never generic "reduce tool errors".
- **Nothing substantive vanishes.** Every recurring standing-preference cluster, every substantive `G*` signal, and every notable friction cluster ends up either cited by a finding (of any type) or in `dropped` with a reason — never silently omitted, and never dropped just because bigger clusters loom larger.

## Findings and the asset ladder

Every finding proposes one concrete asset: `asset.type`, its destination
`asset.target` (file path, skill name, or settings key), and `asset.content` — the
exact deliverable ready to apply (the rule lines, the skill outline, the hook
guard), not a description of it. Every finding names a change to make — codify the
ritual, automate the manual step, guard the failure edge, relocate the rule; a
success observation with no improvement action is dashboard material, not a
finding. Prefer the cheapest asset that would actually have prevented the cited
evidence, in this order:

1. **`hook` / `setting`** — deterministic guards that fire every time, immune to context drift. When a friction item shows a defect that shipped or was committed and a mechanical guard would have blocked it, recommend the guard — a single incident is enough evidence; recurrence is not required for prevention.
2. **`claude_md_rule` / `repo_doc`** — prose rules. `claude_md_rule` is a rule line for the user-global CLAUDE.md; `repo_doc` is a checked-in repo file (a repo CLAUDE.md section or docs page — including creating the file where the repo has none). Checked-in files are loaded deterministically and reach subagents; prefer `repo_doc` over the personal file when the evidence shows failures in delegated work or long-context drift.
3. **Memory** — not an asset type. A preference tied to a single session or to personal context is already best served by auto-memory; promoting it to a rule is noise. Such evidence goes to `dropped`.
4. **`new_skill`** — the most expensive rung: only for a multi-step ritual a one-line rule cannot encode. The bar is a retyped ritual, not a retyped sentence. A `retyped_directives`/`retyped_kickoffs` signal's finding names the recurring directive(s) from the signal's `detail` as promotion candidates — `new_skill` for a ritual; a one-liner belongs as a `claude_md_rule` grounded in the `P*` items that state it (`G*` may not appear in a `claude_md_rule`'s `evidence_ids`). One practice per finding still applies: separable rituals get separate findings.

`habit` sits outside the ladder: a practice no file or automation can carry.
`asset.target` and `asset.content` are omitted for `habit` — its deliverable is
its statement. `placement_fix` is reserved for escalations (below).

## Grounding table

| `asset.type` | `evidence_ids` may cite |
| --- | --- |
| `claude_md_rule` | `P*`, `F*` |
| `repo_doc` | `P*`, `F*` |
| `hook`, `setting` | `F*`, `G*` |
| `new_skill` | retyping-kind `G*` (`retyped_directives`/`retyped_kickoffs`), `P*` |
| `habit` | `F*`, `S*` |
| `placement_fix` | `P*`, `F*` — plus `escalated_from` citing the existing rule |

`G*` signals are directly citable where the table allows them (their member
sessions carry the support). `dropped` entries may cite ids of any kind. The
verifier enforces this table exactly — one out-of-kind citation fails the run, so
re-check every id against it before writing the file.

## Existing assets: adopted and escalation

- **Check before you propose.** Before emitting any finding, look for the proposed asset at the manifest's asset locations — Read/Grep the actual files. Recommending a rule that already exists is the failure mode this contract exists to prevent.
- `already_adopted` answers exactly one question: **is the proposed asset already in place at its target?** `yes` requires `source_path` plus a verbatim `excerpt` copied from that file — Go verifies the excerpt appears byte-for-byte, and a paraphrase downgrades the verdict to `unknown`. `no` means you checked the target and it is absent. `unknown` means you could not check.
- **Escalation.** When a practice is already covered by an existing rule in the manifest's assets AND the bundles show it being violated or re-stated, the finding is a `placement_fix`: its asset is the enforcement or placement change — copy the rule into the checked-in file that reaches subagents, add the clause the existing rule lacks, add a mechanical guard, require task briefs to carry the line — never a restatement of the rule. Put the existing rule's `source_path` and verbatim `excerpt` in `escalated_from`.
- For a `placement_fix`, `already_adopted` is about the fix itself (is the rule already copied to the proposed location? is the guard already present?), never about the pre-existing rule — that one lives in `escalated_from`.
- **Recency is not yours to judge.** You cannot see session dates, so never argue that violations postdate the rule. Propose the escalation on coverage + violations alone; Go compares the rule's git-change date against the cited sessions' dates and removes the finding itself when the rule never had a chance to work.

## Ranking

- `rank` is a 1..N permutation over your findings, N ≤ 10; 1 is the highest-leverage change.
- Each finding carries a one-sentence `rank_rationale` — why it sits where it does, with no numbers. Weigh how broadly the pattern recurs (sessions and repos, judged from the evidence you cited), the cost per occurrence (reverted work, lost work, wrong verdicts delivered to the user, rejected round-trips), and how cheap and deterministic the fix is. A rare pattern with catastrophic cost and a deterministic guard can outrank a frequent mild annoyance.
- Judge pervasiveness against the bundles' `context` rollups (`tool_mix`, `session_types`, `skills`) as denominators — a friction cluster around one tool means more when that tool dominates the histogram. This arithmetic informs your ranking only; no number from it ever appears in output text.
- When the evidence supports more than ten findings, emit the ten highest-leverage and give each remaining practice a `dropped` entry whose reason names what kept it out.

## Titles

Every finding carries a `title`: an imperative short handle for browsing (e.g.
"Verify before claiming done") — at most 40 characters, no trailing period, no
numbers or counts (Go computes all numbers; a bound that is part of the practice
belongs in the statement, not the title), distinct from every other title in this
output, and front-loaded so it stays meaningful truncated to ~30 characters. The
title names the practice; every evidenced qualifier stays in `statement`
(Statement fidelity applies there).

## Audience

- Every `claude_md_rule` and `repo_doc` finding sets `audience` — who must see the asset for it to bind: `user | orchestrator | subagents | both | automation`. `audience` is REQUIRED on `claude_md_rule` and `repo_doc` (the synthesis fails closed without it) and optional on other types.
- A rule violated in delegated/subagent-produced work while the orchestrator held the rule is a reach failure: state the propagation fix (inject the rule into implementer/reviewer subagent prompts), not just the rule itself.

## Dropped

- Every substantive evidence cluster you weighed and did not turn into a finding gets a `dropped` entry: a one-line `summary`, a `reason` naming the judgment, and its `evidence_ids` (any kind).
- Good reasons name why no asset earns its keep: the existing asset already covers it and the fix would restate it; the signal is dominated by legitimate causes; too diffuse to name a concrete asset; infrastructure rather than workflow; a single incident the user already addressed; auto-memory is already the right home; already working with no improvement to exploit; outranked by stronger findings. A bare "low value" is not a reason.
- Dropping is a judgment, not a shortcut: the reason must survive a reader checking the cited ids. No numbers in summaries or reasons — the hard rule applies to `dropped` too.

## Before writing `./synthesis.json`

Re-verify, in order:

1. Ranks form a 1..N permutation, N ≤ 10.
2. Every cited id exists in a bundle file, in namespaced form, and obeys the grounding table for its finding's `asset.type`.
3. Every quote is byte-for-byte from a cited item's `quote` field or a cited signal's `detail` line, at most three per finding.
4. No counts, rates, or percentages describing evidence anywhere in free text — titles, statements, rationales, asset content, dropped summaries and reasons.
5. Every path anywhere in the file is `~`-relative — no `/Users/…`, `/home/…` or `$HOME/…` in any target, source path, statement, asset content, rationale, title, or dropped summary/reason. `quotes` and the two `excerpt` fields are the exceptions: they stay byte-for-byte, paths included.
6. Every `claude_md_rule`/`repo_doc` has `audience`; every `placement_fix` has `escalated_from`; every `already_adopted` `yes` has `source_path` and a verbatim `excerpt`.
7. `schema_version` is `2` and the file parses as JSON.
