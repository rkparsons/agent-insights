// External test package: internal/insights imports skills (run.go), so an
// in-package test importing insights would cycle.
package skills_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/rkparsons/agent-insights/internal/insights"
	"github.com/rkparsons/agent-insights/skills"
)

// TestSynthesisSchemaMatchesRawGlobalSynthesis pins the model-facing L2
// schema to insights.RawGlobalSynthesis field-for-field: every struct field
// appears as a schema property (and nothing else does), and schema `required`
// mirrors the non-omitempty tags. The one sanctioned divergence is the
// top-level `meta`: Go-owned (the run stamps model, the verifier appends
// validation_notes), so the schema keeps it optional for the model.
func TestSynthesisSchemaMatchesRawGlobalSynthesis(t *testing.T) {
	var root map[string]any
	if err := json.Unmarshal(skills.SynthesisSchema(), &root); err != nil {
		t.Fatalf("synthesis schema is not valid JSON: %v", err)
	}
	notRequired := map[string]bool{"meta": true}
	assertNodeMatchesStruct(t, root, root, reflect.TypeOf(insights.RawGlobalSynthesis{}), notRequired)
}

func assertNodeMatchesStruct(t *testing.T, root, node map[string]any, typ reflect.Type, exempt map[string]bool) {
	t.Helper()
	if ref, ok := node["$ref"].(string); ok {
		const prefix = "#/definitions/"
		defs, _ := root["definitions"].(map[string]any)
		def, ok := defs[strings.TrimPrefix(ref, prefix)].(map[string]any)
		if !strings.HasPrefix(ref, prefix) || !ok {
			t.Fatalf("unresolved $ref %q (only #/definitions/* is resolved)", ref)
		}
		node = def
	}

	props, _ := node["properties"].(map[string]any)
	schemaRequired := map[string]bool{}
	if reqList, ok := node["required"].([]any); ok {
		for _, r := range reqList {
			schemaRequired[r.(string)] = true
		}
	}

	structProps := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		tag := strings.Split(f.Tag.Get("json"), ",")
		name := tag[0]
		structProps[name] = true
		omitempty := len(tag) > 1 && tag[1] == "omitempty"

		if _, ok := props[name]; !ok {
			t.Errorf("%s.%s: schema property %q missing", typ.Name(), f.Name, name)
			continue
		}
		wantRequired := !omitempty && !exempt[name]
		if wantRequired != schemaRequired[name] {
			t.Errorf("%s: schema required[%q]=%v, struct (omitempty=%v, exempt=%v) wants %v",
				typ.Name(), name, schemaRequired[name], omitempty, exempt[name], wantRequired)
		}

		elemType := f.Type
		for elemType.Kind() == reflect.Pointer || elemType.Kind() == reflect.Slice {
			elemType = elemType.Elem()
		}
		if elemType.Kind() != reflect.Struct {
			continue
		}
		child, _ := props[name].(map[string]any)
		if f.Type.Kind() == reflect.Slice {
			items, ok := child["items"].(map[string]any)
			if !ok {
				t.Errorf("%s.%s: schema property %q is an array but has no object `items`", typ.Name(), f.Name, name)
				continue
			}
			child = items
		}
		assertNodeMatchesStruct(t, root, child, elemType, nil)
	}
	for name := range props {
		if !structProps[name] {
			t.Errorf("%s: schema property %q has no struct field", typ.Name(), name)
		}
	}
	for name := range schemaRequired {
		if !structProps[name] {
			t.Errorf("%s: schema requires %q, which has no struct field", typ.Name(), name)
		}
	}
}
