package insights

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tmux-ctrl/internal/transcript"
)

// Options configures an analyze run.
type Options struct {
	MinAssistantTurns int
	Timeout           time.Duration
	Force             bool
	DryRun            bool // backfill only: count the plan and print it, spend nothing
	// QuietFor skips transcripts modified within this window, so still-active
	// sessions aren't analyzed mid-flight; 0 disables.
	QuietFor time.Duration
}

// RunSummary is the end-of-run tally surfaced to the user.
type RunSummary struct {
	Scanned            int
	Analyzed           int
	SkippedIncremental int
	SkippedGate        int
	SkippedQuiet       int
	SkippedMeta        int
	Errored            int
	DroppedPreferences int

	Parked    bool // stopped early on a consecutive-failure run of judge calls
	Remaining int  // sessions still needing analysis when the run stopped (parked runs)
}

// RunSingle analyzes one explicitly named session (id or path). It bypasses the gate
// (explicit intent overrides the triviality cut) but honors incremental skip unless
// Force.
func RunSingle(ctx context.Context, sessionOrPath string, repo RepoResolver, judge Judge, opts Options) (RunSummary, error) {
	lock, err := AcquireLock()
	if err != nil {
		return RunSummary{}, err
	}
	defer lock.Release()

	ref, err := resolveRef(sessionOrPath)
	if err != nil {
		return RunSummary{}, err
	}
	var sum RunSummary
	sum.Scanned = 1
	if !opts.Force && analyzedFresh(ref.SessionID, ref.Mtime) {
		sum.SkippedIncremental = 1
		return sum, nil
	}
	events, canary, _, err := transcript.LoadTranscript(ref.Path)
	if err != nil {
		return sum, err
	}
	sctx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()
	rep, err := analyzeSession(sctx, ref, events, canary, repo, judge)
	if err != nil {
		sum.Errored = 1
		return sum, err
	}
	sum.Analyzed = 1
	sum.DroppedPreferences = rep.DroppedPreferences
	return sum, nil
}

// resolveRef accepts a filesystem path (used as-is) or a session-id (resolved via the
// projects tree).
func resolveRef(sessionOrPath string) (transcript.TranscriptRef, error) {
	if fi, err := os.Stat(sessionOrPath); err == nil && !fi.IsDir() {
		return transcript.TranscriptRef{
			SessionID: sessionIDFromPath(sessionOrPath),
			Path:      sessionOrPath,
			Mtime:     fi.ModTime(),
		}, nil
	}
	return transcript.FindTranscript(sessionOrPath)
}

func sessionIDFromPath(p string) string {
	return strings.TrimSuffix(filepath.Base(p), ".jsonl")
}

// analyzeSession runs the producer on already-decoded events, stamps the decode-time
// mtime, and atomically stores the artifact. The caller has already gated.
func analyzeSession(ctx context.Context, ref transcript.TranscriptRef, events []transcript.TranscriptEvent, canary transcript.Canary, repo RepoResolver, judge Judge) (ValidationReport, error) {
	a, rep, err := Analyze(ctx, events, canary, ref.SessionID, repo, judge)
	if err != nil {
		return ValidationReport{}, err
	}
	a.TranscriptMtime = ref.Mtime
	if err := WriteAnalysis(a); err != nil {
		return ValidationReport{}, err
	}
	return rep, nil
}

// analyzedFresh reports whether a stored analysis already covers this transcript mtime.
func analyzedFresh(sessionID string, mtime time.Time) bool {
	stamped, ok := ReadAnalysisMtime(sessionID)
	return ok && !stamped.Before(mtime)
}
