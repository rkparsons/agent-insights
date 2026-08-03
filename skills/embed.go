// Package skills carries the agent skills the insights pipeline drives its
// nested `claude -p` calls with, embedded in the binary and materialized into a
// run's working directory as project-level skills (<workdir>/.claude/skills/<name>).
// Delivering them from the binary rather than the operator's ambient ~/.claude
// makes a run self-contained and its skill content hashable.
package skills

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

const (
	// AnalysisSkill is the L1 per-session analysis skill: its directory name,
	// and (prefixed with "/") the slash-command the nested claude is invoked with.
	AnalysisSkill = "analyzing-agent-sessions"
	// SynthesisSkill is the L2 per-repo synthesis skill.
	SynthesisSkill = "synthesizing-workflow-insights"
)

//go:embed all:analyzing-agent-sessions all:synthesizing-workflow-insights
var FS embed.FS

// Names lists the embedded skills, in a stable order.
func Names() []string { return []string{AnalysisSkill, SynthesisSkill} }

// AnalysisSchema is the L1 structured-output schema, single-sourced from the
// skill that documents it (both are handed to the same nested claude call).
func AnalysisSchema() []byte { return mustReadFile(AnalysisSkill + "/schema.json") }

// SynthesisSchema is the L2 structured-output schema.
func SynthesisSchema() []byte { return mustReadFile(SynthesisSkill + "/schema.json") }

// Materialize writes the embedded skills into dir as project-level skills:
// dir/.claude/skills/<name>/..., preserving relative paths. Claude Code
// resolves /<skill-name> from the session's cwd, so a nested claude run with
// cmd.Dir == dir finds them with no ~/.claude involvement.
func Materialize(dir string) error {
	root := filepath.Join(dir, ".claude", "skills")
	return fs.WalkDir(FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dst := filepath.Join(root, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		data, err := FS.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, data, 0o644)
	})
}

// TempWorkdir creates a scratch cwd for a run's nested claude calls with the
// skills materialized into it, returning the directory and its cleanup. The
// caller defers the cleanup: the dir only needs to outlive the run.
func TempWorkdir() (string, func(), error) {
	dir, err := os.MkdirTemp("", "agent-insights-run")
	if err != nil {
		return "", func() {}, err
	}
	cleanup := func() { os.RemoveAll(dir) }
	if err := Materialize(dir); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("materialize skills: %w", err)
	}
	return dir, cleanup, nil
}

// TreeHash content-hashes the embedded tree exactly as eval's hashTree
// hashes a directory — sorted relative path + size + bytes — so a hash taken
// here and one taken from a materialized copy are the same value.
func TreeHash() string {
	h, err := hashEmbedded()
	if err != nil { // impossible for a compiled-in FS
		panic("skills: hashing embedded tree: " + err.Error())
	}
	return h
}

func hashEmbedded() (string, error) {
	var rels []string
	if err := fs.WalkDir(FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rels = append(rels, p)
		return nil
	}); err != nil {
		return "", err
	}
	sort.Strings(rels)
	h := sha256.New()
	for _, rel := range rels {
		data, err := FS.ReadFile(rel)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%s\x00%d\x00", rel, len(data))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// mustReadFile reads an embedded file; a miss is a build-time mistake (the path
// is compiled in), never a runtime condition worth threading an error for.
func mustReadFile(name string) []byte {
	data, err := FS.ReadFile(name)
	if err != nil {
		panic("skills: " + err.Error())
	}
	return data
}
