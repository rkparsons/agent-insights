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
		rctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
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
