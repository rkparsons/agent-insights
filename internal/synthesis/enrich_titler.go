package synthesis

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
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
		cmd := exec.CommandContext(ctx, "claude", "-p", titlePrompt,
			"--output-format", "json",
			"--json-schema", titleSchema,
			"--model", synthesisModel,
			"--no-session-persistence")
		cmd.Stdin = bytes.NewReader(stdin)
		cmd.WaitDelay = 30 * time.Second
		cmd.Env = scrubbedEnv()
		cmd.Dir = workDir
		out, err := cmd.Output()
		if err != nil {
			return out, wrapClaudeExit(out, err)
		}
		return out, nil
	})
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
		var env claudeEnvelope
		if err := json.Unmarshal(out, &env); err != nil {
			return nil, fmt.Errorf("malformed envelope: %w", err)
		}
		if env.IsError {
			return nil, fmt.Errorf("claude reported is_error: %s", env.Result)
		}
		if len(env.StructuredOutput) == 0 || string(env.StructuredOutput) == "null" {
			return nil, fmt.Errorf("null/missing structured_output")
		}
		var payload struct {
			Titles []struct {
				Index int    `json:"index"`
				Title string `json:"title"`
			} `json:"titles"`
		}
		if err := json.Unmarshal(env.StructuredOutput, &payload); err != nil {
			return nil, fmt.Errorf("structured_output parse: %w", err)
		}
		m := make(map[int]string, len(payload.Titles))
		for _, t := range payload.Titles {
			m[t.Index] = t.Title
		}
		return m, nil
	}
}
