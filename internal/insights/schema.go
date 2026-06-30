package insights

import _ "embed"

// analysisSchema is the JSON schema passed to `claude -p --json-schema`. It is a
// committed copy of the analyzing-agent-sessions skill schema; TestSchemaMatchesLiveSkill
// guards it against drift from the live skill.
//
//go:embed schema.json
var analysisSchema string
