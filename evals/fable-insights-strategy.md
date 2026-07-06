# Playbook: Claude Code usage friction analysis

Repeatable workflow for mining ~30 days of Claude Code session transcripts for friction,
workflow-improvement opportunities, and Claude-asset (skill/memory/hook/settings) recommendations.
Built 2026-07-03. Companion reducer script: `usage-analysis-reduce_sessions.py` (same dir).

## Architecture: 3 layers, by trust level

1. **Deterministic reduction (script, no LLM)** — parse every `~/.claude/projects/*/*.jsonl`
   modified in the window into ~1KB/session summaries. Facts only: prompts, interrupts, denials,
   errors, tool/skill/command histograms. This layer never hallucinates and makes the corpus
   ~300× smaller (324MB → 1.1MB for 652 sessions).
2. **Fan-out extraction (cheap models)** — sonnet agents over repo-grouped chronological batches
   (~120KB each), classifying friction incidents against a fixed taxonomy, quoting verbatim
   evidence + session ids. They may grep raw transcripts for context but never read whole files.
3. **Synthesis + verification (top model, main context)** — cross-batch dedup, trend vs isolated
   judgment, recommendations. Verify load-bearing findings against raw transcripts before
   reporting. Only this layer makes decisions.

## Steps

1. **Scout** (5 min): count/size session files in window; sample one file's entry types.
   Formats drift between CLI versions — re-verify markers before trusting the reducer:
   - human prompt: `type=user`, not `isSidechain`, not `isMeta`, content str or text-blocks
   - interrupt: user text starting `[Request interrupted by user`
   - denial: tool_result starting `The user doesn't want to proceed`
   - slash command: `<command-name>X</command-name>` in user content
   - `entrypoint` field: `cli` = interactive, `sdk-cli` = programmatic (analyze separately!)
2. **Reduce**: run `usage-analysis-reduce_sessions.py` (edit DAYS/SELF_SESSION consts).
   Outputs `reduced/sessions.jsonl` + `reduced/aggregate.json`.
3. **Read aggregate in main context** — this alone surfaces the mechanical trends
   (top error snippets, permission failures, skill usage skew) and drives batch design.
4. **Inventory existing assets in parallel** (haiku agent): skills, memory dirs + MEMORY.md
   contents, hooks, settings, CLAUDE.mds. Needed so recommendations don't duplicate what exists —
   and so "rule exists in memory but violations continue" is itself detectable as a finding.
5. **Batch**: interactive sessions only, grouped by repo, chronological, split ~120KB.
   Filter to `n_prompts ≥ 1 or fscore > 2`. SDK sessions: aggregate stats only.
6. **Fan out** sonnet agents, one per batch, shared prompt file with:
   - the friction taxonomy (correction, repeat-instruction, tool-failure, permission,
     verification-gap, scope, context-gap, process-overhead, environment, rework)
   - strict evidence format: category + session id + date + verbatim quote ≤200 chars
   - explicit anti-noise rules: no praise, no style prose, skip unremarkable sessions,
     beware repos whose CODE quotes friction markers (transcript-tooling repos)
   - ask for REPEATED_INSTRUCTIONS and WORKFLOW_PATTERNS sections — cross-session repeats
     are the main signal separating trends from one-offs
7. **Synthesize (top model)**: cluster incidents across batches; a trend = same category+theme
   in ≥3 sessions or ≥2 repos; check trends against existing memory/CLAUDE.md (existing rule +
   continued violations ⇒ rule placement/wording problem, not a missing rule). For each surviving
   theme, pick the cheapest effective asset: settings/hook > CLAUDE.md > memory > skill.
8. **Verify before reporting** — deterministically where possible: regex-count the claimed
   repeated phrases/denials/tool-errors over the reduced corpus (exact session counts per repo
   beat agent impressions and cost nothing). Fall back to targeted raw-transcript grep only for
   claims regex can't capture (paraphrased intent). In the 2026-07 run every major trend
   survived this pass, several with higher counts than the agents estimated.
9. **Deliver**: recommendations ranked by (frequency × cost per occurrence × fixability).
   Actionable only — each item names the asset type, its content, and the evidence count.

## Traps learned

- Detection strings must match exact prefixes: substring matching hits code/tests that quote
  the markers (meta-contamination), especially in transcript-tooling repos.
- Exclude the current session's own file (it contains this playbook's markers).
- `sdk-cli` sessions swamp interactive ones by count (~50% here); keep them out of friction
  batches or corrections analysis is diluted by machine prompts.
- Giant lines (embedded images) — skip lines >4MB before json.loads for speed.
- Worktree dirs make one repo look like 50 projects; normalize via `cwd` field, not dir slug.
- Session-summary `title`/fscore alone are weak relevance signals; prompts are the payload.
- fscore/duration inflate on overnight-idle sessions and batched multi-edit rejections; treat
  as triage ordering only, never severity. Near-duplicate consecutive prompts at an interrupt
  timestamp are message-edit-and-resend artifacts, not corrections.
- Instruct batch agents to date every incident — memory/CLAUDE.md rules often already cover a
  behavior; only post-rule violations are findings (they indicate wrong asset placement).

## Cost profile (2026-07 run)

652 sessions / 324MB → 1.1MB reduced. 1 haiku inventory (~44k tokens) + 9 sonnet batch agents
(60-120k tokens each); verification was pure script (no verifier agents needed). Synthesis in
main context. Wall clock ~35 min. Output: this playbook + `usage-friction-report-<date>.md`.
