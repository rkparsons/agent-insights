package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ProjectsDir is the root of Claude Code's per-project transcript store. Honors
// AGENT_INSIGHTS_PROJECTS_DIR so tests can point at a temp tree.
func ProjectsDir() string {
	if d := os.Getenv("AGENT_INSIGHTS_PROJECTS_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "projects")
}

// TranscriptRef locates one session transcript without decoding it.
type TranscriptRef struct {
	SessionID string
	Path      string
	Mtime     time.Time
}

// LoadTranscript decodes one transcript file and returns its modification time.
func LoadTranscript(path string) (events []TranscriptEvent, canary Canary, mtime time.Time, err error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, Canary{}, time.Time{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, Canary{}, time.Time{}, err
	}
	defer f.Close()
	events, canary = DecodeTranscript(f)
	return events, canary, fi.ModTime(), nil
}

// WalkTranscripts enumerates top-level session transcripts (<project>/<id>.jsonl).
// It deliberately skips the subagents/ subtree (those are SubagentRun transcripts,
// not sessions) and any filename that is not a bare session-id .jsonl.
func WalkTranscripts() ([]TranscriptRef, error) {
	root := ProjectsDir()
	projects, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var refs []TranscriptRef
	for _, p := range projects {
		if !p.IsDir() {
			continue
		}
		dir := filepath.Join(root, p.Name())
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue // skips subagents/
			}
			id, ok := sessionIDFromFile(e.Name())
			if !ok {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			refs = append(refs, TranscriptRef{SessionID: id, Path: filepath.Join(dir, e.Name()), Mtime: info.ModTime()})
		}
	}
	return refs, nil
}

// FindTranscript resolves a session-id to its newest transcript. A resume copies a
// transcript, so the same id can live in more than one project dir; the newest wins
// and a warning is emitted.
func FindTranscript(sessionID string) (TranscriptRef, error) {
	matches, err := filepath.Glob(filepath.Join(ProjectsDir(), "*", sessionID+".jsonl"))
	if err != nil {
		return TranscriptRef{}, err
	}
	var refs []TranscriptRef
	for _, m := range matches {
		fi, err := os.Stat(m)
		if err != nil {
			continue
		}
		refs = append(refs, TranscriptRef{SessionID: sessionID, Path: m, Mtime: fi.ModTime()})
	}
	if len(refs) == 0 {
		return TranscriptRef{}, fmt.Errorf("no transcript found for session %q under %s", sessionID, ProjectsDir())
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Mtime.After(refs[j].Mtime) })
	if len(refs) > 1 {
		fmt.Fprintf(os.Stderr, "insights: %d transcripts for session %q; using newest %s\n", len(refs), sessionID, refs[0].Path)
	}
	return refs[0], nil
}

// sessionIDFromFile accepts "<id>.jsonl" and rejects "agent-*.jsonl" and non-jsonl.
func sessionIDFromFile(name string) (string, bool) {
	if !strings.HasSuffix(name, ".jsonl") {
		return "", false
	}
	id := strings.TrimSuffix(name, ".jsonl")
	if id == "" || strings.HasPrefix(id, "agent-") {
		return "", false
	}
	return id, true
}
