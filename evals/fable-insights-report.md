# Claude Code usage friction report — 2026-06-02 → 2026-07-03

Corpus: 652 sessions / 324MB across 8 repos; 331 interactive (client-project 179, terminal-app 113, tmux-ctrl 18, misc 21).
Method: deterministic reduction → 9 sonnet batch analyses → deterministic verification of every cross-batch claim
(counts below are exact regex/parser counts over real user prompts, not model estimates).
Companion playbook: `usage-friction-analysis.md`.

## Recommendations, ranked by impact

### 1. Build a `feature-pipeline` skill (dotfiles → ~/.claude/skills)
The single biggest retyping cost. Standing ritual currently stitched by hand every feature session:
brainstorm → spec → **adversarial Opus subagent review** → plan + self-review → SDD full-auto → leave branch for manual test.
Verified counts of near-verbatim retyped clauses:
- "assign an opus subagent to do a critical/adversarial review… no implementation-detail bloat… validate and incorporate": **29 sessions** (terminal-app 10, client-project 10, tmux-ctrl 9)
- "implement with subagent driven development": 18 sessions; "autopilot / without asking me / full-auto": 16; "leave the branch as-is for manual testing": 12

The skill should encode two fixes beyond deduplication:
- **Inject house rules into every implementer/reviewer subagent prompt** (self-documenting code, no near-duplicate tests — extend existing cases, stay in diff scope). Your global/memory rules exist but violations recur *inside SDD subagent output* (comment complaints: 7 sessions; test-bloat corrections: 8 sessions; the TICKET-0000 PR needed ~6 trailing sessions trimming out-of-scope refactors). Rules the orchestrator knows aren't reaching the subagents.
- **A verification stage before "done"**: build + install + smoke where scriptable; else emit your own manual-test-plan format ("input prompt to enter, success/failure outputs to match"). Evidence: recoverable-errors feature passed all pipeline reviews then failed first manual test (~2h re-diagnosis, 2026-07-03); overnight autopilot left terminal-app unusable, binary manually reverted (06-25); ultrakey nav shipped completely non-functional (06-19); `verify` skill invoked ~0×, `verification-before-completion` 1× in the month.

### 2. Global CLAUDE.md rule: stop reaching for AskUserQuestion in design discussion
**62 denials / 258 calls (24%) across 52 sessions in 3 repos** — the dominant denial type (next: Edit 14, Bash 14). You always answer in prose; sometimes type the option number as text instead. Suggested line for `~/.claude/CLAUDE.md`:
> During brainstorming/design discussion ask questions in plain prose. Only use AskUserQuestion for genuinely enumerable choices (≤4 fixed options, e.g. pick variant A/B/C); I answer nuanced questions free-form.

### 3. Build a `handoff` skill
Manual end-of-session ritual, **19 sessions** (tmux-ctrl 11, terminal-app 8): write a concise handoff doc in worktree root (residual bug, key context, next steps — "point to docs rather than restating"), clean up stale docs/test files, emit a kickoff prompt for the fresh session. Your exact repeated instructions define the spec; the 10-session terminal-app preview-corruption arc ran on this ritual.

### 4. Build a `pr-comments` skill (client-project-scoped)
Bot-comment triage is your most-repeated client-project routine: ~22 sessions/month, with "present them one at a time, not all at once, with your recommendation" verbatim in **12 sessions**. Encode: fetch unresolved bot/client-bot comments → one at a time with recommendation → fix/skip per your reply → never post PR comments/replies (existing memory rule) → extend existing tests rather than adding new ones. Also fold in the self-review variant: "review for blockers, gaps, false assumptions. No nits" (6 sessions) as the default review framing. `superpowers:receiving-code-review` sometimes fails to trigger on this task shape (2 confirmed misses).

### 5. Create CLAUDE.md in terminal-app and tmux-ctrl (neither has one)
Mechanical errors recur because repo facts live only in auto-memory (recall-dependent, invisible to subagents):
- `cd src` / module-root confusion: **~27 failures across 16+ sessions** in both repos (Go module root is `src/`, one level below worktree root; inverse `src/src/...` git-add failures too). A terminal-app memory entry for this exists — still failing, wrong asset type.
- terminal-app extras: `~/.config/terminal-app/config.yaml` is a stow symlink (3 Edit refusals — name the real dotfiles target); terminal-app is your live daily-driver terminal (stated twice: never assume idle repro env; state repro requirements explicitly).
- tmux-ctrl extras: wrap Claude-specific logic in skills, call from Go subcommands (your stated architecture rule); code/evals quote transcript friction markers — match exact prefixes when analyzing transcripts.

### 6. Global debugging rule: red test before fixes
You had to mandate this twice mid-arc (06-25): "get a reliable failing red test before implementing anything so we don't end up chasing the wrong lead" — after `systematic-debugging` sessions repeatedly chased unreproducible live screenshots (16h marathon session, "It feels a bit like you're stuck on a loop"). Suggested global CLAUDE.md line:
> Bug fixes: establish a reliable failing test before attempting any fix. If repro needs my live environment, tell me the exact steps and whether the app must stay frozen.

### 7. client-project testing-guidelines addendum (team PR)
8 sessions of the same corrections on prompts-package PRs (06-18→07-03): no static-string assertions on prompts; don't unit-test zod schemas/constants ("well tested library"); extend existing happy-path tests instead of near-duplicate new ones. Belongs in `docs/testing-guidelines.md` so review subagents and teammates' Claudes see it too. Personal memory as stopgap.

### 8. Two client-project memory entries
- **Worktree targeting**: auto-named worktrees (happy-stork, proud-mesa…) are review-only; fixes go in the ticket worktree (`sc-XXXXX`) — you had to redirect twice; default to asking which worktree when a fix emerges mid-review.
- **prompt-manifest.json rebases**: `packages/prompts/prompt-manifest.json` is generated (build runs `generate-manifest.ts`); on rebase conflict regenerate via `pnpm --filter @client-project/prompts build` and restage — never hand-merge. Hit in 2 sessions.

### 9. Hard-block `rm` against ~/.claude/projects (hook)
Near-miss 07-01: a tmux-ctrl session attempted a mass `rm` loop across your entire transcript history; only the auto-mode classifier caught it. One-line guard in your existing PreToolUse Bash hook chain: reject any `rm` whose args reference `~/.claude/projects`. Deterministic, cheap, protects the data all your insights tooling depends on.

### 10. Housekeeping (low effort)
- Run `/fewer-permission-prompts`: 23 Bash calls failed with "opus temporarily unavailable, auto mode cannot determine safety" — a wider explicit allowlist removes the auto-classifier dependency.
- Edit-before-Read errors (**73 occurrences**, clusters after `/clear` in PR-comment sessions and inside SDD implementers): mostly self-recovering 1-turn noise; the pipeline-skill subagent template (rec 1) is the only lever worth pulling.
- API 529/overload babysitting (8 manual "try again" over 3h, 06-23): platform-side; nothing to build.

## Trend vs isolated
Trends (≥3 sessions or ≥2 repos): recs 1–8 and Edit-before-Read. Isolated but severe: rm near-miss (rec 9).
Isolated, no action: zsh `mapfile` bashism, `Explore`/`TodoWrite` phantom-tool calls, Zed keymap analyzer rabbit hole,
OOM-killed vitest, one lost/stalled agents-v2 session.

## Meta-finding on asset placement
Recurring theme behind recs 1, 5, 7: **rules stored in auto-memory keep being violated where memory doesn't reach** —
subagent prompts and cold sessions. Durable repo facts and house style belong in checked-in CLAUDE.md/docs
(deterministic load, subagent-visible); memory is right for personal prefs and cross-session state. Worth applying
that test whenever you add a rule: "who needs this — just the orchestrator, or every subagent it spawns?"
