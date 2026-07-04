package synthesis

import (
	"os"
	"path/filepath"
	"strings"
)

var adoptStopwords = map[string]bool{
	"the": true, "a": true, "an": true, "to": true, "of": true, "and": true, "or": true,
	"for": true, "with": true, "that": true, "this": true, "when": true, "before": true,
	"after": true, "is": true, "are": true, "be": true, "in": true, "on": true, "do": true,
	"not": true, "no": true, "your": true, "you": true, "it": true, "as": true, "into": true,
}

// salientTerms extracts lowercased content words (>=4 runes, non-stopword) from a statement.
func salientTerms(statement string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(statement), func(r rune) bool {
		return !(r >= 'a' && r <= 'z')
	}) {
		if len([]rune(w)) >= 4 && !adoptStopwords[w] {
			out = append(out, w)
		}
	}
	return out
}

// NewAdoptCheckerFromFiles returns a checker: "yes" if a majority of a rec's salient
// terms appear in the concatenated corpus, "no" if terms exist but < majority match,
// "unknown" if the corpus is unreadable or the statement has no salient terms.
// Exported so the eval harness can point it at the frozen config-snapshot.
func NewAdoptCheckerFromFiles(paths []string) AdoptChecker {
	var corpus strings.Builder
	readable := false
	for _, p := range paths {
		if data, err := os.ReadFile(p); err == nil {
			corpus.Write(data)
			corpus.WriteByte('\n')
			readable = true
		}
	}
	hay := strings.ToLower(corpus.String())
	return func(rec Recommendation) string {
		terms := salientTerms(rec.Statement)
		if !readable || len(terms) == 0 {
			return "unknown"
		}
		hits := 0
		for _, t := range terms {
			if strings.Contains(hay, t) {
				hits++
			}
		}
		if hits*2 >= len(terms) {
			return "yes"
		}
		return "no"
	}
}

// AdoptPathsUnder is AdoptPaths against explicit roots: globalRoot stands in
// for ~/.claude and repoRoot for the repo checkout. The eval harness points
// these at its frozen config snapshot, so the path-selection logic stays
// inside this package's code-version hash.
func AdoptPathsUnder(globalRoot, repoRoot string) []string {
	paths := []string{
		filepath.Join(repoRoot, "CLAUDE.md"),
		filepath.Join(globalRoot, "CLAUDE.md"),
		filepath.Join(globalRoot, "settings.json"),
	}
	for _, root := range []string{filepath.Join(repoRoot, ".claude"), filepath.Join(globalRoot, "skills")} {
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(p, ".md") {
				paths = append(paths, p)
			}
			return nil
		})
	}
	return paths
}

// AdoptPaths returns the exact files the already-adopted check greps, in order:
// repo CLAUDE.md, global CLAUDE.md, global settings.json, then repo-local and
// global skill/command markdown. Exported so the eval freeze can snapshot the
// same corpus the checker reads.
func AdoptPaths(repoPath string) []string {
	home, _ := os.UserHomeDir()
	return AdoptPathsUnder(filepath.Join(home, ".claude"), repoPath)
}

// NewAdoptChecker greps the repo's and the global CLAUDE.md/settings/skills corpus.
func NewAdoptChecker(repoPath string) AdoptChecker {
	return NewAdoptCheckerFromFiles(AdoptPaths(repoPath))
}
