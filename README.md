# agent-insights

Eval-gated LLM pipeline that mines your Claude Code sessions for workflow insights.

It reads the transcripts Claude Code already writes to `~/.claude/projects/`, distills
each session into a typed analysis, and synthesizes every repo's evidence in **one
cross-repo pass** into ranked findings, each carrying the cheapest asset that would
fix it — a CLAUDE.md rule, a repo doc, a skill, a hook, a setting, or a habit.
Improvement only: no praise, no token dashboard.

The design stance is that deterministic Go brackets the LLM at every stage:

- **The LLM never emits a number.** Counts come from Go recomputing over typed
  evidence-id sets; a regex tripwire fails the run on any quantitative claim in LLM prose.
- **Every quote is verified verbatim or dropped.** Substring-checked against the
  transcript (layer 1) and the analysis pool (layer 2), fail-closed.
- **Empty is a valid answer.** Zero findings beats an invented one; the evals measure
  fabrication directly, on the *raw pre-guard* output so the metric can't go tautological.

```
~/.claude/projects/**/*.jsonl
   │  [Go]  defensive decode → deterministic stats → token-budgeted reduction
   │  [LLM] analyzing-agent-sessions  → AgentSessionAnalysis
   │  [Go]  verbatim quote validation
   ▼
analysis pool (~/.config/agent-insights/analyses) — outlives the ~30d transcript pruning
   │  [Go]  group by repo root (worktree fold, rename aliases) → one EvidenceBundle
   │        per repo, written to a scratch workdir with repo-namespaced ids
   │  [LLM] synthesizing-workflow-insights → one cross-repo GlobalSynthesis
   │  [Go]  citation + grounding verification → quote guard → recount → rank →
   │        adopted/escalation checks → path normalization → privacy scan
   ▼
GlobalSynthesis snapshot (schema_version 2; one JSON source of truth per run)
```

Full design write-up: [`evals/insights-pipeline-strategy.md`](evals/insights-pipeline-strategy.md).

**Eval-gated** is the load-bearing word: neither layer ships on vibes. A frozen
corpus, hash-keyed rubrics, watermarked baselines, env-pinning and cache-keyed
scoring make a score reproducible, and a two-tier gate decides whether a change is
allowed to land. That harness is the bulk of this repo — see
[The eval harness](#the-eval-harness) below.

## Install

```bash
go install github.com/rkparsons/agent-insights/cmd/agent-insights@latest
```

Requires the `claude` CLI on `PATH` (both LLM stages run as nested `claude -p`
under your subscription auth — API-key env vars are scrubbed so billing can't
silently divert).

## Quickstart

```yaml
# ~/.config/agent-insights/config.yaml
repos:
  - /Users/dev/Developer/my-app          # absolute paths
  - /Users/dev/Developer/tmux-ctrl
aliases:
  old-project-name: my-app               # fold pre-rename sessions onto the current key
cadence_days: 14                         # minimum gap between global synthesis runs
min_sessions: 10                         # volume floor — thinner buckets aren't bundled
due_new_sessions: 10                     # newly-analyzed sessions (summed across repos) a run needs
synthesis_model: claude-fable-5          # the model the global run invokes
dotfiles_repo: /Users/dev/Developer/dotfiles   # optional: dates existing rules from git history
```

**Due** is one global decision, not a per-repo one: a run is due when the last
snapshot is older than `cadence_days` **and** at least `due_new_sessions` analyses have
been written since it, summed across every repo that clears `min_sessions`. Counting
timestamps rather than deltas means a purge of the analysis pool can neither mask new
sessions nor drive the count negative. `status --json`'s `due_repos` names the repos
contributing those sessions, and is empty whenever no run is due.

`synthesis_model` is not a fallback chain: an unavailable model fails the run rather
than silently substituting another, because model drift invalidates every eval
comparison. `claude-opus-5` is the documented alternative — untested under the v2
contract (only opus-4-8 has v1 history), and switching is an explicit config edit plus
an eval re-baseline. `dotfiles_repo` is optional: without it, the verifier skips the
git-dated recency arbitration that decides whether an existing rule has had a chance to
work, and everything else behaves the same.

Repos are matched by path prefix at component boundaries. Sessions outside every
configured repo fall back to a `~/Developer/<project>` path heuristic, so the config
is what makes grouping exact.

```bash
agent-insights backfill --dry-run   # cost preview: to-process / done / gated / meta / quiet
agent-insights backfill             # analyze every substantial session (resumable)
agent-insights synthesize --due     # one cross-repo synthesis, if the global gate says due
agent-insights show --json          # the latest global synthesis
agent-insights status --json        # store root, due repos, acted keys, last run
```

| Command | What it does |
|---|---|
| `backfill [--quiet-for 24h] [--timeout 10m] [--threshold N] [--force] [--dry-run]` | Layer 1 over all sessions; incremental via stamped transcript mtime, resumable, stops after consecutive failures |
| `analyze <session-id\|path> [--force]` | Layer 1 for one session |
| `synthesize [--min-sessions N] [--due] [--dry-run] [--timeout 90m] [--log P]` | Layer 2: one cross-repo run. `--dry-run` prints bundle sizes and the due reasoning without spending; `--timeout` bounds the single model call (default 90m) |
| `status --json` / `show --json` | Stable JSON contracts — schemas in [`schemas/`](schemas/), goldens round-tripped in CI |
| `acted <key>` / `unacted <key>` | Mark a finding adopted so it stops resurfacing |
| `eval <freeze\|outcome\|score\|adjudicate\|probes\|statuses>` | The eval harness, below |

State lives under `~/.config/agent-insights/` (`AGENT_INSIGHTS_DIR` overrides;
`AGENT_INSIGHTS_CONFIG` overrides the config path).

## The eval harness

The interesting part. A pipeline whose output is LLM prose is only as trustworthy as
its measurement, so scoring is a first-class subsystem ([`internal/eval/`](internal/eval/)),
not a smoke test.

- **Frozen corpus** — `eval freeze` writes a fixed set of real sessions (gzipped) plus
  their ground-truth syntheses into an append-only data repo. Re-freezing identical
  content is a no-op; differing content is a hard error. Every score runs on the same
  bytes. The freeze also captures the **asset corpus** the synthesis reads — the global
  `CLAUDE.md`, settings, skills and hooks, plus every bucket repo's `CLAUDE.md` and
  `.claude` tree — and an eval run points the synthesis manifest at those frozen copies
  instead of the live checkouts, so "what the model could read" is pinned too.
  `dotfiles_repo` is deliberately omitted from the eval config: git history is the one
  input a freeze cannot pin, so the run takes the graceful-degradation path, and the
  dated rule-recency branch it skips is pure Go covered by verifier unit tests.
- **Rubric targets, hash-keyed** — each expected insight is a YAML rubric with a
  `pass_at` granularity and nuance clauses; the rubric's sha256 keys every human
  decision made against it, so editing a rubric invalidates its adjudications instead
  of silently reusing them.
- **Watermarked baselines** — when a target's `pass_at` is recalibrated downward, the
  median nuance-pass count at that moment is recorded as a watermark in `benchmark.json`
  (deliberately *not* in the rubric, so watermark upkeep never re-keys the rubric hash).
  A later run that passes the lowered bar but sinks below the watermark raises a
  depth-regression warning — recalibration can't quietly launder a quality drop.
- **Env-pinning** — `EnvPin` composes an ephemeral Claude config dir from a pristine
  snapshot with the live skills overlaid, plus an empty scratch cwd, and hashes the
  lot together with the `claude --version` string. Skill drift and CLI upgrades change
  the hash, so a run is either reproducible or visibly not. The pin also carries the
  configured `synthesis_model`, folded into a *layer-2-only* env hash: a model switch
  re-keys the synthesis stage (an explicit re-baseline) without orphaning the layer-1
  and matcher caches, which cannot see that model.
- **Cache-keyed scoring** — judge calls are content-addressed on
  `(stage, exact stdin payload, model, code version, env hash, repeat index)`.
  Anything the judge can see re-keys; nothing else does. Re-runs are free, and a cache
  hit is provably the same question. The layer-2 raw key additionally carries the
  **asset-corpus hash**: the synthesis reads the frozen `CLAUDE.md`s, skills and
  settings, so a re-frozen corpus must re-buy its answers rather than serve output
  written against corpus bytes that no longer exist.
- **Contested-card adjudication** — Tier 1 is automatic and fail-closed on empty output
  (schema compliance, raw fabrication rate, verbatim-quote validity, run-to-run
  stability, and **recall probes symmetric to every precision axis** so the pipeline
  can't pass by under-reporting). Tier 2 escalates only the *contested* set to a human,
  as recognition cards: rubric statement + produced item + verified quotes + match
  counts. No transcripts, no session ids; cards stay in the local cache and are never
  committed.

**The v2 cutover re-baselines the gate.** Expectations re-freeze on the first v2 run:
findings replace themes, so the v1 score line (0.62) is historical and is not a bar the
first v2 run is measured against. Two things change mechanically at that cutover, both
handled by `eval freeze`: the v2 cross-repo snapshot freezes into its own
`ground-truth/global/` (write-once, like the v1 per-repo reports beside it, which stay
readable for historical records), and a `benchmark.json` left over from the v1 era —
reused verbatim while every bucket resolves — is refused rather than silently pinning v1
buckets the v2 anchors never had. The refusal names the mismatch and asks for the file to
be archived and the freeze re-run; nothing is rewritten under the operator, because those
populations are what committed verdicts' benchmark hashes refer to.

The eval targets themselves were derived by diffing this pipeline's output against two
independent analyses of the same corpus — see
[`evals/insights-eval-spec.md`](evals/insights-eval-spec.md) for the classification and
[`evals/`](evals/) for the historical strategy and report documents (redacted, kept for
methodology).

The corpus and rubrics are **not** in this tree: they are frozen real transcripts and
per-target session-id sets, so they live in a separate private data repo that `eval`
points at via `--data`. What is here is the whole harness — freezing, rubric parsing,
matching, aggregation, verdicts, adjudication — plus its tests.

## tmux-ctrl integration

[tmux-ctrl](https://github.com/rkparsons/tmux-ctrl) surfaces insights in its dashboard by
shelling out to the JSON contracts — no shared library, no Go import:

```bash
agent-insights status --json | jq -r '.due_repos[]'
```

## Scope and limitations

- **Claude Code transcripts only.** It parses `~/.claude/projects/**/*.jsonl`. That
  format is officially unstable; the decoder is defensive (every field optional, never
  hard-fail a session) with a canary counter so drift surfaces loudly.
- **Developed and tested on macOS.** Nothing is knowingly macOS-specific, but Linux
  is untested.
- **Layer 1 calls Opus** (`claude-opus-4-8`), chosen by A/B eval; layer 2's model is the
  `synthesis_model` config key (default `claude-fable-5`). Backfilling a large history
  costs real tokens — `backfill --dry-run` prints the split first.
- **Work inside subagent transcripts is invisible**; the judge correctly returns
  `unclear` for subagent-heavy sessions.
- **Skills are embedded in the binary**, materialized per run into a scratch working
  directory as project-level skills. Nothing is installed into your `~/.claude`, and a
  run's skill content is hashable — which is what makes env-pinning meaningful.
- **Privacy backstop:** a repo-wide scan ([`internal/privacy/`](internal/privacy/))
  walks every git-tracked file on a plain `go test ./...` and fails on home paths,
  session-id shapes, ticket markers, and Claude Code's dash-encoded project slugs.
  It covers the **working tree only** — git history is audited separately before
  publish. Generic shape patterns live in the source; identity tokens would
  themselves be the leak if committed, so they load at scan time from a gitignored
  `.privacy-patterns` file (`AGENT_INSIGHTS_PRIVATE_PATTERNS` overrides the path).
  A clone without that file still runs every generic class.
