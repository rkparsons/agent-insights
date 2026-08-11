package synthesis

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	Snapshots      int // parseable snapshots examined
	Updated        int // snapshots rewritten
	TitlesFilled   int
	LastSeenFilled int
	Malformed      int
	LeakBlocked    int
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
// derives from theme_refs' theme sessions; when nothing resolves it is left
// empty rather than guessed, mirroring Finalize, so a later run can still
// heal it once the data resolves. Fresh syntheses compute it exactly in
// Finalize.
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

	// A missing pool (fresh install) degrades to window.to fallbacks — legitimate.
	// Any other read/parse error must not: it could mean the pool is merely
	// mid-repair, and filling last_seen from window.to would be permanent (enrich
	// never overwrites a non-empty field), baking in a wrong date forever.
	dates, err := analysisDates()
	skipLastSeen := false
	if err != nil {
		if os.IsNotExist(err) {
			dates = map[string]string{}
		} else {
			fmt.Fprintf(os.Stderr, "enrich: analyses pool unreadable, skipping last_seen fill this run: %v\n", err)
			dates = map[string]string{}
			skipLastSeen = true
		}
	}

	base := synthesisDir()
	repoDirs, err := os.ReadDir(base)
	if os.IsNotExist(err) {
		return sum, nil
	}
	if err != nil {
		return sum, err
	}
	matched := false
	for _, rd := range repoDirs {
		if !rd.IsDir() || (opts.Repo != "" && rd.Name() != opts.Repo) {
			continue
		}
		matched = true
		dir := filepath.Join(base, rd.Name())
		names, err := snapshotJSONNames(dir)
		if err != nil {
			return sum, err
		}
		for _, name := range names {
			if err := enrichSnapshot(ctx, filepath.Join(dir, name), dates, skipLastSeen, titler, opts, &sum); err != nil {
				return sum, err
			}
		}
	}
	if opts.Repo != "" && !matched {
		fmt.Fprintf(os.Stderr, "enrich: no synthesis snapshots for repo %q\n", opts.Repo)
	}
	return sum, nil
}

// analysisDates maps session_id → start date over the analyses pool. Errors
// are returned rather than swallowed so RunEnrich can tell a missing pool
// (fresh install, fine to degrade) from an unreadable/corrupt one (must not
// silently degrade — see RunEnrich).
func analysisDates() (map[string]string, error) {
	analyses, err := LoadAnalyses()
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(analyses))
	for _, a := range analyses {
		out[a.Stats.SessionID] = sessionDate(a.Stats.Start)
	}
	return out, nil
}

func enrichSnapshot(ctx context.Context, path string, dates map[string]string, skipLastSeen bool, titler Titler, opts EnrichOptions, sum *EnrichSummary) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var s RepoSynthesis
	if err := json.Unmarshal(data, &s); err != nil {
		sum.Malformed++
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
	if opts.DryRun {
		if missingSeen == 0 && missingTitle == 0 {
			return nil
		}
		fmt.Fprintf(os.Stderr, "enrich (dry-run): %s · %d missing titles · %d missing last_seen\n", path, missingTitle, missingSeen)
		return nil
	}

	titles, lastSeens := 0, 0
	if !skipLastSeen {
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
			if max != "" {
				r.LastSeen = max
				lastSeens++
			}
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
		// Nothing resolved this run — either every field was already present,
		// or what's left (e.g. permanently out-of-range theme_refs) never
		// will. Either way the JSON is unchanged; only the sibling .md can
		// still be stale (a crash between Store's two atomic writes).
		return healStaleMD(path, s, sum)
	}
	md := Render(s)
	if leaks := scanReport(md); len(leaks) > 0 {
		sum.LeakBlocked++
		fmt.Fprintf(os.Stderr, "enrich: %s BLOCKED by privacy scan: %v\n", path, leaks)
		return nil
	}
	// Write back to the exact path that was read, not Store's dir re-derived
	// from s.Repo — a mismatched repo field would otherwise fork the snapshot
	// into a second location that gets re-titled (LLM spend) on every future run.
	data, err = json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWrite(path, data); err != nil {
		return err
	}
	if err := atomicWrite(mdPath(path), []byte(md)); err != nil {
		return err
	}
	sum.Updated++
	sum.TitlesFilled += titles
	sum.LastSeenFilled += lastSeens
	return nil
}

// mdPath derives a snapshot's sibling .md path from its .json path (Store
// writes them as a pair under the same date stem).
func mdPath(jsonPath string) string {
	return strings.TrimSuffix(jsonPath, ".json") + ".md"
}

// healStaleMD re-renders an already-fully-enriched snapshot's markdown and
// rewrites it if it drifted from the JSON — e.g. a crash between Store's two
// atomic writes left a stale .md that the missing-field check above can never
// notice (it only reads the JSON).
func healStaleMD(path string, s RepoSynthesis, sum *EnrichSummary) error {
	md := Render(s)
	mp := mdPath(path)
	if onDisk, err := os.ReadFile(mp); err == nil && string(onDisk) == md {
		return nil
	}
	if leaks := scanReport(md); len(leaks) > 0 {
		fmt.Fprintf(os.Stderr, "enrich: %s BLOCKED md heal by privacy scan: %v\n", path, leaks)
		return nil
	}
	if err := atomicWrite(mp, []byte(md)); err != nil {
		return err
	}
	sum.Updated++
	return nil
}
