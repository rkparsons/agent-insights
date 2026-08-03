# agent-insights

Eval-gated LLM pipeline that mines your Claude Code sessions for workflow insights.

It reads the transcripts Claude Code already writes to `~/.claude/projects/`, distills
each session into a typed analysis, and synthesizes them **per repo** into ranked
friction themes and concrete recommendations — CLAUDE.md rules, skills, hooks, settings.
Improvement only: no praise, no token dashboard.

The design stance is that deterministic Go brackets the LLM at every stage:

- **The LLM never emits a number.** Counts come from Go recomputing over typed
  evidence-id sets; a regex tripwire fails the run on any quantitative claim in LLM prose.
- **Every quote is verified verbatim or dropped.** Substring-checked against the
  transcript (layer 1) and the analysis pool (layer 2), fail-closed.
- **Empty is a valid answer.** Zero themes beats an invented one; the evals measure
  fabrication directly, on the *raw pre-guard* output so the metric can't go tautological.

```
~/.claude/projects/**/*.jsonl
   │  [Go]  defensive decode → deterministic stats → token-budgeted reduction
   │  [LLM] analyzing-agent-sessions  → AgentSessionAnalysis
   │  [Go]  verbatim quote validation
   ▼
analysis pool (~/.config/agent-insights/analyses) — outlives the ~30d transcript pruning
   │  [Go]  group by repo root (worktree fold, rename aliases) → EvidenceBundle w/ typed ids
   │  [LLM] synthesizing-workflow-insights → themes + typed recommendations
   │  [Go]  id/partition verification → quote guard → count → rank → already-adopted check
   ▼
RepoSynthesis snapshot (JSON source of truth; markdown render derived)
```

Full design write-up: [`evals/insights-pipeline-strategy.md`](evals/insights-pipeline-strategy.md).

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
cadence_days: 14                         # how often a repo becomes due for synthesis
min_sessions: 10                         # volume floor — thinner buckets aren't synthesized
```

Repos are matched by path prefix at component boundaries. Sessions outside every
configured repo fall back to a `~/Developer/<project>` path heuristic, so the config
is what makes grouping exact.

```bash
agent-insights backfill --dry-run   # cost preview: to-process / done / gated / meta / quiet
agent-insights backfill             # analyze every substantial session (resumable)
agent-insights synthesize --due     # per-repo synthesis for repos past cadence_days
agent-insights show --json          # every repo's latest synthesis
agent-insights status --json        # store root, due repos, acted keys, last run
```

| Command | What it does |
|---|---|
| `backfill [--quiet-for 24h] [--timeout 10m] [--threshold N] [--force] [--dry-run]` | Layer 1 over all sessions; incremental via stamped transcript mtime, resumable, stops after consecutive failures |
| `analyze <session-id\|path> [--force]` | Layer 1 for one session |
| `synthesize [--repo K] [--min-sessions N] [--due] [--dry-run] [--log P]` | Layer 2 per repo |
| `status --json` / `show --json` | Stable JSON contracts — schemas in [`schemas/`](schemas/), goldens round-tripped in CI |
| `acted <key>` / `unacted <key>` | Mark a recommendation adopted so it stops resurfacing |
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
  bytes.
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
  the hash, so a run is either reproducible or visibly not.
- **Cache-keyed scoring** — judge calls are content-addressed on
  `(stage, exact stdin payload, model, code version, env hash, repeat index)`.
  Anything the judge can see re-keys; nothing else does. Re-runs are free, and a cache
  hit is provably the same question.
- **Contested-card adjudication** — Tier 1 is automatic and fail-closed on empty output
  (schema compliance, raw fabrication rate, verbatim-quote validity, run-to-run
  stability, and **recall probes symmetric to every precision axis** so the pipeline
  can't pass by under-reporting). Tier 2 escalates only the *contested* set to a human,
  as recognition cards: rubric statement + produced item + verified quotes + match
  counts. No transcripts, no session ids; cards stay in the local cache and are never
  committed.

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
- **Both LLM stages call Opus** (`claude-opus-4-8`), chosen by A/B eval. Backfilling a
  large history costs real tokens — `backfill --dry-run` prints the split first.
- **Work inside subagent transcripts is invisible**; the judge correctly returns
  `unclear` for subagent-heavy sessions.
- **Skills are embedded in the binary**, materialized per run into a scratch working
  directory as project-level skills. Nothing is installed into your `~/.claude`, and a
  run's skill content is hashable — which is what makes env-pinning meaningful.
- Committed artifacts are checked by a repo-wide privacy scan
  ([`internal/privacy/`](internal/privacy/)) that runs in a plain `go test ./...`.
