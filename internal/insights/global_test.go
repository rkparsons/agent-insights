package insights

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fullyPopulatedGlobalSynthesis exercises every field, including the two
// optional ones (EscalatedFrom, Asset.Target) the omitted-when-empty tests
// below set to their zero value instead.
func fullyPopulatedGlobalSynthesis() GlobalSynthesisJSON {
	return GlobalSynthesisJSON{
		SchemaVersion: 2,
		GeneratedAt:   time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC),
		Window:        WindowBoundsJSON{From: "2026-07-27", To: "2026-08-10"},
		Repos: []RepoStatsJSON{
			{Key: "alpha", Window: WindowBoundsJSON{From: "2026-07-27", To: "2026-08-10"}, SessionCount: 42, AnalyzedCount: 40},
		},
		Findings: []FindingJSON{
			{
				Rank:          1,
				Title:         "Escalate the draft-PR habit into a repo rule",
				Statement:     "Open a draft PR before requesting review.",
				RankRationale: "Already documented but still violated after the rule shipped.",
				Asset: AssetJSON{
					Type:    "placement_fix",
					Target:  "~/dev/alpha/CLAUDE.md",
					Content: "Open a draft PR before requesting review.",
				},
				Audience:    "user",
				EvidenceIDs: []string{"alpha/P5", "alpha/F9"},
				Quotes:      []string{"\"I keep opening these as ready for review\""},
				AlreadyAdopted: AdoptedJSON{
					Verdict:    "no",
					SourcePath: "~/dev/alpha/CLAUDE.md",
					Excerpt:    "Prefer draft PRs while a change is still in review.",
				},
				EscalatedFrom: &EscalatedFromJSON{
					SourcePath: "~/dev/alpha/CLAUDE.md",
					Excerpt:    "Prefer draft PRs while a change is still in review.",
				},
				Repos:        []string{"alpha"},
				SessionCount: 4,
				LastSeen:     "2026-08-06",
				ActedKey:     "c3d4e5f6a7b8c9d0",
			},
		},
		Dropped: []DroppedJSON{
			{
				Summary:     "Occasional editor lag while diffing large files",
				Reason:      "Environmental, not an actionable practice change.",
				EvidenceIDs: []string{"alpha/G4"},
			},
		},
		Meta: GlobalMetaJSON{
			Model:           "claude-fable-5",
			ValidationNotes: []string{"dropped a quote from the top-ranked finding: not found in the cited item's quote pool"},
		},
	}
}

// TestGlobalSynthesisJSONRoundTrip marshals a fully-populated GlobalSynthesisJSON
// and unmarshals it back, checking no field is lost or altered.
func TestGlobalSynthesisJSONRoundTrip(t *testing.T) {
	want := fullyPopulatedGlobalSynthesis()

	data, err := json.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got GlobalSynthesisJSON
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("round-trip mismatch:\nwant %+v\ngot  %+v", want, got)
	}
}

// TestGlobalFindingOmitsEscalatedFromWhenNil guards escalated_from's
// omitempty: a finding that escalates nothing must not emit the key at all.
func TestGlobalFindingOmitsEscalatedFromWhenNil(t *testing.T) {
	f := FindingJSON{
		Asset:          AssetJSON{Type: "habit"},
		AlreadyAdopted: AdoptedJSON{Verdict: "unknown"},
	}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "escalated_from") {
		t.Errorf("expected escalated_from omitted when nil, got: %s", data)
	}
}

// TestGlobalAssetOmitsTargetAndContentWhenEmpty guards the habit asset.type,
// whose deliverable is its statement — target/content are allowed empty.
func TestGlobalAssetOmitsTargetAndContentWhenEmpty(t *testing.T) {
	a := AssetJSON{Type: "habit"}
	data, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "target") {
		t.Errorf("expected asset.target omitted when empty, got: %s", data)
	}
	if strings.Contains(string(data), "content") {
		t.Errorf("expected asset.content omitted when empty, got: %s", data)
	}
}

// TestGoldenShowV2RoundTrips is TestGoldenStatusRoundTrips's v2 sibling
// (see goldens_test.go): decodes schemas/goldens/show.golden.json into
// GlobalSynthesisJSON, rejecting unknown fields, checks re-marshaling
// round-trips, then validates schemas/show.schema.json against the struct
// shape and schema_version.
func TestGoldenShowV2RoundTrips(t *testing.T) {
	golden := readGolden(t, "show.golden.json")

	var show GlobalSynthesisJSON
	dec := json.NewDecoder(bytes.NewReader(golden))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&show); err != nil {
		t.Fatalf("decode golden into GlobalSynthesisJSON: %v", err)
	}

	remarshaled, err := json.Marshal(show)
	if err != nil {
		t.Fatal(err)
	}
	assertCanonicallyEqual(t, golden, remarshaled)
	schema := loadSchema(t, "show.schema.json")
	assertSchemaMatchesStruct(t, schema, reflect.TypeOf(GlobalSynthesisJSON{}))
	assertSchemaVersion(t, schema, show.SchemaVersion)
}

// TestRawFindingSharesFindingJSONTags guards the "JSON tags identical for
// shared fields" contract between FindingJSON and RawFinding (the model-
// emitted shape minus Go-owned fields): a tag typo here would silently
// desync the verifier (Task 5) from the wire type.
func TestRawFindingSharesFindingJSONTags(t *testing.T) {
	assertSharedTags(t, reflect.TypeOf(FindingJSON{}), reflect.TypeOf(RawFinding{}))
}

// TestRawGlobalSynthesisSharesGlobalSynthesisJSONTags is
// TestRawFindingSharesFindingJSONTags's envelope-level sibling.
func TestRawGlobalSynthesisSharesGlobalSynthesisJSONTags(t *testing.T) {
	assertSharedTags(t, reflect.TypeOf(GlobalSynthesisJSON{}), reflect.TypeOf(RawGlobalSynthesis{}))
}

// assertSharedTags checks every field of raw has a same-named field in full
// with an identical json tag (raw is allowed to be a subset of full's fields;
// full is allowed extra Go-owned fields raw doesn't carry).
func assertSharedTags(t *testing.T, full, raw reflect.Type) {
	t.Helper()
	for i := 0; i < raw.NumField(); i++ {
		rf := raw.Field(i)
		ff, ok := full.FieldByName(rf.Name)
		if !ok {
			t.Errorf("%s.%s has no counterpart field in %s", raw.Name(), rf.Name, full.Name())
			continue
		}
		if rf.Tag.Get("json") != ff.Tag.Get("json") {
			t.Errorf("%s.%s json tag %q != %s.%s json tag %q", raw.Name(), rf.Name, rf.Tag.Get("json"), full.Name(), ff.Name, ff.Tag.Get("json"))
		}
	}
}
