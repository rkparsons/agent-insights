package insights

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestGoldenStatusRoundTrips decodes schemas/goldens/status.json into
// StatusJSON, rejecting any field the contract doesn't declare, then checks
// re-marshaling produces the same JSON (compared canonicalized, since the
// golden's hand-written formatting won't byte-match json.Marshal's output).
func TestGoldenStatusRoundTrips(t *testing.T) {
	golden := readGolden(t, "status.json")

	var status StatusJSON
	dec := json.NewDecoder(bytes.NewReader(golden))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&status); err != nil {
		t.Fatalf("decode golden into StatusJSON: %v", err)
	}

	remarshaled, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	assertCanonicallyEqual(t, golden, remarshaled)
	assertSchemaMatchesStruct(t, loadSchema(t, "status.schema.json"), reflect.TypeOf(StatusJSON{}))
}

// TestGoldenShowRoundTrips is TestGoldenStatusRoundTrips's ShowJSON sibling.
func TestGoldenShowRoundTrips(t *testing.T) {
	golden := readGolden(t, "show.json")

	var show ShowJSON
	dec := json.NewDecoder(bytes.NewReader(golden))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&show); err != nil {
		t.Fatalf("decode golden into ShowJSON: %v", err)
	}

	remarshaled, err := json.Marshal(show)
	if err != nil {
		t.Fatal(err)
	}
	assertCanonicallyEqual(t, golden, remarshaled)
	assertSchemaMatchesStruct(t, loadSchema(t, "show.schema.json"), reflect.TypeOf(ShowJSON{}))
}

func readGolden(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("../../schemas/goldens/" + name)
	if err != nil {
		t.Fatalf("read golden %s: %v (run from repo root: schemas/goldens/%s)", name, err, name)
	}
	return data
}

func loadSchema(t *testing.T, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile("../../schemas/" + name)
	if err != nil {
		t.Fatalf("read schema %s: %v", name, err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse schema %s: %v", name, err)
	}
	return m
}

// assertCanonicallyEqual compares two JSON byte strings structurally: both
// sides are decoded into `any` and re-marshaled, which normalizes whitespace,
// key order, and number formatting so a hand-formatted golden can be compared
// against json.Marshal's compact output.
func assertCanonicallyEqual(t *testing.T, want, got []byte) {
	t.Helper()
	if !bytes.Equal(canonicalizeJSON(t, want), canonicalizeJSON(t, got)) {
		t.Errorf("golden round-trip mismatch:\n--- golden ---\n%s\n--- re-marshaled ---\n%s", want, got)
	}
}

func canonicalizeJSON(t *testing.T, data []byte) []byte {
	t.Helper()
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("canonicalize: %v", err)
	}
	return out
}

var timeType = reflect.TypeOf(time.Time{})

// assertSchemaMatchesStruct is the "no dependency" stand-in for a JSON Schema
// validator: it recursively checks that a schema object node's declared
// `properties`/`required` key sets equal typ's json-tagged field set (fields
// without `,omitempty` are required; the rest are optional), then recurses
// into nested struct/slice-of-struct/pointer-to-struct fields, following
// `$ref` into the schema's `definitions`. This walks the contract types
// directly rather than any one golden instance, so it holds regardless of
// which optional fields a given golden happens to populate.
func assertSchemaMatchesStruct(t *testing.T, root map[string]any, typ reflect.Type) {
	t.Helper()
	assertSchemaNodeMatchesStruct(t, root, root, typ)
}

func assertSchemaNodeMatchesStruct(t *testing.T, root, node map[string]any, typ reflect.Type) {
	t.Helper()
	node = resolveSchemaRef(t, root, node)

	props, _ := node["properties"].(map[string]any)
	schemaProps := keySet(props)
	schemaRequired := map[string]bool{}
	if reqList, ok := node["required"].([]any); ok {
		for _, r := range reqList {
			schemaRequired[r.(string)] = true
		}
	}

	structProps := map[string]bool{}
	structRequired := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.PkgPath != "" { // unexported
			continue
		}
		name, omitempty := jsonTagName(f)
		if name == "-" {
			continue
		}
		structProps[name] = true
		if !omitempty {
			structRequired[name] = true
		}

		elemType := f.Type
		for elemType.Kind() == reflect.Pointer || elemType.Kind() == reflect.Slice {
			elemType = elemType.Elem()
		}
		if elemType.Kind() != reflect.Struct || elemType == timeType {
			continue
		}
		propNode, ok := props[name].(map[string]any)
		if !ok {
			t.Errorf("%s.%s: schema property %q missing or not an object", typ.Name(), f.Name, name)
			continue
		}
		childNode := propNode
		if isSliceField(f.Type) {
			items, ok := propNode["items"].(map[string]any)
			if !ok {
				t.Errorf("%s.%s: schema property %q is an array but has no object `items`", typ.Name(), f.Name, name)
				continue
			}
			childNode = items
		}
		assertSchemaNodeMatchesStruct(t, root, childNode, elemType)
	}

	if diff := setDiff(schemaProps, structProps); diff != "" {
		t.Errorf("%s: schema properties vs struct fields mismatch: %s", typ.Name(), diff)
	}
	if diff := setDiff(schemaRequired, structRequired); diff != "" {
		t.Errorf("%s: schema required vs struct non-omitempty fields mismatch: %s", typ.Name(), diff)
	}
}

func isSliceField(t reflect.Type) bool {
	if t.Kind() == reflect.Slice {
		return true
	}
	return t.Kind() == reflect.Pointer && t.Elem().Kind() == reflect.Slice
}

func resolveSchemaRef(t *testing.T, root, node map[string]any) map[string]any {
	t.Helper()
	ref, ok := node["$ref"].(string)
	if !ok {
		return node
	}
	const prefix = "#/definitions/"
	if !strings.HasPrefix(ref, prefix) {
		t.Fatalf("unsupported $ref %q (only #/definitions/* is resolved)", ref)
	}
	defs, _ := root["definitions"].(map[string]any)
	def, ok := defs[strings.TrimPrefix(ref, prefix)].(map[string]any)
	if !ok {
		t.Fatalf("unresolved %q", ref)
	}
	return def
}

func jsonTagName(f reflect.StructField) (name string, omitempty bool) {
	tag := f.Tag.Get("json")
	parts := strings.Split(tag, ",")
	name = parts[0]
	if name == "" {
		name = f.Name
	}
	for _, opt := range parts[1:] {
		if opt == "omitempty" {
			omitempty = true
		}
	}
	return name, omitempty
}

func keySet(m map[string]any) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

// setDiff reports missing/extra keys between two sets, or "" if equal.
func setDiff(want, got map[string]bool) string {
	var missing, extra []string
	for k := range want {
		if !got[k] {
			missing = append(missing, k)
		}
	}
	for k := range got {
		if !want[k] {
			extra = append(extra, k)
		}
	}
	if len(missing) == 0 && len(extra) == 0 {
		return ""
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return "missing from struct: " + strings.Join(missing, ",") + "; not in schema: " + strings.Join(extra, ",")
}
