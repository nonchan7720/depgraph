package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

func TestLoadMissingIsDefault(t *testing.T) {
	f, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if f.Project != "" || !reflect.DeepEqual(f.Rules, rules.DefaultConfig()) {
		t.Errorf("missing file = %+v", f)
	}
}

func TestParseEmptyIsDefault(t *testing.T) {
	for _, src := range []string{"", "# only a comment\n"} {
		f, err := Parse([]byte(src))
		if err != nil || !reflect.DeepEqual(f.Rules, rules.DefaultConfig()) {
			t.Errorf("%q = %+v, %v", src, f, err)
		}
	}
}

func TestParseProjectOnlyKeepsDefaults(t *testing.T) {
	f, err := Parse([]byte("project: my-key\n"))
	if err != nil || f.Project != "my-key" || !reflect.DeepEqual(f.Rules, rules.DefaultConfig()) {
		t.Errorf("= %+v, %v", f, err)
	}
}

func TestParseFull(t *testing.T) {
	f, err := Parse([]byte(`
project: app
layers:
  interfaces: ["pkg/interfaces/**", "cmd/**"]
  domain: [pkg/domain/**]
  infrastructure: [pkg/infrastructure/**]
forbidden:
  domain: [infrastructure, interfaces]
  infrastructure: [interfaces]
rules:
  god-package: {severity: info, fanIn: 15}
  duplicate-symbol: {ignore: [main, New]}
  import-cycle: {severity: warn}
  layer-violation: {enabled: true}
ignore: ["**/testdata/**"]
`))
	if err != nil {
		t.Fatal(err)
	}
	c := f.Rules
	wantLayers := []rules.Layer{
		{Name: "domain", Packages: []string{"pkg/domain/**"}},
		{Name: "infrastructure", Packages: []string{"pkg/infrastructure/**"}},
		{Name: "interfaces", Packages: []string{"pkg/interfaces/**", "cmd/**"}},
	}
	if !reflect.DeepEqual(c.Layers, wantLayers) {
		t.Errorf("layers = %+v", c.Layers)
	}
	wantPairs := [][2]string{{"domain", "infrastructure"}, {"domain", "interfaces"}, {"infrastructure", "interfaces"}}
	if !reflect.DeepEqual(c.ForbiddenLayers, wantPairs) {
		t.Errorf("forbidden = %v", c.ForbiddenLayers)
	}
	if c.GodPackageFanIn != 15 || !reflect.DeepEqual(c.IgnoreDuplicateNames, []string{"main", "New"}) {
		t.Errorf("options = %d %v", c.GodPackageFanIn, c.IgnoreDuplicateNames)
	}
	if c.Severity(rules.RuleGodPackage) != rules.SeverityInfo || c.Severity(rules.RuleImportCycle) != rules.SeverityWarn ||
		c.Severity(rules.RuleLayerViolation) != rules.SeverityError || !c.Enabled(rules.RuleLayerViolation) {
		t.Errorf("settings = %+v", c.Settings)
	}
	if !reflect.DeepEqual(c.Ignore, []string{"**/testdata/**"}) {
		t.Errorf("ignore = %v", c.Ignore)
	}
}

func TestParseDisable(t *testing.T) {
	f, err := Parse([]byte("rules:\n  duplicate-symbol: {enabled: false}\n"))
	if err != nil || f.Rules.Enabled(rules.RuleDuplicateSymbol) || !f.Rules.Enabled(rules.RuleGodPackage) {
		t.Errorf("= %+v, %v", f.Rules.Settings, err)
	}
}

// Own layers replace the default ones and their forbidden pairs.
func TestParseLayersReplaceDefaults(t *testing.T) {
	f, err := Parse([]byte("layers:\n  core: [core/**]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Rules.Layers) != 1 || len(f.Rules.ForbiddenLayers) != 0 {
		t.Errorf("= %+v", f.Rules)
	}
	// forbidden alone refers to the default layers
	f, err = Parse([]byte("forbidden:\n  domain: [db]\n"))
	if err != nil || !reflect.DeepEqual(f.Rules.ForbiddenLayers, [][2]string{{"domain", "db"}}) || len(f.Rules.Layers) != len(rules.DefaultConfig().Layers) {
		t.Errorf("= %+v, %v", f.Rules, err)
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name, src, want string
	}{
		{"broken yaml", "project: [broken\n", "yaml"},
		{"unknown top-level key", "projct: x\n", `field projct not found`},
		{"unknown rule", "rules:\n  no-such-rule: {severity: warn}\n", `unknown rule "no-such-rule"`},
		{"unknown rule option", "rules:\n  import-cycle: {fanIn: 3}\n", `rules.import-cycle: unknown option "fanIn"`},
		{"typo option", "rules:\n  god-package: {fanin: 3}\n", `unknown option "fanin" (allowed: enabled, severity, fanIn)`},
		{"bad severity", "rules:\n  god-package: {severity: fatal}\n", `severity "fatal"`},
		{"rule not a mapping", "rules:\n  god-package: warn\n", `rules.god-package: want a mapping`},
		{"bad fanIn", "rules:\n  god-package: {fanIn: many}\n", `rules.god-package`},
		{"unknown forbidden from", "forbidden:\n  nope: [db]\n", `unknown layer "nope"`},
		{"unknown forbidden to", "layers:\n  a: [a/**]\nforbidden:\n  a: [b]\n", `unknown layer "b"`},
		{"layer not a list", "layers:\n  a: a/**\n", "yaml"},
		{"empty layer", "layers:\n  a: []\n", `layer "a"`},
		{"bad glob", "layers:\n  a: [a/b**]\n", `layer "a"`},
		{"bad ignore", "ignore: [\"\"]\n", "ignore"},
		{"duplicate key", "project: a\nproject: b\n", "already defined"},
		{"two documents", "project: a\n---\nproject: b\n", "one YAML document"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.src))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Parse = %v, want error containing %q", err, tt.want)
			}
		})
	}
}

func TestLoadErrorNamesFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("rules:\n  nope: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), FileName) {
		t.Errorf("Load = %v", err)
	}
}

// Every rule-specific option a rule declares is decoded (not silently dropped),
// and every rule accepts enabled / severity.
func TestRuleOptionsAreDecoded(t *testing.T) {
	tags := map[string]bool{}
	rt := reflect.TypeOf(ruleYAML{})
	for i := range rt.NumField() {
		tags[rt.Field(i).Tag.Get("yaml")] = true
	}
	for _, r := range rules.Builtin() {
		for _, o := range r.Options {
			if !tags[o] {
				t.Errorf("rule %s option %q is not a ruleYAML field", r.Name, o)
			}
		}
		if _, err := Parse([]byte("rules:\n  " + r.Name + ": {enabled: true, severity: info}\n")); err != nil {
			t.Errorf("%s: %v", r.Name, err)
		}
	}
}

// The repository's own .depgraph.yml must stay valid.
func TestRepositoryConfigIsValid(t *testing.T) {
	f, err := Load(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Rules.Layers) == 0 || len(f.Rules.ForbiddenLayers) == 0 {
		t.Errorf("repository config has no layers: %+v", f.Rules)
	}
}

func TestParseDuplicateCodeOptions(t *testing.T) {
	f, err := Parse([]byte("rules:\n  duplicate-code: {minTokens: 80, similarity: 0.9}\n"))
	if err != nil {
		t.Fatal(err)
	}
	c := f.Rules.Clones
	if c.MinTokens != 80 || c.MinSimilarity != 0.9 || c.MaxFingerprintFreq != rules.DefaultCloneOptions().MaxFingerprintFreq {
		t.Errorf("clones = %+v", c)
	}
	for _, bad := range []string{"{similarity: 1.5}", "{similarity: 0}", "{minTokens: 0}"} {
		if _, err := Parse([]byte("rules:\n  duplicate-code: " + bad + "\n")); err == nil {
			t.Errorf("%s: want an error", bad)
		}
	}
}
