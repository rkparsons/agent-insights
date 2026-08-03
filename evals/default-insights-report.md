# Claude Code built-in `/insights` report (default)

_Source: ~/.claude/usage-data/report.html · generated 2026-06-29 · global across all projects._

---
Claude Code Insights 
- # Claude Code Insights

1,376 messages across 174 sessions (316 total) | 2026-06-09 to 2026-06-28 

At a Glance 

What's working: You run a rigorous, verification-first debugging practice—pushing Claude past first hypotheses to verified root causes, demanding failing red tests, and even commissioning oracle diagnostic tools for the gnarliest rendering bugs. Your version-control discipline stands out too: reviewing PR findings one at a time, consolidating redundant tests, and keeping diffs clean. When a hard problem can't be cracked in one pass, you smartly capture handoff docs so the next session picks up where the last left off. Impressive Things You Did → 
What's hindering you: On Claude's side, the biggest recurring issue is committing to a root cause or shipping a fix before faithfully reproducing the failure—headless 'proofs' passed while the live app stayed broken, and one autopilot emulator swap introduced a freeze. There's also a tendency to add more than asked: extra comments, vendored files, or new tests instead of extending existing ones. On your side, friction often came from approach and workspace ambiguity—edits landing in the wrong worktree, or scope/diff-strategy assumptions that needed an interrupt to redirect. Where Things Go Wrong → 
Quick wins to try: A CLAUDE.md with standing rules ('no comments unless asked', 'minimal diff', 'extend existing tests over adding new ones') would eliminate the repeated correction cycles you keep running. Since you already produce handoff docs, consider promoting them into a custom Skill so resuming a hard bug becomes a single command with consistent structure. And try dispatching Task Agents to explore competing root-cause theories in parallel rather than chasing them one at a time. Features to Try → 
Ambitious workflows: As models improve, expect reproduction-first bug hunting to become autonomous: an agent that refuses to attempt a fix until it has built a deterministic oracle that reproduces the exact artifact, then iterates against that red test and self-escalates when a fix makes things worse—collapsing your multi-session handoff marathons into single loops. You'll also be able to run parallel worktree agents, each chasing a distinct hypothesis in isolation and reporting back with evidence so you just pick the winner. And your PR review loop can become nearly hands-off: ingest every finding, classify fix-vs-skip with reasoning, wire regression tests into existing happy paths, and open a clean PR—all while respecting your standing conventions. On the Horizon → 

What You Work On 
How You Use CC 
Impressive Things 
Where Things Go Wrong 
Features to Try 
New Usage Patterns 
On the Horizon 
Team Feedback 

1,376 Messages 
+70,858/-4,850 Lines 
754 Files 
20 Days 
68.8 Msgs/Day 

## What You Work On

TUI Preview Rendering & Corruption Debugging 
~12 sessions 

Extensive deep-debugging work on a Go terminal UI's preview pane, tackling display corruption, flickering, freezes, and stale-text artifacts. Claude root-caused complex issues like OSC title sequences breaking the ANSI parser, tmux grapheme-width mismatches, and bubbletea scroll optimizations, often using TDD and oracle diagnostic tools. Several sessions ended in handoff docs as fixes proved elusive across emulator swaps and scroll-back paths. 

TUI Visual Design & Styling 
~8 sessions 

Iterative refinement of the terminal UI's appearance including section header gradients, faded underline rules, selection padding, header logo mockups, and applying diagonal gradients to preview borders. Claude rendered accurate visual mockups at each step and backed changes with test updates and clean commits. Work was highly collaborative with frequent user feedback on tint, fade, and tilt direction. 

Backend Auth & API Refactoring 
~9 sessions 

Work on TypeScript backend-api routers and shared backend auth middleware, including enforcing required token auth, removing anonymous handling, fixing 401 errors, and deduplicating boilerplate. Claude traced auth consumers across the codebase, refactored shared utilities, and caught subtle bugs like async-rejection hangs. Sessions typically ended with verified fixes, passing tests, and committed changes. 

PR Review, Test Cleanup & Version Control 
~10 sessions 

Reviewing bot PR comments and findings one at a time, resolving valid issues with regression tests, and trimming PR bloat by consolidating redundant test files (removing hundreds of lines). Claude also handled rebases, conflict resolution, draft PR creation via merge-base scoping, and CI failure diagnosis. Frequent use of the draft-pr skill and careful verification of test/lint/typecheck status. 

Codebase Understanding & Strategic Guidance 
~6 sessions 

Explaining repo architecture and rendering pipelines, providing build-vs-buy strategic analysis on the agent framework, and researching Claude Code insights tooling. Claude also handled cross-repo operations like a full project rename across disk, dotfiles, and live processes, plus merging GitHub accounts with symlink auditing. These sessions emphasized research, grounded explanations, and safe migrations. 

What You Wanted 

Debugging 

11 

Bug Fix 

9 

Version Control 

8 

Documentation 

7 

Code Review 

6 

Feature Implementation 

5 

Top Tools Used 

Bash 

3973 

Read 

1814 

Edit 

1367 

Agent 

823 

Write 

415 

TaskUpdate 

355 

Languages 

Go 

1795 

Markdown 

816 

TypeScript 

721 

HTML 

83 

Shell 

31 

JSON 

27 

Session Types 

Single Task 

17 

Multi Task 

13 

Iterative Refinement 

9 

Exploration 

8 

## How You Use Claude Code

You operate with a strong engineering discipline and treat Claude as a capable but supervised collaborator. Your work is heavily concentrated in deep debugging and root-cause analysis— you don't just want a bug patched, you want the actual cause proven , often demanding a failing red test before any fix is accepted (e.g., the preview rendering corruption saga, where you pushed through multiple wrong hypotheses until midterm emulator width-blindness was isolated). You lean on Claude's autonomy for the hard investigative grind, frequently letting it run subagents, build oracle diagnostic tools, and produce handoff docs for fresh sessions when a problem outlasts a single context window. The sheer scale here—3,973 Bash calls, 823 Agent invocations, and a near-even split of debugging, version control, and code review goals—shows you treat Claude as an end-to-end workflow partner, not a snippet generator. 
Your interaction style is iterative and correction-driven rather than spec-heavy upfront . You let Claude propose an approach, then steer hard when it drifts: you interrupted to redirect Claude toward editing a shared util directly instead of adding a flag, caught it making edits in the wrong worktree, and reframed investigations that fixated on the wrong code path. Visual and stylistic work (header gradients, faded underlines, logo mockups) is especially iterative—you refine in tight loops with rendered mockups at each step rather than describing the final state in one shot. You also expect Claude to *commit and push* as part of the task, treating clean version control as a non-negotiable part of done. 
A recurring source of friction is Claude's tendency to over-produce and over-claim : you repeatedly had to strip out unwanted inline comments ('Don't add comments'), prune vendored chrome and discovery docs, and reject premature 'proven' verification when headless tests didn't reproduce real-world races. Your dissatisfaction clusters around 'wrong_approach' (13 instances)—Claude confidently going down brittle live-tmux paths or shipping a large emulator swap on autopilot built on unverified diagnosis. You tolerate Claude being wrong as long as it course-corrects rigorously, and your high satisfaction overall reflects that the deep debugging payoffs usually justify the supervision overhead. 
Key pattern: You delegate deep, autonomous root-cause debugging to Claude but supervise tightly—demanding proven diagnoses with failing tests, redirecting wrong approaches mid-flight, and pruning Claude's tendency to over-produce and over-claim. 

User Response Time Distribution 

2-10s 

36 

10-30s 

100 

30s-1m 

95 

1-2m 

142 

2-5m 

181 

5-15m 

128 

>15m 

57 

Median: 118.4s • Average: 298.2s

Multi-Clauding (Parallel Sessions) 

45 
Overlap Events 

57 
Sessions Involved 

17% 
Of Messages 

You run multiple Claude Code sessions simultaneously. Multi-clauding is detected when sessions
overlap in time, suggesting parallel workflows.

User Messages by Time of Day

PT (UTC-8) 
ET (UTC-5) 
London (UTC) 
CET (UTC+1) 
Tokyo (UTC+9) 
Custom offset... 

Morning (6-12) 

335 

Afternoon (12-18) 

716 

Evening (18-24) 

325 

Night (0-6) 

0 

Tool Errors Encountered 

Command Failed 

110 

Other 

85 

User Rejected 

64 

Edit Failed 

19 

File Changed 

9 

File Not Found 

7 

## Impressive Things You Did

Over 174 sessions you've leaned on Claude for deep debugging, TUI/rendering work in Go, and rigorous PR hygiene across a tmux-control and backend-api codebase. 

TDD-driven root cause debugging 
You consistently push Claude past surface symptoms to verified root causes, demanding failing red tests before fixes. For tricky rendering bugs you built oracle diagnostic tools and ran midterm-vs-x/vt repros to prove the real cause (OSC title sequences, grapheme-width mismatches) rather than accepting the first hypothesis. This discipline turned 22 sessions into solid 'good_debugging' wins. 

Handoff docs for hard problems 
When a gnarly bug can't be solved in one pass, you have Claude produce clean handoff documents that carry diagnosis forward into fresh sessions. This chaining across sessions let you steadily narrow the preview-corruption problem—ruling out width tables, truncation, and scroll paths—while preserving context and avoiding wasted re-investigation. 

Disciplined PR review and cleanup 
You run a tight version-control workflow: reviewing bot PR comments one at a time, deciding which findings to fix versus skip, deduplicating logic across multiple files, and folding redundant tests into existing ones (removing 557 lines with full test/lint/typecheck verification). You also lean on the draft-pr skill, which correctly handles polluted diffs via merge-base comparison for clean PRs. 

What Helped Most (Claude's Capabilities) 

Good Debugging 

22 

Good Explanations 

7 

Correct Code Edits 

7 

Multi-file Changes 

6 

Proactive Help 

3 

Fast/Accurate Search 

2 

Outcomes 

Not Achieved 

2 

Partially Achieved 

5 

Mostly Achieved 

11 

Fully Achieved 

27 

Unclear 

2 

## Where Things Go Wrong

Your work is generally successful, but recurring friction stems from Claude pursuing premature or unverified diagnoses, over-expanding scope with unwanted changes, and occasionally misreading the intended approach before correction. 

Premature or Unverified Root-Cause Diagnoses 
Claude frequently committed to a root cause or fix without faithfully reproducing the problem, leading to long brittle investigations and shipped changes that failed in real-world testing. You can reduce this by asking Claude to build a minimal faithful reproduction (a unit-level oracle) before proposing or shipping a fix. 
Claude shipped a large x/vt emulator swap on autopilot based on a prior session's unverified diagnosis, introducing an io.Pipe deadlock freeze and failing to fix the original corruption (not_achieved).
- Claude repeatedly mislabeled scroll-back screenshots as 'live' output and chased brittle live-tmux investigations with a false-positive send-keys test, missing that the artifact was on the untested scroll-back path until a late reframe.

Unwanted Scope Expansion and Extra Edits

Claude often added more than requested—extra comments, vendored chrome, production-code changes, or new test files—that you then had to ask it to prune or remove. Stating up-front constraints like 'no comments' or 'minimal diff' would head off these correction cycles.

- Claude added inline comments you found unnecessary on multiple occasions, requiring you to say 'Don't add comments' and have them removed, costing back-and-forth turns.
- An implementer subagent over-expanded scope into production code to fix a test race, and Claude vendored extra repo chrome and a discovery doc you had to ask it to delete.

Misreading Intended Approach and Workspace

Claude sometimes pursued the wrong approach or environment—editing the wrong worktree, adding new tests instead of extending existing ones, or misjudging diff scope—requiring you to interrupt and redirect. Confirming the target worktree and the intended change strategy before edits would prevent rework.

- Claude made all edits in the main worktree instead of the intended feature worktree, forcing you to catch it and Claude to migrate the changes afterward.
- Claude started adding a new emission test instead of wiring the assertion into an existing happy-path test, and separately flipped the header gradient to mostly vertical when you wanted mostly horizontal, both requiring interruptions.

Primary Friction Types

Wrong Approach

13

Buggy Code

8

Misunderstood Request

5

Excessive Changes

5

User Rejected Action

3

Inferred Satisfaction (model-estimated)

Dissatisfied

10

Likely Satisfied

79

Satisfied

16

Happy

6

## Existing CC Features to Try

### Suggested CLAUDE.md Additions

Just copy this into Claude Code to add it to your CLAUDE.md.

Copy All Checked 

Do NOT add inline comments to code unless explicitly requested. 
Copy 

Across multiple sessions the user had to tell Claude to remove unwanted comments it added ('Don't add comments').

Always verify the actual root cause with a faithful unit-level reproduction (a failing red test) before attempting a fix; never build a fix on an unverified diagnosis from a prior session. 
Copy 

Several preview-rendering sessions failed because fixes were applied on top of wrong or unverified root-cause hypotheses, sometimes making things worse.

Confirm which worktree/branch is active before making edits; never edit the main worktree when a feature worktree is intended. 
Copy 

Claude made all edits in the main worktree instead of the intended feature worktree, requiring manual migration.

When verifying a UI/TUI fix headlessly, ensure the test reproduces the real-world failure mode (e.g. blank-frame race, live render) before declaring it proven. 
Copy 

Multiple headless verifications were declared successful but failed in the live app because the test didn't reproduce the real race.

When asked about test coverage or removals across a PR, audit ALL touched test files, not just newly added ones. 
Copy 

Claude repeatedly checked only newly added test files until the user clarified tests were spread across the whole PR.

Just copy this into Claude Code and it'll set it up for you.

Custom Skills

Reusable single-command workflows defined as markdown.

Why for you: You already use a draft-pr skill successfully and do tons of commit/review/PR-bot-comment work; codifying these recurring flows reduces repetition and misunderstandings.

Create .claude/skills/resolve-bot-comments/SKILL.md with: 'Fetch PR bot comments, assess each as blocker/valid/skip one at a time, fix valid ones with a regression test, and commit.' 
Copy 

Hooks

Shell commands that auto-run at lifecycle events.

Why for you: You hit buggy_code and build errors in Go/TS repeatedly; auto-running gofmt/go build/tsc and stripping that you don't want stray comments enforces quality automatically.

// .claude/settings.json
{
"hooks": {
"PostToolUse": [{
"matcher": "Edit|Write",
"hooks": [{"type": "command", "command": "gofmt -w . && go build ./... 2>&1 | head"}]
}]
}
} 
Copy 

Task Agents

Focused subagents for exploration and parallel work.

Why for you: Your hardest sessions were deep root-cause hunts (preview corruption, race conditions) where a dedicated exploration agent could isolate reproductions without polluting main context.

use an agent to build a minimal unit-level reproduction of the preview corruption bug before we attempt any fix 
Copy 

## New Ways to Use Claude Code

Just copy this into Claude Code and it'll walk you through it.

Verify before you fix

Lock in a verified, reproducible root cause with a failing test before writing any fix.

Your two not_achieved sessions and several partial ones came from acting on unverified diagnoses—an emulator swap on autopilot caused a deadlock, and headless 'proofs' failed live. Your best sessions (cursor overflow, 401 auth) started from a confirmed root cause. Make 'reproduce first' a hard gate.

Paste into Claude Code:

Before fixing anything, write a failing unit test that reproduces this bug at the lowest faithful level. Do not propose a fix until the test reliably fails for the right reason. 
Copy 

Write handoff docs as resumable specs

You already produce handoff docs—make them executable starting points for fresh sessions.

Multiple preview-bug sessions ended with handoff docs, and the next session sometimes inherited a wrong framing (live path vs scroll-back path). Have each handoff explicitly list the verified facts, ruled-out hypotheses, and the single next experiment to run so the reframe happens immediately.

Paste into Claude Code:

Write a handoff doc with three sections: VERIFIED FACTS (with repro commands), RULED OUT (with evidence), and NEXT EXPERIMENT (one concrete step). Flag any assumption that hasn't been independently verified. 
Copy 

Stop minor friction with style guardrails

Eliminate the repeated 'remove the comments' and 'wrong gradient direction' corrections via a CLAUDE.md and clearer up-front constraints.

You repeatedly had to remove unwanted inline comments and correct misread directional instructions (horizontal vs vertical gradient). These are cheap-to-prevent friction points. Codify no-comments and ask Claude to restate visual/directional intent before implementing.

Paste into Claude Code:

Before implementing this visual change, restate in one sentence exactly what you understand (direction, magnitude, scope), and do not add any inline code comments. 
Copy 

## On the Horizon

Your work has evolved from single-edit assists into autonomous, test-driven debugging campaigns spanning hundreds of commits, deep terminal-rendering forensics, and multi-agent PR workflows—the next frontier is making those investigations self-verifying and parallel.

Faithful Reproduction-First Bug Hunting

Your hardest sessions (preview corruption, OSC sequences, scroll-back artifacts) repeatedly burned cycles because fixes were validated against tests that didn't reproduce the real failure path—headless 'proofs' passed while the live app stayed broken. Imagine an agent that refuses to attempt a fix until it has built an oracle/diff harness that deterministically reproduces the exact artifact, then iterates fixes against that red test until green, escalating to alternative hypotheses automatically when a fix makes things worse. This turns multi-session handoff marathons into single autonomous loops that can't fool themselves.

Getting started: Use a TDD subagent with Bash-driven repro scripts (tmux capture-pane, golden-file diffing) and an explicit gate that blocks any Edit until a failing reproduction exists.

Paste into Claude Code:
Before changing any code, build a deterministic reproduction harness for this rendering bug: capture the actual corrupted output (e.g. via tmux capture-pane or an x/vt snapshot) and write a failing test that asserts against the real artifact, not a synthetic approximation. Do NOT propose a fix until the test reliably reproduces the failure. Then loop: hypothesize root cause, apply the smallest fix, re-run the harness, and if the artifact worsens or persists, automatically revert and try the next hypothesis. Maintain a running log of refuted hypotheses with evidence. Only declare success when the harness is green AND you've described why a live-app run would also pass. Copy 

Parallel Worktree Agents Per Investigation

Several sessions stalled on competing root-cause theories (midterm vs x/vt width-blindness, scroll-back vs live path, sync-output vs fork fix) explored one-at-a-time across exhausting handoff docs—and you once lost time when edits landed in the wrong worktree. Picture dispatching independent agents into isolated git worktrees, each pursuing a distinct hypothesis or fix strategy simultaneously, then reporting back with reproducible evidence so you pick the winner. This collapses days of serial dead-ends into a single parallel bake-off with zero cross-contamination.

Getting started: Use the Agent tool to spawn subagents bound to separate git worktrees, each with its own repro harness and a structured verdict (confirmed/refuted + evidence).

Paste into Claude Code:
Create three separate git worktrees and dispatch one subagent into each to independently investigate this bug under a different hypothesis (I'll list them). Each agent must work ONLY in its assigned worktree, build its own reproduction, attempt a fix, run the full test/lint/typecheck suite, and return a structured verdict: hypothesis confirmed or refuted, the diff, and the evidence. Do not let any agent touch the main worktree. When all three report back, summarize the comparison and recommend which fix to merge, including any that should be combined. Copy 

Autonomous PR Review-to-Resolution Pipeline

You routinely review bot/Claude PR comments, fix valid findings with regression tests, skip invalid ones, dedupe logic across files, and create draft PRs via skills—but it's still a guided, one-finding-at-a-time dance with occasional unwanted comments and scope drift. Imagine a pipeline that ingests every PR finding, classifies each as fix/skip with reasoning, implements fixes with regression tests wired into existing happy-path tests, runs full verification, and opens the cleaned-up PR—respecting your standing rules (no extraneous comments, merge-base diffing, minimal scope). This makes your code-review loop nearly hands-off while staying faithful to your conventions.

Getting started: Combine your draft-pr Skill with the gh CLI via Bash and a review subagent that outputs a per-finding decision table before touching code.

Paste into Claude Code:
Pull all open review comments on this PR via gh and produce a decision table: for each finding, classify as FIX or SKIP with a one-line justification and severity. For every FIX, add the assertion into the most relevant existing test rather than creating new test files, implement the minimal change, and add NO explanatory comments. After all fixes, run the full test/lint/typecheck suite, scope the diff against the merge-base (not a two-dot diff), and update the PR. Stop and ask me only if a finding requires a design decision; otherwise complete the whole pipeline and give me a final summary of what was fixed, skipped, and why. Copy 

"The preview rendering bug that wouldn't die: a multi-session saga where every fix made things worse, emulators got swapped wholesale, and Claude kept mistaking screenshots of scroll-back for 'live' output"

Across nearly a dozen sessions on the tmux-ctrl project, a single preview pane corruption bug became an epic debugging battle. Claude built an 'oracle diagnostic tool,' refuted its own hypotheses, swapped out an entire terminal emulator (x/vt) only to introduce a freeze from an undrained io.Pipe deadlock, and repeatedly chased brittle live-tmux investigations before finally tracing the real culprits to OSC title sequences and tmux grapheme-width mismatches. The journey produced multiple handoff docs explicitly written for 'a fresh session' to take over.
