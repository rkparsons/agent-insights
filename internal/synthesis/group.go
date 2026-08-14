package synthesis

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/rkparsons/agent-insights/internal/insights"
)

const DefaultMinSessions = 10

// RepoKey returns the synthesis grouping key for an analysis, or "" to drop it.
// Configured analyses use basename(stats.repo) with any trailing /.worktrees/<wt>
// segment stripped first, so a worktree-specific configured repo still folds to its
// project root; unmatched ("") analyses derive the project root from cwd (the segment
// under ~/Developer/<project>), never basename(cwd) (which would misfile worktree
// leaves like ".../alpha/.worktrees/x/src" as "src"). cfg.Canonical folds a
// pre-rename project path segment onto its current key.
func RepoKey(a insights.AgentSessionAnalysis, cfg insights.Config) string {
	if r := a.Stats.Repo; r != "" {
		return filepath.Base(stripWorktree(r))
	}
	proj := projectUnderDeveloper(a.Stats.Cwd)
	if proj == "" {
		return ""
	}
	return cfg.Canonical(proj)
}

// stripWorktree truncates a path at a "/.worktrees/" segment, if present.
func stripWorktree(p string) string {
	if i := strings.Index(p, "/.worktrees/"); i >= 0 {
		return p[:i]
	}
	return p
}

// projectUnderDeveloper extracts <project> from a path like
// /Users/<u>/Developer/<project>/... ; "" if the path is not under a Developer dir.
func projectUnderDeveloper(cwd string) string {
	const marker = "/Developer/"
	i := strings.Index(cwd, marker)
	if i < 0 {
		return ""
	}
	rest := cwd[i+len(marker):]
	if j := strings.IndexByte(rest, '/'); j >= 0 {
		return rest[:j]
	}
	return rest
}

// LoadAnalyses reads every analysis JSON under insights.AnalysesDir(),
// stamping each one's AnalyzedAt from its store file's mtime — the only record
// of when an analysis was written, since the artifact itself carries no such
// field (see insights.AgentSessionAnalysis).
func LoadAnalyses() ([]insights.AgentSessionAnalysis, error) {
	dir := insights.AnalysesDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []insights.AgentSessionAnalysis
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		var a insights.AgentSessionAnalysis
		if err := json.Unmarshal(data, &a); err != nil {
			return nil, err
		}
		if info, err := e.Info(); err == nil {
			a.AnalyzedAt = info.ModTime()
		}
		out = append(out, a)
	}
	return out, nil
}

// GroupByRepo buckets analyses by RepoKey, dropping the "" key and any bucket
// with fewer than minSessions analyses. cfg is threaded through to RepoKey.
func GroupByRepo(analyses []insights.AgentSessionAnalysis, minSessions int, cfg insights.Config) map[string][]insights.AgentSessionAnalysis {
	byKey := map[string][]insights.AgentSessionAnalysis{}
	for _, a := range analyses {
		k := RepoKey(a, cfg)
		if k == "" {
			continue
		}
		byKey[k] = append(byKey[k], a)
	}
	for k, g := range byKey {
		if len(g) < minSessions {
			delete(byKey, k)
		}
	}
	return byKey
}
