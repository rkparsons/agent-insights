# Strategy: built-in `claude /insights` command

How the default Claude Code insights report is produced. **Not publicly documented** — official
docs carry only a one-liner ("Generate a report analyzing your Claude Code sessions, including
project areas, interaction patterns, and friction points",
https://code.claude.com/docs/en/commands). Everything below was extracted from embedded strings
in the local native binary: `~/.local/share/claude/versions/2.1.199`
(v2.1.199, build 2026-07-02, git sha `968b0c41`). Prompts quoted verbatim. Details may drift
between CLI versions.

## Architecture: 3 layers

1. **Deterministic metadata extraction (no LLM)** — parse every session JSONL into per-session
   stats (tools, tokens, errors, interrupts, response times). Cached per session.
2. **Per-session LLM "facet" extraction** — one LLM call per session producing a small structured
   JSON (goal, outcome, satisfaction, friction). Cached per session; ≤50 new per run.
3. **Aggregate LLM synthesis** — deterministic cross-session aggregation, then 7 parallel section
   prompts + 1 final at-a-glance prompt over a compact stats payload. Rendered to static HTML.

All LLM calls use the **Opus** model (`ANTHROPIC_DEFAULT_OPUS_MODEL` env override, else default
opus48), non-interactive, no tools, plain-prompt with regex `{...}` JSON extraction; failed
sections silently drop. Runs fully locally.

## Pipeline detail

### Session discovery + filters
- Scan all `~/.claude/projects/*/` transcript files, sort by mtime desc. No date-window filter —
  caps are count-based.
- **Self-exclusion**: skips transcripts whose first 5 user messages contain
  `RESPOND WITH ONLY A VALID JSON OBJECT` or `record_facets` (i.e. the insights pipeline's own
  meta-sessions).
- Dedup by session_id keeping the copy with more user messages, then longer duration.
- **Eligibility for analysis**: ≥2 user messages AND ≥1 min duration. Sessions whose facet has
  only `warmup_minimal` as goal category are excluded from all aggregates.

### Layer 1: metadata (cache `~/.claude/usage-data/session-meta/<id>.json`)
Recomputed when transcript mtime is newer than cache; ≤200 new + ≤200 stale refreshes per run,
processed in batches of 50. Per session extracts:
- tool call counts; language counts by file extension of `file_path` args
- git commits/pushes = Bash commands containing `git commit` / `git push`
- input/output tokens (summed from usage blocks)
- interrupts = user text containing `[Request interrupted by user`
- user response times = gap assistant→next user message, kept only if 2s–3600s
- tool errors (`is_error` results) + keyword-bucketed categories: Command Failed / User Rejected /
  Edit Failed / File Changed / File Too Large / File Not Found / Other
- flags: uses task agent, MCP (`mcp__` prefix), WebSearch, WebFetch
- lines added/removed (line-diff of Edit old/new strings; Write = full content lines),
  distinct files modified (Edit/Write paths)
- message hour-of-day list, user message timestamps, duration = file created→modified,
  user/assistant message counts, first_prompt, summary

### Layer 2: facets (cache `~/.claude/usage-data/facets/<id>.json`)
≤50 new facet extractions per run (newest eligible sessions first); facets accumulate across
runs via cache. Transcript is rendered compactly first: header (id/date/project/duration), user
text truncated to 500 chars, assistant text to 300, tool calls as `[Tool: name]`. If the render
exceeds 30,000 chars it's split into 25,000-char chunks, each summarized by a separate LLM call
(≤500 output tokens):

> Summarize this portion of a Claude Code session transcript. Focus on:
> 1. What the user asked for
> 2. What Claude did (tools used, files modified)
> 3. Any friction or issues
> 4. The outcome
>
> Keep it concise - 3-5 sentences. Preserve specific details like file names, error messages, and user feedback.

Facet extraction prompt (≤4096 output tokens), verbatim:

> Analyze this Claude Code session and extract structured facets.
>
> CRITICAL GUIDELINES:
>
> 1. **goal_categories**: Count ONLY what the USER explicitly asked for.
>    - DO NOT count Claude's autonomous codebase exploration
>    - DO NOT count work Claude decided to do on its own
>    - ONLY count when user says "can you...", "please...", "I need...", "let's..."
>
> 2. **user_satisfaction_counts**: Base ONLY on explicit user signals.
>    - "Yay!", "great!", "perfect!" → happy
>    - "thanks", "looks good", "that works" → satisfied
>    - "ok, now let's..." (continuing without complaint) → likely_satisfied
>    - "that's not right", "try again" → dissatisfied
>    - "this is broken", "I give up" → frustrated
>
> 3. **friction_counts**: Be specific about what went wrong.
>    - misunderstood_request: Claude interpreted incorrectly
>    - wrong_approach: Right goal, wrong solution method
>    - buggy_code: Code didn't work correctly
>    - user_rejected_action: User said no/stop to a tool call
>    - excessive_changes: Over-engineered or changed too much
>
> 4. If very short or just warmup, use warmup_minimal for goal_category

Required output schema:

```json
{
  "underlying_goal": "What the user fundamentally wanted to achieve",
  "goal_categories": {"category_name": "count"},
  "outcome": "fully_achieved|mostly_achieved|partially_achieved|not_achieved|unclear_from_transcript",
  "user_satisfaction_counts": {"level": "count"},
  "claude_helpfulness": "unhelpful|slightly_helpful|moderately_helpful|very_helpful|essential",
  "session_type": "single_task|multi_task|iterative_refinement|exploration|quick_question",
  "friction_counts": {"friction_type": "count"},
  "friction_detail": "One sentence describing friction or empty",
  "primary_success": "none|fast_accurate_search|correct_code_edits|good_explanations|proactive_help|multi_file_changes|good_debugging",
  "brief_summary": "One sentence: what user wanted and whether they got it"
}
```

Known goal-category labels (from the display map): debug_investigate, implement_feature, fix_bug,
write_script_tool, refactor_code, configure_system, create_pr_commit, analyze_data,
understand_codebase, write_tests, write_docs, deploy_infra, warmup_minimal. Extra friction labels
beyond the prompt's five: claude_got_blocked, user_stopped_early, wrong_file_or_location,
slow_or_verbose, tool_failed, user_unclear, external_issue.

### Deterministic aggregation
Sums/distributions over eligible sessions + facets: totals (messages, hours, tokens, commits,
pushes, interrupts, tool errors + categories, lines added/removed, files modified), top tools,
languages, projects, goal/outcome/satisfaction/helpfulness/session-type/friction/success
distributions, median+avg response time, days active, messages/day, hour-of-day histogram, and
**multi-clauding detection**: sliding 30-min window over user-message timestamps across sessions;
counts overlap events where the user messaged two different sessions interleaved.

### Layer 3: synthesis payload
Every section prompt receives the same `DATA:` blob:
- compact JSON: sessions, analyzed count, date_range, messages, hours, commits, **top 8 tools,
  top 8 goals**, outcomes, satisfaction, friction, success, languages
- `SESSION SUMMARIES:` ≤50 lines of `- {brief_summary} ({outcome}, {helpfulness})`
- `FRICTION DETAILS:` ≤20 lines of facet `friction_detail`
- `USER INSTRUCTIONS TO CLAUDE:` ≤15 items from optional facet field
  `user_instructions_to_claude` (not in current schema; "None captured" otherwise)

Note: raw transcripts never reach layer 3 — it sees one sentence per session plus histograms.

### Layer 3: section prompts (7 parallel, each ≤8192 output tokens)
1. **project_areas** — "identify project areas… Include 4-5 areas. Skip internal CC operations."
   `{areas: [{name, session_count, description (2-3 sentences)}]}`
2. **interaction_style** — "2-3 paragraphs analyzing HOW the user interacts… iterate quickly vs
   detailed upfront specs? Interrupt often or let Claude run? Include specific examples." plus
   `key_pattern` one-liner.
3. **what_works** — "identify what's working well… Include 3 impressive workflows."
   `{intro, impressive_workflows: [{title (3-6 words), description}]}`
4. **friction_analysis** — "identify friction points… Include 3 friction categories with 2
   examples each." `{intro, categories: [{category, description (+ what could be done
   differently), examples: [with consequence]}]}`
5. **suggestions** — includes a hardcoded "CC FEATURES REFERENCE" (MCP servers, custom skills,
   hooks, headless mode, task agents) to pick features_to_try from. Output:
   `claude_md_additions` ("PRIORITIZE instructions that appear MULTIPLE TIMES in the user data.
   If user told Claude the same thing in 2+ sessions… they shouldn't have to repeat themselves"),
   `features_to_try` (2-3 from reference, with copyable example_code), `usage_patterns`
   (with copyable_prompt).
6. **on_the_horizon** — "identify future opportunities… Include 3 opportunities. Think BIG -
   autonomous workflows, parallel agents, iterating against tests."
   `{intro, opportunities: [{title, whats_possible, how_to_try, copyable_prompt}]}`
7. **fun_ending** — "find a memorable moment… A memorable QUALITATIVE moment from the
   transcripts - not a statistic. Something human, funny, or surprising."

### Layer 3: at_a_glance (final, sequential)
Runs after the 7 sections; its prompt embeds the DATA blob plus bullet summaries of the section
outputs (project areas, big wins, friction categories, features to try, usage patterns,
horizon opportunities). Four-part structure, verbatim gist:

> 1. **What's working** - What is the user's unique style… Don't be fluffy or overly complimentary. Also, don't focus on the tool calls they use.
> 2. **What's hindering you** - Split into (a) Claude's fault (misunderstandings, wrong approaches, bugs) and (b) user-side friction (not providing enough context, environment issues…). Be honest but constructive.
> 3. **Quick wins to try** - Specific Claude Code features they could try… (Avoid stuff like "Ask Claude to confirm before taking actions" or "Type out more context up front" which are less compelling.)
> 4. **Ambitious workflows for better models** - As we move to much more capable models over the next 3-6 months, what should they prepare for?
>
> Keep each section to 2-3 not-too-long sentences. Don't overwhelm the user. Don't mention specific numerical stats or underlined_categories from the session data below. Use a coaching tone.

### Output
- Static HTML written to `~/.claude/usage-data/report-<timestamp>.html` + `report.html`
  (mode 0600): at-a-glance, project areas, interaction-style narrative, impressive workflows,
  friction categories, suggestions, horizon, fun ending; bar charts for tools/goals/outcomes/
  friction/satisfaction, response-time buckets (2-10s … >15m), time-of-day histogram,
  multi-clauding stats.
- Chat turn then instructs the interactive model to emit the `file://` report URL verbatim.
- Export JSON bundles metadata (username, CC version, date range, session counts), aggregated
  stats, all section outputs, facet summary distributions.

## Properties relevant for comparison

| Property | Default /insights |
|---|---|
| Corpus per run | ≤200 sessions metadata-refreshed, ≤50 new facets (cached cumulative) |
| Evidence granularity | 1 sentence/session + histograms reaches synthesis; no verbatim quotes, no session ids |
| Transcript fidelity | user 500 chars, assistant 300 chars, tools name-only; >30KB → lossy chunk summaries |
| Friction taxonomy | fixed 12-label enum, counted per session by one LLM pass |
| Verification | none — no re-read of raw transcripts, no dedup/adjudication of LLM claims |
| Numbers | deterministic layer only; synthesis told to avoid quoting stats |
| Model | Opus for all calls (chunk summary ≤500 tok, facet ≤4096, sections ≤8192) |
| Cross-session dedup | session_id dedup only; no semantic dedup of findings |
| Failure mode | section prompt fails → section silently missing |
