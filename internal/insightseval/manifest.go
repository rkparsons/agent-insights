package insightseval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"tmux-ctrl/internal/insights"
	"tmux-ctrl/internal/sources/claude"
	"tmux-ctrl/internal/synthesis"
)

type ManifestEntry struct {
	SessionID  string    `json:"session_id"`
	SHA256     string    `json:"sha256"`
	RepoKey    string    `json:"repo_key,omitempty"`
	Start      time.Time `json:"start"`
	Mtime      time.Time `json:"mtime"`
	Bytes      int64     `json:"bytes"`
	SourcePath string    `json:"source_path"`
}

type SidechainEntry struct {
	ParentSessionID string    `json:"parent_session_id"`
	File            string    `json:"file"`
	SHA256          string    `json:"sha256"`
	Mtime           time.Time `json:"mtime"`
	Bytes           int64     `json:"bytes"`
	SourcePath      string    `json:"source_path"`
}

type Manifest struct {
	FrozenAt   time.Time        `json:"frozen_at"`
	Entries    []ManifestEntry  `json:"entries"`
	Sidechains []SidechainEntry `json:"sidechains"`
}

// FreezeCorpus copies every top-level transcript and subagent sidechain into
// dataDir (gzip, append-only) and writes manifest.json. byID joins pool
// analyses for repo-key/start; sessions without an analysis freeze with those
// fields empty.
func FreezeCorpus(dataDir string, byID map[string]insights.AgentSessionAnalysis, frozenAt time.Time) (Manifest, error) {
	m := Manifest{FrozenAt: frozenAt}
	refs, err := claude.WalkTranscripts()
	if err != nil {
		return m, err
	}
	for _, r := range refs {
		dst := filepath.Join(dataDir, "corpus", r.SessionID+".jsonl.gz")
		sha, n, err := freezeFile(r.Path, dst)
		if err != nil {
			return m, err
		}
		e := ManifestEntry{SessionID: r.SessionID, SHA256: sha, Mtime: r.Mtime, Bytes: n, SourcePath: r.Path}
		if a, ok := byID[r.SessionID]; ok {
			e.RepoKey = synthesis.RepoKey(a)
			e.Start = a.Stats.Start
		}
		m.Entries = append(m.Entries, e)
	}
	scs, err := listSidechains(claude.ProjectsDir())
	if err != nil {
		return m, err
	}
	for _, sc := range scs {
		name := filepath.Base(sc.Path)
		dst := filepath.Join(dataDir, "corpus-sidechains", sc.Parent, name+".gz")
		sha, n, err := freezeFile(sc.Path, dst)
		if err != nil {
			return m, err
		}
		m.Sidechains = append(m.Sidechains, SidechainEntry{
			ParentSessionID: sc.Parent, File: name, SHA256: sha, Mtime: sc.Mtime, Bytes: n, SourcePath: sc.Path,
		})
	}
	sort.Slice(m.Entries, func(i, j int) bool { return m.Entries[i].SessionID < m.Entries[j].SessionID })
	sort.Slice(m.Sidechains, func(i, j int) bool {
		if m.Sidechains[i].ParentSessionID != m.Sidechains[j].ParentSessionID {
			return m.Sidechains[i].ParentSessionID < m.Sidechains[j].ParentSessionID
		}
		return m.Sidechains[i].File < m.Sidechains[j].File
	})
	if err := writeJSON(filepath.Join(dataDir, "manifest.json"), m); err != nil {
		return m, err
	}
	return m, nil
}

type sidechainRef struct {
	Parent string
	Path   string
	Mtime  time.Time
}

// listSidechains finds every agent-*.jsonl under projectsDir. The parent
// session id is the path segment immediately before "subagents"
// (<project>/<parent-session-id>/subagents/agent-*.jsonl).
func listSidechains(projectsDir string) ([]sidechainRef, error) {
	var out []sidechainRef
	err := filepath.WalkDir(projectsDir, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasPrefix(name, "agent-") || !strings.HasSuffix(name, ".jsonl") {
			return nil
		}
		rel, relErr := filepath.Rel(projectsDir, p)
		if relErr != nil {
			return nil
		}
		parts := strings.Split(rel, string(filepath.Separator))
		parent := ""
		for i, seg := range parts {
			if seg == "subagents" && i >= 1 {
				parent = parts[i-1]
				break
			}
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		out = append(out, sidechainRef{Parent: parent, Path: p, Mtime: info.ModTime()})
		return nil
	})
	if os.IsNotExist(err) {
		return nil, nil
	}
	return out, err
}

// writeJSON atomically writes indented JSON (same temp-file dance as the
// insights store).
func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
