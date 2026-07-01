package insights

import (
	"context"
	"time"

	"tmux-ctrl/internal/sources/claude"
)

// RunBackfill scans every top-level transcript, applies incremental + gate skips, and
// analyzes the substantial, not-yet-done sessions. "Done" is a current analysis file, so
// a re-run reprocesses every not-done session — window-interrupted and previously-errored
// alike (no flag). It parks cleanly after consecutiveFailureLimit judge failures in a row
// so a hit usage window doesn't grind through per-session timeouts. Lock-guarded against a
// concurrent run.
func RunBackfill(ctx context.Context, repo RepoResolver, judge Judge, opts Options) (RunSummary, error) {
	lock, err := AcquireLock()
	if err != nil {
		return RunSummary{}, err
	}
	defer lock.Release()

	refs, err := claude.WalkTranscripts()
	if err != nil {
		return RunSummary{}, err
	}
	manifest, err := loadManifest()
	if err != nil {
		return RunSummary{}, err
	}

	refs = dedupNewest(refs)
	var sum RunSummary
	consecutiveFailures := 0
	for _, ref := range refs {
		sum.Scanned++

		if !opts.Force {
			if reason, skip := backfillSkip(ref, manifest, opts.MinAssistantTurns); skip {
				switch reason {
				case "incremental":
					sum.SkippedIncremental++
				case "gate":
					sum.SkippedGate++
				}
				continue
			}
		}

		events, canary, _, err := claude.LoadTranscript(ref.Path)
		if err != nil {
			recordErrored(&sum, &manifest, ref, err)
			continue
		}
		stats := Extract(events, canary, ref.SessionID, repo).Stats
		if !Substantial(stats, opts.MinAssistantTurns) {
			sum.SkippedGate++
			e := ManifestEntry{SessionID: ref.SessionID, TranscriptMtime: ref.Mtime, Outcome: "gated", Threshold: opts.MinAssistantTurns, At: time.Now().UTC()}
			manifest[ref.SessionID] = e
			_ = appendManifest(e)
			continue
		}

		sctx, cancel := context.WithTimeout(ctx, opts.Timeout)
		rep, err := analyzeSession(sctx, ref, events, canary, repo, judge)
		cancel()
		if err != nil {
			// The real Judge surfaces exec failures as *exec.ExitError("signal: killed"),
			// not a context sentinel, so we cannot use errors.Is on the judge error to
			// distinguish user-abort from per-session timeout. Branch on the parent ctx instead.
			if ctx.Err() != nil { // parent canceled (user abort) — leave unrecorded for clean resume
				return sum, ctx.Err()
			}
			recordErrored(&sum, &manifest, ref, err)
			consecutiveFailures++
			if consecutiveFailures >= consecutiveFailureLimit {
				// The usage window is likely hit; park cleanly rather than grind through
				// every remaining session's timeout. A re-run finishes what is left.
				sum.Parked = true
				sum.Remaining = countRemaining(refs, manifest, opts)
				return sum, nil
			}
			continue
		}
		consecutiveFailures = 0
		sum.Analyzed++
		sum.DroppedPreferences += rep.DroppedPreferences
	}
	return sum, nil
}

// consecutiveFailureLimit parks the backfill after this many judge failures in a row —
// the signature of a hit usage window (rate-limited claude -p calls fail or hang).
const consecutiveFailureLimit = 3

// countRemaining reports how many deduped sessions still need analysis: neither done (a
// current analysis file) nor gated at the current threshold. Errored sessions count as
// remaining. Read-only over the analysis files + in-memory manifest; no transcript decode.
func countRemaining(refs []claude.TranscriptRef, manifest map[string]ManifestEntry, opts Options) int {
	_, _, pending := planCounts(refs, manifest, opts)
	return pending
}

// planCounts classifies each deduped ref by the cheap pre-decode signals: done (current
// analysis file), gated (manifest entry at this threshold), or pending (everything else —
// including previously-errored and never-seen sessions). Under --force nothing is skipped,
// so every session is pending. No transcript decode, so pending is an upper bound: a
// never-seen session that turns out trivial gates only once actually decoded.
func planCounts(refs []claude.TranscriptRef, manifest map[string]ManifestEntry, opts Options) (done, gated, pending int) {
	for _, ref := range refs {
		if !opts.Force {
			if reason, skip := backfillSkip(ref, manifest, opts.MinAssistantTurns); skip {
				switch reason {
				case "incremental":
					done++
				case "gate":
					gated++
				}
				continue
			}
		}
		pending++
	}
	return done, gated, pending
}

// BackfillCounts is the pre-run split surfaced by BackfillPlan.
type BackfillCounts struct {
	ToProcess int // sessions lacking a current analysis file and not gated (upper bound; see planCounts)
	Done      int // sessions with a current analysis file
	Gated     int // sessions recorded gated at the current threshold
}

// BackfillPlan classifies every top-level session by the cheap pre-decode signals and
// returns the counts, spending nothing: no transcript decode, no Judge. It answers "how
// many are left" between usage windows and backs the `--dry-run` / pre-run summary.
// Lock-free — a concurrent run only shifts the snapshot.
func BackfillPlan(opts Options) (BackfillCounts, error) {
	refs, err := claude.WalkTranscripts()
	if err != nil {
		return BackfillCounts{}, err
	}
	manifest, err := loadManifest()
	if err != nil {
		return BackfillCounts{}, err
	}
	done, gated, pending := planCounts(dedupNewest(refs), manifest, opts)
	return BackfillCounts{ToProcess: pending, Done: done, Gated: gated}, nil
}

func recordErrored(sum *RunSummary, manifest *map[string]ManifestEntry, ref claude.TranscriptRef, err error) {
	sum.Errored++
	e := ManifestEntry{SessionID: ref.SessionID, TranscriptMtime: ref.Mtime, Outcome: "errored", Error: err.Error(), At: time.Now().UTC()}
	(*manifest)[ref.SessionID] = e
	_ = appendManifest(e)
}

// backfillSkip implements the pre-decode skip rule. analyzed-fresh (a current analysis
// file) wins; then a gated entry at the same threshold. Errored sessions are NOT skipped:
// they have no analysis file, so they are simply "not done" and get retried next run.
func backfillSkip(ref claude.TranscriptRef, m map[string]ManifestEntry, threshold int) (string, bool) {
	if analyzedFresh(ref.SessionID, ref.Mtime) {
		return "incremental", true
	}
	e, ok := m[ref.SessionID]
	if !ok || e.TranscriptMtime.Before(ref.Mtime) {
		return "", false
	}
	if e.Outcome == "gated" && e.Threshold == threshold {
		return "gate", true
	}
	return "", false
}

// dedupNewest keeps the newest ref per session-id (a resume copies a transcript into
// more than one project dir).
func dedupNewest(refs []claude.TranscriptRef) []claude.TranscriptRef {
	best := map[string]claude.TranscriptRef{}
	for _, r := range refs {
		if cur, ok := best[r.SessionID]; !ok || r.Mtime.After(cur.Mtime) {
			best[r.SessionID] = r
		}
	}
	out := make([]claude.TranscriptRef, 0, len(best))
	for _, r := range best {
		out = append(out, r)
	}
	return out
}
