package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// schemaFile is the published schema (editors reference it by URL).
var schemaFile = filepath.Join("..", "..", "..", "schema", "depgraph.schema.json")

func decodeSchema(t *testing.T) map[string]any {
	t.Helper()
	var s map[string]any
	if err := json.Unmarshal(Schema(), &s); err != nil {
		t.Fatalf("Schema() is not JSON: %v", err)
	}
	return s
}

func obj(t *testing.T, v any, path string) map[string]any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("%s: not an object: %#v", path, v)
	}
	return m
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// yamlKeys are the yaml tags of a struct type.
func yamlKeys(v any) []string {
	var out []string
	for f := range reflect.TypeOf(v).Fields() {
		if tag, _, _ := strings.Cut(f.Tag.Get("yaml"), ","); tag != "" && tag != "-" {
			out = append(out, tag)
		}
	}
	sort.Strings(out)
	return out
}

// The committed schema is the generated one: run `mise run schema` after
// changing a rule or an option.
func TestSchemaFileIsCurrent(t *testing.T) {
	b, err := os.ReadFile(schemaFile)
	if err != nil {
		t.Fatalf("%v (run mise run schema)", err)
	}
	if string(b) != string(Schema()) {
		t.Fatalf("%s is stale: run mise run schema", schemaFile)
	}
}

// The schema has exactly the keys the strict decoder accepts: what the
// schema allows parses, and the editor flags what the decoder rejects.
func TestSchemaMatchesTheDecoder(t *testing.T) {
	s := decodeSchema(t)
	if s["additionalProperties"] != false {
		t.Error("top level must not allow unknown keys")
	}
	props := obj(t, s["properties"], "properties")
	if got, want := keys(props), yamlKeys(fileYAML{}); !slices.Equal(got, want) {
		t.Errorf("top-level keys = %v, want %v", got, want)
	}
	store := obj(t, obj(t, props["store"], "store")["properties"], "store.properties")
	if got, want := keys(store), yamlKeys(StoreSettings{}); !slices.Equal(got, want) {
		t.Errorf("store keys = %v, want %v", got, want)
	}
	comps := obj(t, props["components"], "components")
	comp := obj(t, comps["additionalProperties"], "components.additionalProperties")
	if comp["additionalProperties"] != false {
		t.Error("a component must not allow unknown keys")
	}
	if got, want := keys(obj(t, comp["properties"], "components.*.properties")), yamlKeys(componentYAML{}); !slices.Equal(got, want) {
		t.Errorf("component keys = %v, want %v", got, want)
	}

	rs := obj(t, props["rules"], "rules")
	if rs["additionalProperties"] != false {
		t.Error("rules must not allow unknown rules")
	}
	rprops := obj(t, rs["properties"], "rules.properties")
	var names []string
	for _, r := range rules.Builtin() {
		names = append(names, r.Name)
	}
	sort.Strings(names)
	if got := keys(rprops); !slices.Equal(got, names) {
		t.Errorf("rules = %v, want %v", got, names)
	}
	optionUnion := map[string]bool{}
	for _, r := range rules.Builtin() {
		rule := obj(t, rprops[r.Name], "rules."+r.Name)
		if rule["additionalProperties"] != false {
			t.Errorf("rules.%s must not allow unknown options", r.Name)
		}
		want := append([]string{"enabled", "severity"}, r.Options...)
		sort.Strings(want)
		if got := keys(obj(t, rule["properties"], "rules."+r.Name+".properties")); !slices.Equal(got, want) {
			t.Errorf("rules.%s options = %v, want %v", r.Name, got, want)
		}
		if d, _ := rule["description"].(string); d == "" {
			t.Errorf("rules.%s has no description", r.Name)
		}
		for _, o := range r.Options {
			optionUnion[o] = true
		}
	}
	// every option the decoder reads belongs to some rule
	for _, k := range yamlKeys(ruleYAML{}) {
		if k != "enabled" && k != "severity" && !optionUnion[k] {
			t.Errorf("option %q is decoded but no rule accepts it", k)
		}
	}
}

// Only JSON Schema (draft-07) keywords are used, so a typo does not
// silently disable a check in the editor.
func TestSchemaUsesKnownKeywords(t *testing.T) {
	known := map[string]bool{
		"$schema": true, "$id": true, "title": true, "description": true, "type": true,
		"properties": true, "additionalProperties": true, "items": true, "enum": true,
		"minimum": true, "maximum": true, "exclusiveMinimum": true, "default": true,
		"examples": true, "uniqueItems": true, "minLength": true, "pattern": true, "oneOf": true, "required": true,
	}
	var walk func(v any, path string, inProps bool)
	walk = func(v any, path string, inProps bool) {
		switch x := v.(type) {
		case map[string]any:
			for k, c := range x {
				if !inProps && !known[k] {
					t.Errorf("%s: unknown keyword %q", path, k)
				}
				walk(c, path+"/"+k, k == "properties" && !inProps)
			}
		case []any:
			for _, c := range x {
				walk(c, path, false)
			}
		}
	}
	walk(decodeSchema(t), "#", false)
}

// The examples in the schema parse.
func TestSchemaDefaultsParse(t *testing.T) {
	doc := `
project: my-repo
layers:
  domain: ["pkg/domain/**"]
  infra: ["pkg/infrastructure/**"]
forbidden:
  domain: [infra]
rules:
  god-package: {severity: warn, fanIn: 10, fanOut: 3}
  duplicate-code: {minTokens: 50, similarity: 0.8}
  file-cycle: {deferred: false}
  duplicate-symbol: {enabled: false, ignore: [main]}
  unused-export: {ignore: ["pkg/api/**"]}
  unreachable-file: {entries: ["plugins/**"]}
ignore: ["**/testdata/**"]
store: {persist: true, provider: container, containerCli: podman}
`
	if _, err := Parse([]byte(doc)); err != nil {
		t.Fatal(err)
	}
}
