---
name: analyzing-agent-sessions
description: Use when given a reduced agent session transcript (Claude Code), on stdin or in the prompt, and asked to produce a structured per-session analysis. Emits an AgentSessionAnalysis JSON — underlying goal, session type, outcome, a one-line summary, discrete friction incidents, and user-stated standing preferences — for workflow-improvement analysis.
---

# analyzing-agent-sessions

You are analyzing **one** Claude Code session transcript to produce an `AgentSessionAnalysis`:
a compact, structured judgment of how the session went, for the purpose of helping the
user improve how they work with Claude Code.

The reduced transcript is provided as input (stdin or prompt). It contains the user's
turns verbatim, tool-error events, interrupts, and trimmed assistant turns, headed by
deterministic session stats. Analyze it and emit the analysis.

## Output

Emit a JSON object matching the provided schema. When invoked with `--json-schema`,
return only the structured object. Otherwise, respond with **only** a valid JSON object
and nothing else.

Fields:

- `underlying_goal` — what the user fundamentally wanted, in their terms. Infer from
  what they asked for and corrected, not from what Claude happened to do.
- `session_type` — `single_task` | `multi_task` | `iterative_refinement` |
  `exploration` | `quick_question`.
- `outcome` — did the user get what they wanted?
  - `fully_achieved` — goal met, no major caveats.
  - `mostly_achieved` — met with minor gaps.
  - `partially_achieved` — meaningful progress, meaningful gaps.
  - `not_achieved` — goal not met.
  - `unclear` — the transcript genuinely does not show the result. Use sparingly.
- `brief_summary` — one sentence: what the user wanted and whether they got it.
- `friction_incidents` — an array of discrete things that went wrong **for the user**.

## Friction incidents — the core judgment

A friction incident is a concrete moment where Claude's work created cost for the user:
a wrong approach, buggy output, a misread request, over-broad changes, a rejected action,
or leaving the task incomplete.

`type` is one of:
- `wrong_approach` — Claude pursued a wrong strategy/direction.
- `buggy_code` — Claude produced code that didn't work / had to be fixed.
- `misunderstood_request` — Claude misread what the user asked for.
- `excessive_changes` — Claude changed more than asked / over-engineered.
- `user_rejected_action` — the user rejected, reverted, or interrupted what Claude did.
- `incomplete` — Claude left the requested work unfinished.

For each incident provide `one_line` (the specific incident), and when an exact supporting
quote exists, `evidence_quote` — a short **verbatim substring copied from the transcript**
(typically a user complaint or correction). `file` if it concerns a specific path.

### Rules that matter most

1. **Do not fabricate friction.** Many good sessions have *zero* friction. If nothing
   genuinely went wrong for the user, return `"friction_incidents": []`. A smooth session
   is not a failure to find problems — it is the correct answer. Inventing friction to
   fill the array is the single worst error you can make here.
2. **Friction is from the user's side.** Claude self-correcting quickly, a tool error
   Claude immediately recovered from, or normal iteration the user was happy with are
   **not** friction. Friction is cost the *user* bore: rework, rejection, frustration,
   an unmet goal.
3. **`evidence_quote` must be verbatim.** Copy the substring exactly from the transcript.
   If you cannot find an exact supporting substring, omit `evidence_quote` rather than
   paraphrasing or inventing one.
4. **One incident per distinct problem.** Don't merge two different problems into one
   line; don't split one problem into many.
5. **Judge only what the transcript shows.** Subagent-heavy sessions may hide work; if the
   result is genuinely not visible, prefer `outcome: unclear` over guessing.

## Standing preferences — durable rules the user stated

`standing_preferences` captures rules the user expressed about **how** Claude should work —
working style, conventions, or process — that are **reusable across sessions**. These are
candidate standing rules (e.g. CLAUDE.md). For each: `rule` (phrased as a reusable rule) and
`evidence_quote` (a **verbatim substring of the user's own words** — required).

### The discriminator that matters most

Capture a preference **only** if it is a *reusable rule about how to work*, not a *task step
for this session*. This is the whole judgment:

- ✅ standing: "investigate how the existing code does it before asking me", "stay consistent
  with the existing conventions", "don't add comments when the code self-documents", "keep
  the diff small / scoped".
- ❌ one-off task instruction: "use a default export here", "rename this method", "add a
  guard to the batch op", "create a Shortcut ticket". These are *what to do now*, not *how to
  work generally* — do **not** capture them.

Rules:
1. **Empty array is the common, correct answer.** Most sessions state no standing preference.
   Do not manufacture one from ordinary task instructions — that is the worst error here, the
   same as fabricating friction.
2. **`evidence_quote` must be verbatim and must be the USER stating it** (not Claude
   inferring it). If you cannot quote the user, omit the preference.
3. A preference is worth capturing whether or not it caused friction — a rule the user stated
   and Claude *followed* is still a standing preference.
