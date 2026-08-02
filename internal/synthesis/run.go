package synthesis

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	"tmux-ctrl/internal/insights"
)

type Options struct {
	Repo        string
	MinSessions int
	DryRun      bool
	Due         bool          // only synthesize repos DueRepos reports
	Cadence     time.Duration // due-ness age threshold; 0 = DefaultCadence
}
type Summary struct {
	Repos   int
	Written int
	Skipped int
}

func RunSynthesize(ctx context.Context, syn Synthesizer, opts Options) (Summary, error) {
	if opts.MinSessions == 0 {
		opts.MinSessions = DefaultMinSessions
	}
	analyses, err := LoadAnalyses()
	if err != nil {
		return Summary{}, err
	}
	groups := GroupByRepo(analyses, opts.MinSessions)

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
	sum := Summary{Repos: len(keys)}
	if opts.DryRun {
		for _, k := range keys {
			fmt.Fprintf(os.Stderr, "synthesis (dry-run): %s · %d analyses\n", k, len(groups[k]))
		}
		return sum, nil
	}

	lock, err := insights.AcquireLock()
	if err != nil {
		return sum, err
	}
	defer lock.Release()

	date := time.Now().UTC().Format("2006-01-02")
	for _, k := range keys {
		adopt := NewAdoptChecker(repoPathFor(groups[k]))
		// A production-size bundle (116 analyses) measured 35 min on Opus;
		// eval pool slices ran 8–14 min. The old 10m/20m deadlines killed
		// every call after burning spend.
		rctx, cancel := context.WithTimeout(ctx, 90*time.Minute)
		rs, report, err := Synthesize(rctx, k, groups[k], syn, adopt)
		cancel()
		if err != nil {
			sum.Skipped++
			fmt.Fprintf(os.Stderr, "synthesis: %s skipped: %v\n", k, err)
			continue
		}
		md := Render(rs)
		if leaks := scanReport(md); len(leaks) > 0 {
			sum.Skipped++
			fmt.Fprintf(os.Stderr, "synthesis: %s BLOCKED by privacy scan: %v\n", k, leaks)
			continue
		}
		if len(report.HardErrors) > 0 {
			fmt.Fprintf(os.Stderr, "synthesis: %s has %d validation warnings (written, surfaced in report)\n", k, len(report.HardErrors))
		}
		if err := Store(rs, md, date); err != nil {
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
