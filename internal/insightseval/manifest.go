package insightseval

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
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

// FreezeStats counts how FreezeCorpus disposed of each session/sidechain on
// this run: newly frozen, already-frozen-and-preserved-verbatim, or
// already-frozen-but-diverged-from-the-still-growing-live-file (informational
// only — Diverged sessions are still counted in AlreadyFrozen).
type FreezeStats struct {
	Frozen        int `json:"frozen"`
	AlreadyFrozen int `json:"already_frozen"`
	Diverged      int `json:"diverged"`
}

// loadManifest reads dataDir/manifest.json if present. ok is false (with a
// nil error) when there is no prior freeze to build on.
func loadManifest(dataDir string) (Manifest, bool, error) {
	raw, err := os.ReadFile(filepath.Join(dataDir, "manifest.json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Manifest{}, false, nil
		}
		return Manifest{}, false, err
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return Manifest{}, false, err
	}
	return m, true, nil
}

// FreezeCorpus copies every top-level transcript and subagent sidechain into
// dataDir (gzip, append-only) and writes manifest.json. byID joins pool
// analyses for repo-key/start; sessions without an analysis freeze with those
// fields empty.
//
// Re-runs are canonical on the existing manifest: every existing entry and
// sidechain is seeded into the new manifest first (verified against its
// frozen file's sha — the tamper check) regardless of whether its live source
// still exists, so a live transcript pruned between freezes never drops out
// of the manifest and gets reclassified as a gap. The live walk only ADDS
// ids/sidechain-keys not already present; it never removes one. FrozenAt is
// likewise carried over from the first freeze.
func FreezeCorpus(dataDir string, byID map[string]insights.AgentSessionAnalysis, frozenAt time.Time) (Manifest, FreezeStats, error) {
	var stats FreezeStats
	existing, hasExisting, err := loadManifest(dataDir)
	if err != nil {
		return Manifest{}, stats, fmt.Errorf("load existing manifest: %w", err)
	}
	m := Manifest{FrozenAt: frozenAt}
	if hasExisting {
		m.FrozenAt = existing.FrozenAt
	}
	existingEntries := make(map[string]ManifestEntry, len(existing.Entries))
	for _, e := range existing.Entries {
		existingEntries[e.SessionID] = e
	}
	existingSidechains := make(map[string]SidechainEntry, len(existing.Sidechains))
	for _, sc := range existing.Sidechains {
		existingSidechains[sc.ParentSessionID+"\x00"+sc.File] = sc
	}

	for _, prev := range existing.Entries {
		dst := filepath.Join(dataDir, "corpus", prev.SessionID+".jsonl.gz")
		if err := verifyFrozenUnchanged(dst, prev.SHA256, prev.SessionID); err != nil {
			return m, stats, err
		}
		m.Entries = append(m.Entries, prev)
		stats.AlreadyFrozen++
	}
	for _, prev := range existing.Sidechains {
		dst := filepath.Join(dataDir, "corpus-sidechains", prev.ParentSessionID, prev.File+".gz")
		if err := verifyFrozenUnchanged(dst, prev.SHA256, prev.ParentSessionID+"/"+prev.File); err != nil {
			return m, stats, err
		}
		m.Sidechains = append(m.Sidechains, prev)
		stats.AlreadyFrozen++
	}

	rawRefs, err := claude.WalkTranscripts()
	if err != nil {
		return m, stats, err
	}
	// A resume can copy an entire project dir (transcripts included) into a
	// second project dir, so the same session-id can surface twice; collapse
	// to one ref before freezing, newest Mtime wins — mirroring
	// claude.FindTranscript's documented newest-wins resolution.
	for _, r := range dedupeTranscriptRefs(rawRefs) {
		if prev, ok := existingEntries[r.SessionID]; ok {
			if info, statErr := os.Stat(r.Path); statErr == nil && info.Size() != prev.Bytes {
				stats.Diverged++
			}
			continue
		}
		dst := filepath.Join(dataDir, "corpus", r.SessionID+".jsonl.gz")
		sha, n, err := freezeFile(r.Path, dst)
		if err != nil {
			return m, stats, err
		}
		e := ManifestEntry{SessionID: r.SessionID, SHA256: sha, Mtime: r.Mtime, Bytes: n, SourcePath: r.Path}
		if a, ok := byID[r.SessionID]; ok {
			e.RepoKey = synthesis.RepoKey(a)
			e.Start = a.Stats.Start
		}
		m.Entries = append(m.Entries, e)
		stats.Frozen++
	}
	scs, err := listSidechains(claude.ProjectsDir())
	if err != nil {
		return m, stats, err
	}
	for _, sc := range scs {
		name := filepath.Base(sc.Path)
		key := sc.Parent + "\x00" + name
		if prev, ok := existingSidechains[key]; ok {
			if info, statErr := os.Stat(sc.Path); statErr == nil && info.Size() != prev.Bytes {
				stats.Diverged++
			}
			continue
		}
		dst := filepath.Join(dataDir, "corpus-sidechains", sc.Parent, name+".gz")
		sha, n, err := freezeFile(sc.Path, dst)
		if err != nil {
			return m, stats, err
		}
		m.Sidechains = append(m.Sidechains, SidechainEntry{
			ParentSessionID: sc.Parent, File: name, SHA256: sha, Mtime: sc.Mtime, Bytes: n, SourcePath: sc.Path,
		})
		stats.Frozen++
	}
	sort.Slice(m.Entries, func(i, j int) bool { return m.Entries[i].SessionID < m.Entries[j].SessionID })
	sort.Slice(m.Sidechains, func(i, j int) bool {
		if m.Sidechains[i].ParentSessionID != m.Sidechains[j].ParentSessionID {
			return m.Sidechains[i].ParentSessionID < m.Sidechains[j].ParentSessionID
		}
		return m.Sidechains[i].File < m.Sidechains[j].File
	})
	if err := writeJSON(filepath.Join(dataDir, "manifest.json"), m); err != nil {
		return m, stats, err
	}
	return m, stats, nil
}

// dedupeTranscriptRefs collapses refs sharing a session-id to one, newest
// Mtime wins.
func dedupeTranscriptRefs(refs []claude.TranscriptRef) []claude.TranscriptRef {
	bySession := make(map[string]claude.TranscriptRef, len(refs))
	for _, r := range refs {
		if prev, ok := bySession[r.SessionID]; !ok || r.Mtime.After(prev.Mtime) {
			bySession[r.SessionID] = r
		}
	}
	out := make([]claude.TranscriptRef, 0, len(bySession))
	for _, r := range bySession {
		out = append(out, r)
	}
	return out
}

// verifyFrozenUnchanged hard-errors, naming id, when the frozen file at dst is
// missing or its content no longer matches the manifest's recorded sha — the
// tamper check that lets re-runs treat frozen files as canonical without
// re-reading the (possibly since-grown) live transcript.
func verifyFrozenUnchanged(dst, wantSHA, id string) error {
	actual, err := frozenSHA(dst)
	if err != nil {
		return fmt.Errorf("frozen corpus tamper check failed for %s: %w", id, err)
	}
	if actual != wantSHA {
		return fmt.Errorf("frozen corpus tamper check failed for %s: sha256 mismatch", id)
	}
	return nil
}

type sidechainRef struct {
	Parent string
	Path   string
	Mtime  time.Time
}

// listSidechains finds every agent-* file under a subagents/ dir beneath
// projectsDir — not just the primary agent-*.jsonl transcript but sibling
// artifacts such as agent-*.meta.json. The parent session id is the path
// segment immediately before "subagents"
// (<project>/<parent-session-id>/subagents/agent-*).
//
// A missing projectsDir returns (nil, nil); any other error reading the root
// or a subtree (e.g. a permission-denied directory) propagates rather than
// being swallowed into a silent partial freeze.
func listSidechains(projectsDir string) ([]sidechainRef, error) {
	if _, err := os.Stat(projectsDir); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []sidechainRef
	err := filepath.WalkDir(projectsDir, func(p string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasPrefix(name, "agent-") {
			return nil
		}
		rel, relErr := filepath.Rel(projectsDir, p)
		if relErr != nil {
			return nil
		}
		parts := strings.Split(rel, string(filepath.Separator))
		parent := ""
		inSubagents := false
		for i, seg := range parts {
			if seg == "subagents" && i >= 1 {
				parent = parts[i-1]
				inSubagents = true
				break
			}
		}
		if !inSubagents {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		out = append(out, sidechainRef{Parent: parent, Path: p, Mtime: info.ModTime()})
		return nil
	})
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
