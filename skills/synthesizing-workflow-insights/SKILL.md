---
name: synthesizing-workflow-insights
description: Use when given a per-repo EvidenceBundle JSON (friction/pref/success items + inefficiency signals, each with a typed id) on stdin, and asked to synthesize workflow-improvement themes and recommendations. Emits a RawSynthesis JSON referencing the bundle's ids — qualitative only, no numbers.
---

# Synthesizing Workflow Insights

You receive an `EvidenceBundle` (JSON on stdin) for one repository: numbered friction
items (`F*`), standing-preference items (`P*`), success items (`S*`), Go-computed
inefficiency signals (`G*`), and context rollups. Produce a `RawSynthesis` JSON.

## Hard rules
- **Reference evidence by id only.** Every theme/recommendation cites the bundle's `F*/P*/S*/G*` ids. Never invent an id.
- **Reference kinds are fixed per field.** A friction theme's `evidence_ids` accept only `F*`; an opportunity theme's `evidence_ids` accept only `S*` and `F*`; `G*` ids go only in a theme's `signal_refs`, never in `evidence_ids`; `P*` ids never appear in any theme — they ground recommendations only. One out-of-kind reference invalidates the entire theme.
- **No evidence counts.** Do not write counts, rates, "N sessions", "X times", or percentages describing the evidence in any `summary` or `statement`. Go computes all numbers. A bound that is part of the recommended practice itself (e.g. "at most four options") is content, not a count: keep it exactly as the evidence states it.
- **Quotes must be copied verbatim** from a bundle item's `quote` field into `cited_quotes`. Do not paraphrase.
- **Empty arrays are valid** and common. Do not manufacture themes.

## Statement fidelity (themes and recommendations)
- **Carry every qualifier the evidence states — not just the practice.** Orderings ("fetch, then diff"), conditions ("only on explicit request"), scope gates ("on heavy work, not small tasks"), limits ("at most four options"), and required follow-ups ("re-run the review after a rewrite") are what make a practice correct. Before emitting a statement, re-read its evidence items and carry each qualifier, bound, ordering, and follow-up they state; a statement that names the practice but omits any evidenced qualifier is wrong, not shorter.
- **Two-sided boundaries stay two-sided.** When the evidence separates a sanctioned case from a forbidden one, state both sides. Never collapse a boundary into a blanket "always/never" that outlaws the sanctioned case.
- **Permission gates are act-specific.** When evidence gates several related acts (e.g. committing / pushing / opening a PR / merging / writing to memory), state each act at its own evidenced strictness — never let one act's stricter gate bleed onto a neighboring act the evidence gates more loosely, and never merge the acts into one blanket ban. When one item states an absolute-sounding ban on an act that other evidence in the same bundle shows being performed as a normal part of requested work, scope the ban to its evidenced trigger (the unrequested or out-of-scope instances), not the widest reading. The adjacent absolutes are unchanged: acts the evidence does gate stay gated at their stated strength — an "only on explicit request" qualifier is never softened — and every qualifier the evidence states is still carried (Statement fidelity).
- **One practice per recommendation.** Separable practices get separate recommendations, each grounded in its own evidence — do not fold a neighboring practice into an existing statement just because the same sessions evidence both. This cuts both ways: when several separable practices govern the same act (e.g. when to perform it, and what must be true before performing it), each surfaces as its own recommendation — the best-evidenced practice for an act must not absorb or displace the others.

## Themes
- **Friction themes** (`kind: "friction"`): cluster related friction items. `evidence_ids` are `F*` ids. The partition rule is absolute: each `F*` id appears in exactly one friction theme. Within that partition, cite completely — every friction item that belongs to a theme's cluster is listed in it; an item that fits two clusters is cited only in its single dominant theme, never both.
- **Opportunity themes** (`kind: "opportunity"`): a workflow worth improving. Anchor EITHER on `signal_refs` (>=1 `G*` inefficiency signal) OR, for a recurring clean workflow with no inefficiency signal, on `evidence_ids` of >=4 distinct `S*` success items. `evidence_ids` may also include `F*` ids as corroboration. Cite every `S*` (and corroborating `F*`) that evidences the workflow, not a representative sample — an undercited theme understates its real support. Reference kinds stay fixed: only `S*`/`F*` here, `G*` only in `signal_refs`, `P*` never. Work through the `G*` signals first: each substantive inefficiency signal deserves its own opportunity theme with a recommendation addressing it. Every opportunity summary must name the improvement to exploit — codify the workflow into a skill or habit, automate the manual step, extend the pattern, or guard its known failure edge. A success observation with no improvement action is dashboard material, not an insight: do not emit it.
- **Signal `detail` lines are evidence qualifiers.** When a `G*` signal carries `detail` (named mechanical modes, exemplar error texts, or exemplar retyped directives), carry those named modes / exemplar directives into the theme statement — statement fidelity applies to them exactly as to item qualifiers, and a mechanical-friction theme names the concrete modes, never generic "reduce tool errors". The adjacent absolutes are unchanged: no evidence counts in any summary or statement (Go computes all numbers), and `G*` ids still appear only in `signal_refs`, never in `evidence_ids`.

## Recommendations
- `type` ∈ `claude_md_rule | new_skill | hook | setting | habit`.
- Every recommendation carries a `title`: an imperative short handle for browsing (e.g. "Verify before claiming done") — at most 40 characters, no trailing period, no numbers or counts (Go computes all numbers; a bound that is part of the practice belongs in the statement, not the title), distinct from every other title in this output, and front-loaded so it stays meaningful truncated to ~30 characters. The title names the practice; every evidenced qualifier stays in `statement` (Statement fidelity applies there).
- `claude_md_rule`: ground in `P*` (a stated standing preference) and/or `F*` (friction the rule would prevent). Cluster near-identical standing preferences across sessions into one rule, citing the cluster exhaustively — every `P*` that states the practice and every `F*` the rule would have prevented (`P*`/`F*` only in `evidence_ids` here); an undercited rule understates its real support. Every recurring standing-preference cluster surfaces as a recommendation — do not drop a recurring preference because friction themes loom larger.
- `hook`/`setting`: when a friction item shows a defect that shipped or was committed and a mechanical guard would have blocked it, recommend the guard — a single incident is enough evidence; recurrence is not required for prevention.
- Link each recommendation to its theme(s) via `theme_refs` (0-based index into `themes`).
- `statement` is a concrete, actionable rule/action — no evidence counts, but keep every qualifier the evidence states (see Statement fidelity).
- A `retyped_directives`/`retyped_kickoffs` signal's recommendation names the recurring directive(s) from the signal's `detail` as promotion candidates — `new_skill` or `claude_md_rule` per what the evidence supports. One practice per recommendation still applies: separable rituals get separate recommendations, and evidence counts still never appear in the statement.

## Asset placement

- Every `claude_md_rule` recommendation sets `audience` — who must see the rule for it to bind: `user | orchestrator | subagents | both | automation`. `audience` is REQUIRED on `claude_md_rule` (the synthesis fails closed without it) and optional on other types.
- A rule violated in delegated/subagent-produced work while the orchestrator held the rule is a reach failure: state the propagation fix (inject the rule into implementer/reviewer subagent prompts), not just the rule itself.
- Reference kinds are unchanged by this section: `claude_md_rule` grounds in `P*`/`F*` only, and `G*` never appears in a recommendation's `evidence_ids`.
