package insights

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/rkparsons/agent-insights/skills"
)

// analysisSchema is the JSON schema passed to `claude -p --json-schema`,
// single-sourced from the embedded analyzing-agent-sessions skill so the schema
// and the prompt that documents it cannot drift apart.
var analysisSchema = string(skills.AnalysisSchema())

// SchemaHash returns the sha256 (hex) of the L1 analysis schema, for eval cache
// keys and reproducibility records.
func SchemaHash() string {
	sum := sha256.Sum256([]byte(analysisSchema))
	return hex.EncodeToString(sum[:])
}
