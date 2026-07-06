# Workflow Insights Pipeline — Analysis Strategy

How this branch turns local Claude Code session transcripts into per-repo
workflow-improvement insights. Covers data processing, analysis, aggregation, and
insight extraction only (not rendering, scheduling, or TUI display). Written to
support comparison with other strategies (e.g. the built-in `/insights` command).

"Insight" means exactly one thing here: *how can the user improve their workflow with
Claude Code* — recurring friction, standing rules worth adopting, inefficient habits.
Not a cost/token dashboard, not praise.

## Core stance: deterministic Go brackets the LLM

Every stage assigns work by trust level. Go does everything that can be computed
deterministically (parsing, stats, counting, ranking, quote verification, privacy
redaction). The LLM does only what Go provably cannot: reading comprehension of a
session, and semantic clustering of free text (measured: friction one-liners,
stated preferences, and goals have ~0 textual duplicates, so no string matching can
cluster them). Three invariants follow:

1. **The LLM never emits a number.** No counts, rates, or "N sessions" anywhere in
   LLM output; Go recomputes all quantities from typed evidence-id sets and injects
   them at render. A regex tripwire flags quantitative claims as hard validation
   errors, and the renderer additionally redacts any that slip through.
2. **Every quote is verified verbatim or removed.** Substring-checked
   (exact → whitespace-normalized fallback, 12-rune minimum so a short quote can't
   trivially match) against the appropriate corpus. Non-verbatim quotes never
   survive as evidence.
3. **Empty is valid.** Zero friction, zero preferences, zero themes are correct
   answers, stated explicitly in both skills. Fabricating findings to fill arrays is
   treated as the worst possible error, and the evals measure it directly.

## Architecture: two layers, one durable pool between them

```
~/.claude/projects/**/<session>.jsonl              (raw transcripts, pruned ~30d)
        │
LAYER 1 — per session
  [Go]  defensive decode → AgentSessionStats (deterministic facts)
  [Go]  reduce transcript → token-budgeted "spine + prose" text
  [Go]  substantiality gate (skip trivial sessions)
  [LLM] analyzing-agent-sessions skill → judged fields (goal, outcome,
        friction incidents, standing preferences)
  [Go]  verbatim quote validation (asymmetric drop/flag policy)
        ▼
  AgentSessionAnalysis pool — flat, per-session JSON keyed by session-id;
  durable distillation that outlives transcript pruning; incremental via
  stamped transcript mtime
        │
LAYER 2 — per repo
  [Go]  group by repo root (worktree fold, rename aliases, volume floor)
  [Go]  aggregate → EvidenceBundle with typed ids (F/P/S/G) + inefficiency
        signals + context rollups, deterministic chronological order
  [LLM] synthesizing-workflow-insights skill → RawSynthesis (themes +
        recommendations, qualitative only, references evidence by id)
  [Go]  verify ids/partition → drop non-pool quotes → count → rank →
        already-adopted check
        ▼
  RepoSynthesis — ranked friction/opportunity themes + typed recommendations,
  every claim traceable: theme → typed id → incident → verbatim quote → session
```

Both LLM calls run as nested `claude -p <skill>` with schema-enforced structured
output, under subscription auth (API-key env vars scrubbed so billing can't
silently divert), with `--no-session-persistence` so analysis runs don't pollute
the very transcript corpus they read. Both skills are tmux-ctrl-agnostic (JSON in /
JSON out) and live outside the repo as personal skills; their schemas are embedded
in Go with drift-guard tests. Model locked to Opus 4.8 for both layers, chosen by
A/B eval (0 fabricated friction on clean sessions, 100% verbatim quotes, best
run-to-run stability; Haiku is the documented cheap fallback, Sonnet was
dominated).

## Layer 1 — per-session analysis

### Deterministic extraction (no LLM)

The transcript format is officially unstable, so the decoder parses defensively:
every field optional, never hard-fail a session, plus a **canary counter** for
unknown line types/fields so format drift surfaces loudly instead of silently
corrupting analyses. Empirically-forced correctness rules baked into the stats:

- **Line ≠ turn.** One assistant message spans multiple JSONL lines with duplicated
  `usage`; per-message stats (tokens, model mix, turn counts) dedup by `message.id`
  (~2.6–3.5× inflation otherwise); per-block stats (tool counts) stay per line.
- **`cache_read` is a peak, not a sum** (each turn re-reads the growing cached
  prefix; summing inflates 95–238×).
- **Friction is disambiguated from `is_error:true` by content:** rejection
  (canonical preamble) vs genuine tool error; an interrupt is never `is_error`.
  Markers are **prefix-anchored** (`HasPrefix(TrimSpace(body))`, never `Contains`) —
  corpus-validated that genuine markers sit at offset 0, so a body merely *quoting*
  a marker (e.g. Claude reading this pipeline's own source) can't fabricate friction.
- **Subagent fan-out = the `Agent` tool_use**; `task-notification` turns are
  subagent results, not user turns — counted separately.

Captured per session: repo/cwd/branch, wall-clock, model mix, token usage, per-tool
counts by name, tool errors / interrupts / rejections, edits/writes/lines/files,
subagent fan-out + types, skills and plugins *by name*, user-turn count, and
content-based **user-turn fingerprints** (ordered hashes of substantive user turns)
for cross-session resume/fork dedup.

### Reduction: the spine is sacred, prose is budgeted

The LLM never sees a raw transcript (median ≈128K tokens, max ≈2.8M). Go reduces
each session to a single text fed on stdin:

- **Spine — always kept verbatim, never budget-trimmed:** every real user turn,
  every tool error, every rejection (`[Rejected]`, with the user's stated reason
  when present), every interrupt (`[Interrupt]`), every inline subagent result
  (`[Subagent result]`). Friction lives almost entirely in the spine.
- **Assistant prose is budget-filled** (~160K chars) in priority order text >
  thinking > tool_use (tool calls collapse to name + key param); a footer states
  how many events were dropped.
- **Injected pseudo-user content is stripped** — skill bodies, IDE/context tags,
  `<system-reminder>`, command output, harness enforcement markers. Not stripping
  these misrepresents what the user actually said (a bug class found empirically).
- **Headed by the deterministic stats** (turns, errors, interrupts, top tools,
  duration) so the judge sees the facts before the narrative.

### Gate: analyze substantial sessions only

Stats are recorded for *every* session (cheap). The LLM runs when
`AssistantTurns ≥ 5` (per-message) **or** any friction signal exists (tool
error/interrupt/rejection) — a cost cut, not a quality guard (short sessions
analyze cleanly and return no friction; the friction override means a short but
frictionful session is never skipped).

### Judgment: the `analyzing-agent-sessions` skill

One session in, one `AgentSessionAnalysis` out: `underlying_goal` (inferred from
what the user asked and corrected, not what Claude did), `session_type`, `outcome`
(5-level, `unclear` allowed when subagent-heavy sessions hide the result),
`brief_summary`, plus the two evidence arrays:

- **`friction_incidents[]`** — discrete moments Claude's work cost the *user*
  (rework, rejection, unmet goal), typed against a fixed 6-value taxonomy
  (`wrong_approach`, `buggy_code`, `misunderstood_request`, `excessive_changes`,
  `user_rejected_action`, `incomplete`). An array of specific incidents, not one
  merged sentence — later solution-generation needs the granularity. Claude
  self-correcting quickly is *not* friction. Optional verbatim `evidence_quote`.
- **`standing_preferences[]`** — durable "how to work" rules the user stated,
  with a *required* verbatim quote of the user's own words. The core discriminator:
  a reusable rule about how to work ("don't add comments", "keep the diff small")
  vs a one-off task step ("rename this method") — only the former is captured, and
  a rule Claude *followed* still counts (it logs no friction, so this is the only
  channel that sees it).

Deliberately dropped from the official facet schema: praise axes
(`claude_helpfulness`, `user_satisfaction`, `primary_success`), freeform
`goal_categories`, and any judged "inefficiency" field (the deterministic stats
carry that signal; a free-text version invites fabrication).

### Post-judgment quote validation (asymmetric, fail-closed)

Two corpora are indexed from the decoded transcript: **user-authored prose** (real
user turns + rejection reasons after "the user said:") and the **full text**.
Policy honors each field's contract:

- Friction quote (optional) fails validation → quote cleared +
  `quote_unverified: true`, incident kept (a quoteless incident is still valid).
- Standing-preference quote (required, must be user's words, checked against the
  user corpus only) fails → the entire preference is dropped.

A non-verbatim quote never survives as verbatim, in either direction.

### The pool

One JSON per session, flat, keyed by globally-unique session-id (repo grouping is
deferred to synthesis time via the stored repo/cwd). Analyses are the durable
distillation — they outlive the ~30-day transcript pruning. Incremental behavior is
race-free: each analysis is stamped with the transcript mtime captured *before* the
LLM call, so a transcript appended mid-analysis gets re-processed next run.
Backfill is resumable ("done" = current analysis file exists) with a
consecutive-failure clean stop.

## Layer 2 — per-repo synthesis

### Grouping: repo root, not basename

Group key = the repo *root*: `basename(stats.repo)` with any `/.worktrees/<wt>`
suffix stripped; unmatched sessions derive the project from the path segment under
`~/Developer/<project>` (never `basename(cwd)`, which misfiles worktree leaves like
`.../repo/.worktrees/x/src` as a junk `src` repo), with rename aliases folding
pre-rename paths onto the current key. Buckets under a volume floor (default 10
analyses) are skipped — too thin to synthesize honestly.

### Aggregation: the EvidenceBundle and typed ids

Go flattens a repo's analyses into one bundle, sorted chronologically (start time,
session-id tiebreak — deterministic order is a golden invariant so ids map to the
same items across runs). Every item gets a **short typed id**:

- **F1..Fn** — friction incidents `{type, one_line, quote?, file (relativized),
  session_id}`.
- **P1..Pm** — standing preferences `{rule, quote, session_id}`.
- **S1..Sk** — success items from fully/mostly-achieved sessions `{goal, summary,
  session_type, Read count, skills, session_id}` — opportunity insights come from
  *successful* sessions' patterns, not just failures.
- **G1..Gj** — **Go-computed inefficiency signals**, one per kind, each with a
  magnitude (member-session count): `high_read` (Read count ≥ repo-relative p90 —
  re-deriving context every session), `friction_density`
  ((interrupts+rejections+errors)/turn ≥ p90), `unskilled_toil` (single-task +
  zero skills + high-Read). Emitted only when ≥3 sessions qualify. Deliberately
  *not* skill popularity — frequent skills are established workflows, the opposite
  of an opportunity.
- **Context rollups** (skill/session-type/tool-mix frequencies) — LLM context only,
  never a ranking oracle.

Typed ids are the mechanism that makes qualitative LLM output countable: Go counts
the exact referenced id sets and hard-errors on out-of-range, wrong-kind, or
partition-violating references. (The alternative — the LLM echoing session-id
lists — silently under/double-counts and was rejected.)

Privacy is handled at aggregation: file paths relativized to the repo, residual
home paths redacted, and the final artifact grep-scanned for home paths, UUIDs,
and ticket/branch patterns.

### Synthesis: the `synthesizing-workflow-insights` skill

One LLM call per repo (bundle on stdin, ~40K tokens at 159-session scale;
map-reduce is a contingency for quality, not size). Hard rules in the skill:

- Reference evidence **by id only**; never invent an id.
- **No numbers anywhere** in summaries or statements.
- Quotes copied **verbatim from bundle items** only.
- **Friction themes partition F** — each incident belongs to exactly one theme
  (its dominant one), so theme counts can't inflate by double-assignment.
- **Opportunity themes must anchor** on ≥1 `G` signal *or* ≥4 distinct `S` items
  (the "recurring clean workflow worth codifying" class fires no inefficiency
  signal); `F` ids allowed as corroboration only.
- **Recommendations are typed** — `claude_md_rule | new_skill | hook | setting |
  habit` — each with non-empty evidence ids and links to its themes. A
  `claude_md_rule` must cite `P` and/or `F` ids (preference-stated or
  friction-inferred; success-only grounding is rejected).
- Empty arrays valid; don't manufacture themes.

### Verification, counting, ranking (Go, mechanical)

- **Structural guards (hard errors, surfaced in the artifact, never silently
  dropped):** id in-range + kind-correct; F-partition holds; opportunity anchor
  rule holds; `claude_md_rule` evidence ⊂ P∪F; quantitative-claim tripwire on all
  prose. Orphan F-ids (in no theme) render as an explicit "unthemed friction (n)"
  residual — under-enumeration is made visible, not hidden.
- **Pool-sourced quote guard:** every cited quote must substring-match a pool
  evidence quote (transcripts prune, so the pool is the verification corpus); the
  **raw pre-guard drop-rate is recorded** as the fabrication metric (measuring
  post-guard would be tautological).
- **Counting/ranking:** incident/session counts recomputed from id sets. Friction
  themes rank by incident count normalized by analyzed-session count; opportunity
  themes by distinct corroborating session count; ranked within kind.
  Outcome-severity is deliberately *not* a primary signal (outcomes skew heavily
  to success, so it's near-constant). A friction theme spanning >2 friction types
  is flagged as an over-generalization candidate for human review.
- **Already-adopted check:** deterministic grep of the repo's and global
  CLAUDE.md/settings/skills for a majority of each recommendation's salient terms
  → `yes | no | unknown`, rendered as "already in place", never silently dropped.
  Exists because the naive version was falsified: an early probe's #1 tip was
  already in the user's CLAUDE.md.

Output is a dated `RepoSynthesis` snapshot (JSON is the source of truth; markdown
render is derived). Dated snapshots lay the groundwork for trends as exact Go
diffs of *normalized* friction rates — never LLM-judged, never raw counts.

## Quality gates (how the pipeline earned trust)

Each layer shipped only after passing a two-tier eval on real sessions:

- **Tier 1 — automatic, fail-closed on empty output.** Deterministically stratified
  session curation (friction × shape × length, meta sessions excluded, extra
  repeats on the dangerous zero-friction direction); schema compliance; **RAW
  pre-guard fabrication rate** (0.000 at both gates); verbatim-quote validity;
  run-to-run stability (outcome jumps, friction-type Jaccard; theme
  membership-churn via session-set overlap at Layer 2); **recall probes symmetric
  to the precision axes** (a frictionful session reporting zero friction, a
  high-magnitude G signal with no referencing theme, prefs present but no
  claude_md_rule) so the pipeline can't pass by under-reporting; paired
  positive/negative seeds through the real already-adopted matcher; privacy scan.
- **Tier 2 — human recognition, not recall.** Evidence-anchored cards (claim +
  verified quote + Go match/total counts — no transcripts, no session-ids) for the
  contested set only; the human adjudicates "is this real / adoptable", while
  representativeness leans on the auto probes.

Honest, stated asymmetries: semantic mis-grouping (real quotes, wrong theme) has
no full auto-catch (the >2-type smell + Tier 2 are the guard); clean-workflow
opportunities have no Go recall oracle (goals are 100% textually distinct);
branch-chain fingerprints are captured but resume/fork dedup is not yet applied at
synthesis; work inside separate subagent transcripts is invisible (the judge
correctly returns `unclear` there).

## Comparison: built-in `/insights`

The pipeline deliberately steals the official two-layer shape (per-session facets →
LLM synthesis), the friction framing, and the CLAUDE.md-rule idea, then diverges
where the official design is weakest for this goal:

| Axis | Built-in `/insights` | This pipeline |
|---|---|---|
| Scope | Global, all projects mixed | Per-repo (worktree/rename-aware grouping), recommendations land where they apply |
| Coverage | LLM-faceted 50 of 198 sessions, size-biased toward long ones | Every substantial session (turns ≥5 *or* any friction), short/zero-friction sessions proven to analyze cleanly |
| Friction detail | One merged `friction_detail` sentence | Typed incident array with per-incident evidence — granular enough to generate solutions from |
| Numbers | LLM-authored (observed mis-citing exact stats) | Go-computed from typed id sets; LLM numerically silent; tripwire + render redaction |
| Quotes | Unverified | Verbatim-verified against transcript (L1) and pool (L2); fail-closed; fabrication measured raw |
| Standing preferences | Not captured | First-class, user-verbatim-quoted; the direct CLAUDE.md-rule feed; catches rules Claude followed (invisible to friction) |
| Redundancy | Recommends rules already adopted | Deterministic already-adopted check against repo + global config |
| Lifecycle | One-shot report; artifacts die with transcripts | Durable incremental pool outliving the 30-day pruning; dated snapshots enable trends |
| Content | Praise, "impressive things", horizon speculation | Improvement-only by design |
| Output | HTML report | Typed JSON artifact (render derived from it) |

Against an ad-hoc "big-context LLM reads batches of transcripts" approach (the
`fable-insights-strategy.md` playbook in this directory), the pipeline trades the
flexibility of a hand-driven analysis for repeatability: fixed schemas, mechanical
verification, measured fabrication/recall, incremental accumulation, and no reliance
on a human orchestrating dedup and verification in-context.
