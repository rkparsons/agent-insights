package insights

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tmux-ctrl/internal/sources/claude"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestGolden(t *testing.T) {
	fixtures, err := filepath.Glob("testdata/*.jsonl")
	if err != nil || len(fixtures) == 0 {
		t.Fatalf("no fixtures found: %v", err)
	}
	for _, fixture := range fixtures {
		name := strings.TrimSuffix(filepath.Base(fixture), ".jsonl")
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(fixture)
			if err != nil {
				t.Fatal(err)
			}
			ev, c := claude.DecodeTranscript(strings.NewReader(string(data)))
			r := Extract(ev, c, name, noRepo)

			statsJSON, err := json.MarshalIndent(r.Stats, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			statsPath := strings.TrimSuffix(fixture, ".jsonl") + ".stats.json"
			reducedPath := strings.TrimSuffix(fixture, ".jsonl") + ".reduced.txt"

			if *update {
				if err := os.WriteFile(statsPath, statsJSON, 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(reducedPath, []byte(r.Reduced.Text), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}

			wantStats, err := os.ReadFile(statsPath)
			if err != nil {
				t.Fatalf("missing golden (run: go test -run TestGolden -update): %v", err)
			}
			if string(wantStats) != string(statsJSON) {
				t.Errorf("stats mismatch (run -update to refresh)\n--- got ---\n%s", statsJSON)
			}
			wantReduced, err := os.ReadFile(reducedPath)
			if err != nil {
				t.Fatalf("missing golden (run -update): %v", err)
			}
			if string(wantReduced) != r.Reduced.Text {
				t.Errorf("reduced mismatch (run -update to refresh)\n--- got ---\n%s", r.Reduced.Text)
			}
		})
	}
}
