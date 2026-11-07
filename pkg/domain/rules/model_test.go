package rules

import (
	"slices"
	"strings"
	"testing"
)

func TestFindingKey(t *testing.T) {
	if k := (Finding{Rule: "r", Message: "m"}).Key(); k != "r|m" {
		t.Errorf("key without ID = %q", k)
	}
	if k := (Finding{Rule: "r", Message: "m", ID: "x"}).Key(); k != "r|x" {
		t.Errorf("key with ID = %q", k)
	}
}

func TestDiff(t *testing.T) {
	a := Finding{Rule: "r", Message: "a"}
	b := Finding{Rule: "r", Message: "b"}
	c := Finding{Rule: "r", Message: "c"}
	added, removed := Diff([]Finding{a, b}, []Finding{b, c})
	if len(added) != 1 || added[0].Key() != c.Key() {
		t.Errorf("added = %+v", added)
	}
	if len(removed) != 1 || removed[0].Key() != a.Key() {
		t.Errorf("removed = %+v", removed)
	}
}

func TestDefaultConfig(t *testing.T) {
	c := DefaultConfig()
	if c.GodPackageFanIn <= 0 || len(c.ForbiddenLayers) == 0 || len(c.Layers) == 0 {
		t.Errorf("DefaultConfig = %+v", c)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("DefaultConfig is invalid: %v", err)
	}
	// names a language requires in every package are not duplicated logic
	for _, n := range []string{"main", "init", "TestMain"} {
		if !slices.Contains(c.IgnoreDuplicateNames, n) {
			t.Errorf("%s is not ignored by duplicate-symbol", n)
		}
	}
	for _, r := range Builtin() {
		if !c.Enabled(r.Name) || c.Severity(r.Name) != r.Severity {
			t.Errorf("%s: enabled=%v severity=%s", r.Name, c.Enabled(r.Name), c.Severity(r.Name))
		}
	}
}

// The default layers are whole path segments, not substrings of the key.
func TestDefaultLayersMatchWholeSegments(t *testing.T) {
	c := DefaultConfig()
	tests := []struct {
		key  string
		want []string
	}{
		{"pkg/feedback", nil},
		{"pkg/eventhandler", nil},
		{"pkg/infrared", nil},
		{"src/dbutil", nil},
		{"pkg/domainmodel", nil},
		{"internal/handler", []string{"handler"}},
		{"internal/handlers/user", []string{"handler"}},
		{"db", []string{"db"}},
		{"pkg/infrastructure/repository/graph", []string{"infra", "repository"}},
		{"app/infra", []string{"infra"}},
		{"pkg/domain/rules", []string{"domain"}},
		{"app/usecases", []string{"usecase"}},
		{"api/controllers", []string{"controller"}},
	}
	for _, tt := range tests {
		got, err := c.LayersOf(tt.key)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(got, ",") != strings.Join(tt.want, ",") {
			t.Errorf("LayersOf(%q) = %v, want %v", tt.key, got, tt.want)
		}
	}
}

func TestConfigOverrides(t *testing.T) {
	c := DefaultConfig()
	c.Settings = map[string]RuleSetting{
		RuleGodPackage:      {Disabled: true},
		RuleDuplicateSymbol: {Severity: SeverityError},
	}
	if c.Enabled(RuleGodPackage) || !c.Enabled(RuleImportCycle) {
		t.Error("god-package must be disabled, import-cycle enabled")
	}
	if c.Severity(RuleDuplicateSymbol) != SeverityError || c.Severity(RuleLayerViolation) != SeverityError || c.Severity(RuleGodPackage) != SeverityWarn {
		t.Error("severity override not applied")
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Config)
		want string
	}{
		{"unknown rule", func(c *Config) { c.Settings = map[string]RuleSetting{"no-such-rule": {}} }, `unknown rule "no-such-rule"`},
		{"bad severity", func(c *Config) { c.Settings = map[string]RuleSetting{RuleImportCycle: {Severity: "fatal"}} }, `severity "fatal"`},
		{"unknown from layer", func(c *Config) { c.ForbiddenLayers = [][2]string{{"nope", "db"}} }, `unknown layer "nope"`},
		{"unknown to layer", func(c *Config) { c.ForbiddenLayers = [][2]string{{"db", "nope"}} }, `unknown layer "nope"`},
		{"self", func(c *Config) { c.ForbiddenLayers = [][2]string{{"db", "db"}} }, `cannot name itself`},
		{"layer without packages", func(c *Config) { c.Layers = append(c.Layers, Layer{Name: "x"}) }, `layer "x"`},
		{"duplicate layer", func(c *Config) { c.Layers = append(c.Layers, Layer{Name: "db", Packages: []string{"x"}}) }, `layer "db" defined twice`},
		{"bad layer glob", func(c *Config) { c.Layers = append(c.Layers, Layer{Name: "x", Packages: []string{"a**"}}) }, `layer "x"`},
		{"bad ignore glob", func(c *Config) { c.Ignore = []string{""} }, `ignore`},
		{"fan-in", func(c *Config) { c.GodPackageFanIn = 0 }, `fanIn`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := DefaultConfig()
			tt.edit(&c)
			err := c.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Validate() = %v, want error containing %q", err, tt.want)
			}
		})
	}
}

func TestIgnored(t *testing.T) {
	c := DefaultConfig()
	c.Ignore = []string{"**/testdata/**"}
	if ok, err := c.Ignored("pkg/x/testdata/corpus"); err != nil || !ok {
		t.Errorf("testdata must be ignored: %v %v", ok, err)
	}
	if ok, _ := c.Ignored("pkg/x"); ok {
		t.Error("pkg/x must not be ignored")
	}
}
