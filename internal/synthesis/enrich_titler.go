package synthesis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
)

const titlePrompt = `You are given a JSON array of workflow recommendations, each {"index","type","statement"}. Write a short browsing title for each statement: imperative form (e.g. "Verify before claiming done"), at most 40 characters, no trailing period, no numbers or counts, distinct from every other title you produce, front-loaded so it stays meaningful truncated to 30 characters. Return {"titles":[{"index":<same index>,"title":"<title>"}]} covering every input exactly once.`

const titleSchema = `{
  "type": "object",
  "additionalProperties": false,
  "required": ["titles"],
  "properties": {
    "titles": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["index", "title"],
        "properties": {
          "index": {"type": "integer"},
          "title": {"type": "string", "minLength": 1}
        }
      }
    }
  }
}`

// NewClaudeTitler returns a Titler shelling out to `claude -p` with an inline
// prompt (no skill — the title rules fit in one instruction). workDir must be
// a neutral scratch directory the caller owns: the nested claude reads
// project config from its cwd, and the operator's cwd must never leak in.
func NewClaudeTitler(workDir string) Titler {
	return titlerFromRunner(func(ctx context.Context, stdin []byte) ([]byte, error) {
		cmd, err := newTitleCommand(ctx, stdin, "", workDir)
		if err != nil {
			return nil, err
		}
		out, err := cmd.Output()
		if err != nil {
			return out, wrapClaudeExit(out, err)
		}
		return out, nil
	})
}

// newTitleCommand builds the claude invocation that runs the inline titling
// prompt with structured output, mirroring newSynthesizeCommand. workDir is
// required, not optional: an empty workDir would silently fall back to the
// caller's cwd and whatever project config happens to be ambient there.
func newTitleCommand(ctx context.Context, stdin []byte, configDir, workDir string) (*exec.Cmd, error) {
	if workDir == "" {
		return nil, errors.New("titling workDir is empty: the caller must pass a neutral scratch cwd (skills.TempWorkdir)")
	}
	cmd := exec.CommandContext(ctx, "claude", "-p", titlePrompt,
		"--output-format", "json",
		"--json-schema", titleSchema,
		"--model", synthesisModel,
		"--no-session-persistence")
	cmd.Stdin = bytes.NewReader(stdin)
	cmd.WaitDelay = 30 * time.Second
	cmd.Env = scrubbedEnv()
	// Appended last so it wins over any inherited CLAUDE_CONFIG_DIR (os/exec
	// keeps the last duplicate).
	if configDir != "" {
		cmd.Env = append(cmd.Env, "CLAUDE_CONFIG_DIR="+configDir)
	}
	cmd.Dir = workDir
	return cmd, nil
}

func titlerFromRunner(run commandRunner) Titler {
	return func(ctx context.Context, reqs []TitleReq) (map[int]string, error) {
		stdin, err := json.Marshal(reqs)
		if err != nil {
			return nil, err
		}
		out, err := run(ctx, stdin)
		if err != nil {
			return nil, fmt.Errorf("titling command: %w", err)
		}
		raw, err := insights.ParseClaudeEnvelope(out)
		if err != nil {
			return nil, err
		}
		var payload struct {
			Titles []struct {
				Index int    `json:"index"`
				Title string `json:"title"`
			} `json:"titles"`
		}
		if err := json.Unmarshal(raw, &payload); err != nil {
			return nil, fmt.Errorf("structured_output parse: %w", err)
		}
		want := make(map[int]bool, len(reqs))
		for _, r := range reqs {
			want[r.Index] = true
		}
		m := make(map[int]string, len(payload.Titles))
		for _, t := range payload.Titles {
			if !want[t.Index] {
				return nil, fmt.Errorf("titler returned unrequested index %d", t.Index)
			}
			if _, dup := m[t.Index]; dup {
				return nil, fmt.Errorf("titler returned duplicate index %d", t.Index)
			}
			m[t.Index] = t.Title
		}
		if len(m) != len(want) {
			var missing []int
			for idx := range want {
				if _, ok := m[idx]; !ok {
					missing = append(missing, idx)
				}
			}
			sort.Ints(missing)
			return nil, fmt.Errorf("titler response missing indices %v", missing)
		}
		return m, nil
	}
}
