package synthesis

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
)

type EnrichOptions struct {
	Repo    string // filter to one repo key; "" = all
	DryRun  bool
	Timeout time.Duration // per titling call; 0 = 10m
}

type EnrichSummary struct {
	Snapshots      int
	Updated        int
	TitlesFilled   int
	LastSeenFilled int
	Skipped        int
}

// TitleReq is one untitled recommendation sent to the titler.
type TitleReq struct {
	Index     int    `json:"index"`
	Type      string `json:"type"`
	Statement string `json:"statement"`
}

// Titler produces titles for untitled recommendations, keyed by TitleReq.Index.
type Titler func(ctx context.Context, reqs []TitleReq) (map[int]string, error)

// RunEnrich backfills title and last_seen onto stored syntheses. Idempotent:
// only missing fields are filled, and untouched snapshots are not rewritten.
// last_seen is approximate here — stored recs carry no session ids, so it
// derives from theme_refs' theme sessions, falling back to window.to; fresh
// syntheses compute it exactly in Finalize.
func RunEnrich(ctx context.Context, titler Titler, opts EnrichOptions) (EnrichSummary, error) {
	var sum EnrichSummary
	if opts.Timeout == 0 {
		opts.Timeout = 10 * time.Minute
	}
	if !opts.DryRun {
		lock, err := insights.AcquireLock("enrich")
		if err != nil {
			return sum, err
		}
		defer lock.Release()
	}

	dates := analysisDates()

	base := synthesisDir()
	repoDirs, err := os.ReadDir(base)
	if os.IsNotExist(err) {
		return sum, nil
	}
	if err != nil {
		return sum, err
	}
	for _, rd := range repoDirs {
		if !rd.IsDir() || (opts.Repo != "" && rd.Name() != opts.Repo) {
			continue
		}
		dir := filepath.Join(base, rd.Name())
		entries, err := os.ReadDir(dir)
		if err != nil {
			return sum, err
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, name := range names {
			if err := enrichSnapshot(ctx, filepath.Join(dir, name), dates, titler, opts, &sum); err != nil {
				return sum, err
			}
		}
	}
	return sum, nil
}

// analysisDates maps session_id → start date over the analyses pool. A
// missing/empty pool degrades to window.to fallbacks, not an error.
func analysisDates() map[string]string {
	analyses, err := LoadAnalyses()
	if err != nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(analyses))
	for _, a := range analyses {
		out[a.Stats.SessionID] = a.Stats.Start.Format("2006-01-02")
	}
	return out
}

func enrichSnapshot(ctx context.Context, path string, dates map[string]string, titler Titler, opts EnrichOptions, sum *EnrichSummary) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var s RepoSynthesis
	if err := json.Unmarshal(data, &s); err != nil {
		sum.Skipped++
		fmt.Fprintf(os.Stderr, "enrich: %s skipped (malformed): %v\n", path, err)
		return nil
	}
	sum.Snapshots++

	var missingSeen, missingTitle int
	for _, r := range s.Recommendations {
		if r.LastSeen == "" {
			missingSeen++
		}
		if r.Title == "" {
			missingTitle++
		}
	}
	if missingSeen == 0 && missingTitle == 0 {
		return nil
	}
	if opts.DryRun {
		fmt.Fprintf(os.Stderr, "enrich (dry-run): %s · %d missing titles · %d missing last_seen\n", path, missingTitle, missingSeen)
		return nil
	}

	titles, lastSeens := 0, 0
	for i := range s.Recommendations {
		r := &s.Recommendations[i]
		if r.LastSeen != "" {
			continue
		}
		max := ""
		for _, ref := range r.ThemeRefs {
			if ref < 0 || ref >= len(s.Themes) {
				continue
			}
			for _, sid := range s.Themes[ref].SessionIDs {
				if d := dates[sid]; d > max {
					max = d
				}
			}
		}
		if max == "" {
			max = s.Window.To
		}
		if max != "" {
			r.LastSeen = max
			lastSeens++
		}
	}

	if missingTitle > 0 && titler != nil {
		var reqs []TitleReq
		for i, r := range s.Recommendations {
			if r.Title == "" {
				reqs = append(reqs, TitleReq{Index: i, Type: r.Type, Statement: r.Statement})
			}
		}
		tctx, cancel := context.WithTimeout(ctx, opts.Timeout)
		got, err := titler(tctx, reqs)
		cancel()
		if err != nil {
			fmt.Fprintf(os.Stderr, "enrich: %s titling failed (last_seen still applied): %v\n", path, err)
		} else {
			seen := map[string]bool{}
			for _, r := range s.Recommendations {
				if r.Title != "" {
					seen[strings.ToLower(r.Title)] = true
				}
			}
			for i, r := range s.Recommendations {
				if r.Title != "" {
					continue
				}
				title := normalizeTitle(got[i])
				for _, w := range titleWarnings(title, r.Statement, seen) {
					fmt.Fprintf(os.Stderr, "enrich: %s: %s\n", path, w)
				}
				if title != "" {
					s.Recommendations[i].Title = title
					titles++
				}
			}
		}
	}

	if titles == 0 && lastSeens == 0 {
		return nil
	}
	md := Render(s)
	if leaks := scanReport(md); len(leaks) > 0 {
		sum.Skipped++
		fmt.Fprintf(os.Stderr, "enrich: %s BLOCKED by privacy scan: %v\n", path, leaks)
		return nil
	}
	date := strings.TrimSuffix(filepath.Base(path), ".json")
	if err := Store(s, md, date); err != nil {
		return err
	}
	sum.Updated++
	sum.TitlesFilled += titles
	sum.LastSeenFilled += lastSeens
	return nil
}
