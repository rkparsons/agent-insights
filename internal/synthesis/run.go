package synthesis

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
	"github.com/rkparsons/agent-insights/skills"
)

type Options struct {
	Repo        string
	MinSessions int
	DryRun      bool
	Due         bool          // only synthesize repos DueRepos reports
	Cadence     time.Duration // due-ness age threshold; 0 = DefaultCadence
	LogPath     string        // recorded in the run state; the spawner tees output here
}
type Summary struct {
	Repos   int
	Written int
	Skipped int
}

func RunSynthesize(ctx context.Context, newSyn SynthesizerFactory, opts Options) (sum Summary, retErr error) {
	if opts.MinSessions == 0 {
		opts.MinSessions = DefaultMinSessions
	}
	// A store with no analyses yet (no repo has analyzed a session) is a valid
	// empty state, not an error: RunSynthesize must still reach the run-state
	// write below so due/running/error stays visible to the TUI.
	analyses, err := LoadAnalyses()
	if err != nil && !os.IsNotExist(err) {
		return Summary{}, err
	}
	cfg, err := insights.LoadConfig()
	if err != nil {
		return Summary{}, err
	}
	cfg.WarnIfNoRepos()
	groups := GroupByRepo(analyses, opts.MinSessions, cfg)

	keys := make([]string, 0, len(groups))
	for k := range groups {
		if opts.Repo == "" || k == opts.Repo {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	if opts.Due {
		cadence := opts.Cadence
		if cadence == 0 {
			cadence = DefaultCadence
		}
		syntheses, err := LoadSyntheses()
		if err != nil {
			return Summary{}, err
		}
		dueSet := make(map[string]bool)
		for _, k := range DueRepos(groups, syntheses, cadence, time.Now()) {
			dueSet[k] = true
		}
		kept := keys[:0]
		for _, k := range keys {
			if dueSet[k] {
				kept = append(kept, k)
			}
		}
		keys = kept
	}
	sum = Summary{Repos: len(keys)}
	if opts.DryRun {
		for _, k := range keys {
			fmt.Fprintf(os.Stderr, "synthesis (dry-run): %s · %d analyses\n", k, len(groups[k]))
		}
		return sum, nil
	}

	lock, err := insights.AcquireLock("synthesize")
	if err != nil {
		return sum, err
	}
	defer lock.Release()

	rs := RunState{Status: "running", PID: os.Getpid(), StartedAt: time.Now().UTC(), LogPath: opts.LogPath}
	writeRunState(rs)
	var failures []string
	defer func() {
		finishedAt := time.Now().UTC()
		rs.FinishedAt = &finishedAt
		rs.Written, rs.Skipped = sum.Written, sum.Skipped
		rs.Status = "ok"
		if retErr != nil {
			failures = append(failures, retErr.Error())
		}
		if len(failures) > 0 {
			rs.Status, rs.Reason = "failed", strings.Join(failures, "; ")
		}
		writeRunState(rs)
	}()

	// Skill delivery is the run's job, not the operator's ~/.claude: the nested
	// claude calls work out of a scratch cwd with the skills materialized into
	// it, removed when the run ends. Set up after the run-state defer so a
	// failure here is recorded like any other.
	if newSyn == nil {
		return sum, errors.New("no synthesizer factory: a run must be able to build its synthesizer for the materialized workdir")
	}
	workDir, cleanupWorkDir, err := skills.TempWorkdir()
	if err != nil {
		return sum, err
	}
	defer cleanupWorkDir()
	syn := newSyn(workDir)

	date := time.Now().UTC().Format("2006-01-02")
	for _, k := range keys {
		adopt := NewAdoptChecker(repoPathFor(groups[k]))
		// A production-size bundle (116 analyses) measured 35 min on Opus;
		// eval pool slices ran 8–14 min. The old 10m/20m deadlines killed
		// every call after burning spend.
		rctx, cancel := context.WithTimeout(ctx, 90*time.Minute)
		res, _, err := Synthesize(rctx, k, groups[k], syn, adopt)
		cancel()
		if err != nil {
			sum.Skipped++
			failures = append(failures, fmt.Sprintf("%s: %v", k, err))
			fmt.Fprintf(os.Stderr, "synthesis: %s skipped: %v\n", k, err)
			continue
		}
		md := Render(res)
		if leaks := scanReport(md); len(leaks) > 0 {
			sum.Skipped++
			failures = append(failures, fmt.Sprintf("%s: privacy scan blocked: %v", k, leaks))
			fmt.Fprintf(os.Stderr, "synthesis: %s BLOCKED by privacy scan: %v\n", k, leaks)
			continue
		}
		if n := len(res.Meta.ValidationErrors); n > 0 {
			fmt.Fprintf(os.Stderr, "synthesis: %s has %d validation warnings (written, surfaced in report)\n", k, n)
		}
		if err := Store(res, md, date); err != nil {
			return sum, err
		}
		sum.Written++
	}
	return sum, nil
}

// repoPathFor recovers a repo root from a group for the already-adopted grep.
func repoPathFor(group []insights.AgentSessionAnalysis) string {
	for _, a := range group {
		if a.Stats.Repo != "" {
			return a.Stats.Repo
		}
	}
	return ""
}
