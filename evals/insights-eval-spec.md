# Insights-pipeline eval spec — classification layer

_Consolidates `caught-insights.md` (22 the pipeline produced) + `missed-insights.md` (6 it didn't) into one source set for the eval suite. Classifications and nuance only — implementation (fixtures, scoring, harness) is a later phase. Derived from a manual diff of the custom pipeline (`insights-pipeline-report.md`) against fable (`fable-insights-report.md`) and Claude's built-in `/insights` (`default-insights-report.md`). 2026-07-03._

## The two eval arms

- **Part A — CAUGHT (regression evals).** The pipeline currently produces these. Eval intent: it must keep producing them, at the stated desired outcome. Failure = regression.
- **Part B — MISSED (gap/target evals).** The pipeline currently does **not** produce these; the next-phase refinement should make it. Eval intent: success = these start appearing. Until then they fail by design.

**Corroboration = eval confidence** (independent detection across sources): **[P+F+D]** all three → highest-confidence anchor · **[P+D]/[P+F]** two → strong · **[P]** pipeline-only → unique strength *or* unvalidated. Sources: **P**ipeline, **F**able, **D**efault.

**Tiers:** HIGH = strong evidence, clean/testable · MODERATE(-HIGH) = thinner evidence or nuanced.

**Totals:** 28 insights — 22 caught (HIGH 19 · MOD-HIGH 1 · MED 2), 6 missed (HIGH 5 · MOD-HIGH 1). Cross-source across the full set: [P+F+D] ×6.

---

## Why the misses happen — two structural pipeline blind spots

Not individual evals — the root causes the gap evals (Part B) exist to test:

1. **Behavioral-theme altitude only.** The pipeline synthesizes qualitative themes from an LLM pass; it has no deterministic pass over raw tool-errors/prompts. → misses M3 (mechanical friction) and M4 (retyping cost), both of which are pure counting problems.
2. **Orchestrator-only reasoning.** Every recommendation is a rule aimed at the top-level agent; the pipeline never reasons about *where* a rule/workflow should live. → misses M2 (rules don't reach subagents) and M6 (skill-ify rituals), both of which are asset-placement problems.

---

## Part A — Caught insights (regression evals)

### HIGH · cross-corroborated anchors [P+F+D]
- **C-01 Verify diagnoses/claims against real evidence before asserting.** Search/logs/real data first; account for context already given; seek contradicting evidence.
- **C-02 No comments / self-documenting / minimal edit delta.** No unrequested comments; minimal delta on edits/reverts; concise answers.
- **C-07 Bot-comment triage one-at-a-time.** Find every source; validated fix/skip recommendation each; apply only endorsed.
- **C-10 Red test before any fix; debug systematically.** Deterministic repro before fixing; no hypothesis-hopping.
- **C-11 Confirm target worktree before edit/build/test.** Feature worktree not main; isolate parallel subagents; no destructive ops on the real store.
- **C-G Keep tests lean.** Extend nearest existing happy-path test; no assertions on library internals/constants/static strings; no near-duplicate blocks. (F: 8 sessions.)

### HIGH · strong [P+D]/[P+F]
- **C-03 AskUserQuestion restraint** [P+F]. Prose for open-ended/design; widget only for ≤4 discrete choices; lead with a recommendation.
- **C-05 Diff against fetched remote merge-base** [P+D]. Three-dot/merge-base against origin; never stale local or `--stat`.
- **C-06 Safe/reversible commands + rm/stash guard hook** [P+F]. No `rm -rf` of build outputs / `git stash` of the user's tree; guard bulk `rm` under `~/.claude/projects`.
- **C-08 Adversarial-spec-review skill** [P+F]. Opus/Fable subagent critiques a spec before impl; validate + fold findings; re-run after rewrites. (F: retyped 29 sessions — strongest skill signal.)
- **C-09 Live-terminal verification for TUI** [P+D]. Never "done" on headless tests + review alone; confirm in real terminal or say only the user can.
- **C-12 Draft-PR via merge-base** [P+D]. Scope via merge-base against remote; create only on request.
- **C-A Tight scope; no unrequested push/PR; leave branch** [P+D]. No incidental refactors/doc-bundling/memory-writes; don't push/PR unless asked.
- **C-B Read the actual implementation before proposing** [P+D]. Grep/read the existing helper + convention and reuse before proposing a new method/design question.
- **C-C Never silently drop/change stated inputs or existing behavior** [P+D]. Preserve specified inputs + conventions (padding, status color, state-aware labels); flag deviations.
- **C-F Re-verify the full requested surface before completion; refresh invalidated artifacts** [P+D]. Recheck coverage across the whole scope; update stale PR Testing steps/docs.

### HIGH · pipeline-only [P]
- **C-E Validate spec/scope before implementing; keep specs at design altitude** [P]. Sanity-check premises before coding; author at final-state altitude, no planning-level detail. (5 sessions.)
- **C-H Terminal-fidelity preview skill** [P] — **pipeline-unique strength.** Render candidate glyphs/colors/layouts in the real terminal (tmux/Ghostty) + screenshot; never decide from browser mockups or plain ANSI. Neither F nor D caught it despite D's heavy TUI-visual work.

### MODERATE
- **C-I Front-load understanding via summarizing Explore subagents; keep a codebase map** [P+D] — MODERATE-HIGH. Delegate broad reading up front on heavy work; persist a map instead of re-reading. (14-session workflow signal; 2-session habit.)
- **C-D Clean up as you go** [P+D+F] — MEDIUM. **Split:** C-D1 delete dead code/obsolete plumbing + throwaway docs/tests on feature land/removal; C-D2 never commit without running tests.
- **C-J Research leading tools before designing; avoid personal-cloud coupling** [P] — MEDIUM (low-priority). Prior-art + gold-standard pattern before design. Thinnest (3 sessions), bundled.

### Caught-but-flawed (highest-value evals — test judgment, not the blunt rule)
- **C-04 Match process weight to task** [P+D] — HIGH but flawed. Small/mechanical → implement directly; no ceremony for trivial work. ⚠️ Pipeline's anti-parallel-Explore-agent framing (F7) **over-generalizes** — collides with M5. The eval must gate on task difficulty, not reward the blanket rule.

---

## Part B — Missed insights (gap/target evals)

- **M1 Handoff-doc ritual** [F+D] — HIGH. End-of-session handoff doc in worktree root (residual bug / key context / next step; "point to docs, don't restate") + cleanup + kickoff prompt; promote to a `handoff` skill. Default's template: VERIFIED FACTS / RULED OUT / NEXT EXPERIMENT. Pipeline: zero mention. (F: 19 sessions.)
- **M2 House rules don't reach subagents (asset-placement)** [F] — HIGH. Rules (no comments, lean tests, in-scope) violated *inside* SDD subagent output because they live in memory/orchestrator, never injected into subagent prompts. Fix: inject into every implementer/reviewer prompt; ask "who needs this — orchestrator or every subagent?". Pipeline reports the symptoms, never the subagent-reach root cause. *(Tests structural blind spot #2.)*
- **M3 Mechanical/environmental friction class** [F] — HIGH. `cd src`/Go-module-root confusion (~27 fails/16+ sessions), Edit-before-Read (73), permission-prompt failures (23), stow-symlink edit refusals. Pipeline captures zero tool-mechanics friction. *(Tests structural blind spot #1.)*
- **M4 Retyping-cost metric** [F] — HIGH (methodology). Count near-verbatim retyped instruction clauses (adversarial review 29 · SDD 18 · autopilot 16 · "leave branch" 12 · "one at a time" 12) to rank/justify skill candidates. Pipeline reports per-theme session counts but never measures retyping. *(Tests structural blind spot #1; underpins M1/M6.)*
- **M5 Parallel agents for HARD debugging** [D+F] — MODERATE-HIGH. Dispatch parallel isolated worktree agents, one per root-cause hypothesis, returning structured verdicts — for gnarly multi-session debugging. Pipeline flags parallel Explore agents as friction (C-04/F7); the carve-out (difficulty-gated) is missing.
- **M6 Skill-ify recurring rituals with concrete templates** [F+D] — HIGH. Beyond M1: `feature-pipeline` skill; `pr-comments` skill with a FIX/SKIP+severity **decision table** produced before touching code; a **manual-test-plan** artifact when verification isn't scriptable. Pipeline names the workflows as "well-working" and stops short of "build a skill" or the artifact. *(Tests structural blind spot #2; evidenced by M4.)*

### Considered and excluded (not eval targets)
- **terminal-app out of scope** — terminal-app was renamed to tmux-ctrl; the pipeline's tmux-ctrl window already swept the renamed sessions. Not a coverage gap.
- **Auto-run gofmt/go build hook** — generic tooling advice, no measured friction; format already handled at commit time.
- **Concrete client-project repo-facts** (generated manifest.json regenerate-on-rebase; auto-named worktrees review-only vs `TICKET-NNNNN`; testing-guidelines as team doc) — low-frequency/repo-specific; memory-caliber not eval-caliber. Team-doc placement folds into M2.
- **Audit ALL touched test files** — the granular counterpart to C-F; folded into the paired caught/missed nuance below rather than a standalone eval.

---

## Cross-cutting nuances for eval design

- **Caught-but-flawed cases are the most discriminating evals.** C-04 (over-generalized anti-parallel rule) and the C-I↔C-04↔M5 cluster all encode a **difficulty gate** the pipeline states too bluntly. Reward the nuance (parallel/front-loading is right on *hard* work, wrong on *small* work), not the blanket rule.
- **Paired caught/missed measure granularity, not just presence.** C-F (caught: re-verify full surface, general) ↔ the *audit-ALL-touched-test-files* specific failure mode (considered but skipped in the missed pass; the granular counterpart). The pipeline caught the principle, missed the specific. Pairs test resolution depth.
- **Corroboration ≈ confidence.** The six [P+F+D] anchors (C-01, C-02, C-07, C-10, C-11, C-G) are the safest positive evals. The three [P]-only caught split into: one clear unique strength (C-H), one legit-but-skill-routed (C-E), one thin (C-J).
- **Splits — keep bundled behaviors separate as eval items.** C-D → C-D1 / C-D2; C-E → validate-before-implement vs. spec-altitude-when-authoring.
- **Resolution ≠ insight.** C-E routes to the adversarial-spec-review skill (C-08); C-G's fix is a doc placement; M2/M6 resolve via asset placement. An insight is a valid positive eval even when its ideal fix lives in an existing asset — don't conflate "did the pipeline surface it?" with "where should it be fixed?".
- **Boundary precision.** C-A must encode commit-ok-when-it's-the-task vs. PR/push-only-on-request (default expects commit+push) — a fuzzy eval lets a model "correct" in the wrong direction. Same for any rule with a legitimate exception.
- **Two blind spots, four gap evals.** M2/M6 test whether the refinement adds *placement reasoning*; M3/M4 test whether it adds a *deterministic counting pass*. Passing the four implies the structural fixes landed.
