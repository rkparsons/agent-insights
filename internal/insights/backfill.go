package insights

import (
	"context"
	"errors"
	"time"

	"tmux-ctrl/internal/sources/claude"
)

// RunBackfill scans every top-level transcript, applies incremental + gate skips, and
// analyzes the substantial, not-yet-done sessions. Resumable across the pruning race
// via the stamped analysis files + the gated/errored manifest; lock-guarded against a
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

	var sum RunSummary
	seen := map[string]bool{} // dedup same id across project dirs (resume copies)
	for _, ref := range dedupNewest(refs) {
		if seen[ref.SessionID] {
			continue
		}
		seen[ref.SessionID] = true
		sum.Scanned++

		if !opts.Force {
			if reason, skip := backfillSkip(ref, manifest, opts.MinAssistantTurns, opts.RetryErrored); skip {
				switch reason {
				case "incremental":
					sum.SkippedIncremental++
				case "gate":
					sum.SkippedGate++
				case "errored":
					sum.Errored++
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
			if errors.Is(err, context.Canceled) {
				return sum, err // user abort: do not record, resume cleanly next run
			}
			recordErrored(&sum, &manifest, ref, err)
			continue
		}
		sum.Analyzed++
		sum.DroppedPreferences += rep.DroppedPreferences
	}
	return sum, nil
}

func recordErrored(sum *RunSummary, manifest *map[string]ManifestEntry, ref claude.TranscriptRef, err error) {
	sum.Errored++
	e := ManifestEntry{SessionID: ref.SessionID, TranscriptMtime: ref.Mtime, Outcome: "errored", Error: err.Error(), At: time.Now().UTC()}
	(*manifest)[ref.SessionID] = e
	_ = appendManifest(e)
}

// backfillSkip implements the pre-decode skip rule. analyzed-fresh wins; then a gated
// entry at the same threshold; then an errored entry unless retrying.
func backfillSkip(ref claude.TranscriptRef, m map[string]ManifestEntry, threshold int, retryErrored bool) (string, bool) {
	if analyzedFresh(ref.SessionID, ref.Mtime) {
		return "incremental", true
	}
	e, ok := m[ref.SessionID]
	if !ok || e.TranscriptMtime.Before(ref.Mtime) {
		return "", false
	}
	switch e.Outcome {
	case "gated":
		if e.Threshold == threshold {
			return "gate", true
		}
	case "errored":
		if !retryErrored {
			return "errored", true
		}
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
