package synthesis

import (
	"context"
	"os"
	"testing"
)

func TestClaudeSynthesizerParsesEnvelope(t *testing.T) {
	data, err := os.ReadFile("testdata/envelope.json")
	if err != nil {
		t.Fatal(err)
	}
	s := claudeSynthesizer{run: func(ctx context.Context, stdin []byte) ([]byte, error) { return data, nil }}
	raw, err := s.Synthesize(context.Background(), EvidenceBundle{Repo: "client-project"})
	if err != nil {
		t.Fatalf("Synthesize: %v", err)
	}
	if len(raw.Themes) != 1 || raw.Themes[0].Kind != "friction" {
		t.Fatalf("themes = %+v", raw.Themes)
	}
	if raw.Themes[0].EvidenceIDs[1] != "F2" {
		t.Errorf("evidence_ids = %v, want [F1 F2]", raw.Themes[0].EvidenceIDs)
	}
	if len(raw.Recommendations) != 1 || raw.Recommendations[0].Type != "claude_md_rule" {
		t.Errorf("recs = %+v", raw.Recommendations)
	}
}

func TestClaudeSynthesizerNullStructuredOutput(t *testing.T) {
	s := claudeSynthesizer{run: func(ctx context.Context, stdin []byte) ([]byte, error) {
		return []byte(`{"is_error": false, "result": "", "structured_output": null}`), nil
	}}
	if _, err := s.Synthesize(context.Background(), EvidenceBundle{}); err == nil {
		t.Error("expected error on null structured_output")
	}
}
