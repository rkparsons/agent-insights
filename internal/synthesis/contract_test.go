package synthesis

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rkparsons/agent-insights/internal/insights"
)

func TestBuildShowJSONReturnsTheSnapshot(t *testing.T) {
	snap := globalFixture(time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC), "test-model")
	show := BuildShowJSON(snap, true)
	if show.SchemaVersion != 3 || show.Meta.Model != "test-model" || len(show.Findings) != 1 {
		t.Fatalf("show payload = %+v, want the stored snapshot itself", show)
	}
	if show.Findings[0].ActedKey != snap.Findings[0].ActedKey {
		t.Errorf("acted_key = %q, want the stored %q (Go owns it at verification, not here)", show.Findings[0].ActedKey, snap.Findings[0].ActedKey)
	}
}

// TestBuildShowJSONNeverRun covers the degraded state the TUI renders before a
// first run: a well-formed empty envelope at the current contract version,
// never a null-array payload.
func TestBuildShowJSONNeverRun(t *testing.T) {
	show := BuildShowJSON(insights.GlobalSynthesisJSON{}, false)
	if show.SchemaVersion != insights.ContractVersion {
		t.Errorf("schema_version = %d, want %d", show.SchemaVersion, insights.ContractVersion)
	}
	data, err := json.Marshal(show)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "null") {
		t.Errorf("never-run payload must not carry null arrays: %s", data)
	}
}

// TestBuildShowJSONPreservesStoredSchemaVersion is the version-skew path: a
// snapshot written by a different binary keeps its own schema_version so the
// consumer can name the skew instead of silently mis-rendering it.
func TestBuildShowJSONPreservesStoredSchemaVersion(t *testing.T) {
	snap := globalFixture(time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC), "m")
	snap.SchemaVersion = 2
	if show := BuildShowJSON(snap, true); show.SchemaVersion != 2 {
		t.Errorf("schema_version = %d, want the stored 2", show.SchemaVersion)
	}
}

// TestBuildShowJSONNormalizesNilArrays guards the contract's required arrays
// against a snapshot whose optional slices round-tripped as nil.
func TestBuildShowJSONNormalizesNilArrays(t *testing.T) {
	snap := insights.GlobalSynthesisJSON{SchemaVersion: 2, Findings: []insights.FindingJSON{{Title: "t"}}}
	data, err := json.Marshal(BuildShowJSON(snap, true))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "null") {
		t.Errorf("nil slices must normalize to []: %s", data)
	}
}
