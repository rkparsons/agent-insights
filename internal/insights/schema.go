package insights

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
)

// analysisSchema is the JSON schema passed to `claude -p --json-schema`. It is a
// committed copy of the analyzing-agent-sessions skill schema; TestSchemaMatchesLiveSkill
// guards it against drift from the live skill.
//
//go:embed schema.json
var analysisSchema string

// SchemaHash returns the sha256 (hex) of the embedded L1 analysis schema, for
// eval cache keys and reproducibility records.
func SchemaHash() string {
	sum := sha256.Sum256([]byte(analysisSchema))
	return hex.EncodeToString(sum[:])
}
