package rules

import (
	"reflect"
	"testing"
)

func TestUnusedDependencyFinding(t *testing.T) {
	certain := UnusedDependency(DefaultConfig(), "web/package.json", 12, "left-pad", "")
	if certain.Severity != "error" || certain.ID != "web/package.json#left-pad" {
		t.Errorf("certain = %+v", certain)
	}
	if want := "web/package.json:12 left-pad is declared but nothing imports it or names it"; certain.Message != want {
		t.Errorf("message = %q, want %q", certain.Message, want)
	}
	doubt := UnusedDependency(DefaultConfig(), "package.json", 0, "eslint-plugin-x", "eslint may load it by its name")
	if doubt.Severity != "warn" {
		t.Errorf("doubtful severity = %q, want warn", doubt.Severity)
	}
	if want := "package.json eslint-plugin-x is declared but nothing imports it or names it (not certain: eslint may load it by its name)"; doubt.Message != want {
		t.Errorf("message = %q, want %q", doubt.Message, want)
	}
	if !reflect.DeepEqual(doubt.Data, map[string]any{"manifest": "package.json", "line": 0, "name": "eslint-plugin-x", "doubt": "eslint may load it by its name"}) {
		t.Errorf("data = %v", doubt.Data)
	}
}

// The configured severity is the one of the certain findings; a doubtful
// one is a level below it, and never above warn.
func TestUnusedDependencySeverity(t *testing.T) {
	for _, c := range []struct{ set, certain, doubt string }{
		{"", "error", "warn"},
		{"error", "error", "warn"},
		{"warn", "warn", "info"},
		{"info", "info", "info"},
	} {
		cfg := DefaultConfig()
		if c.set != "" {
			cfg.Settings = map[string]RuleSetting{RuleUnusedDependency: {Severity: Severity(c.set)}}
		}
		if got := UnusedDependency(cfg, "go.mod", 1, "m", "").Severity; got != c.certain {
			t.Errorf("severity %q: certain = %q, want %q", c.set, got, c.certain)
		}
		if got := UnusedDependency(cfg, "go.mod", 1, "m", "why").Severity; got != c.doubt {
			t.Errorf("severity %q: doubtful = %q, want %q", c.set, got, c.doubt)
		}
	}
}

func TestDependencyIgnoreRegexes(t *testing.T) {
	cfg := DefaultConfig()
	cfg.IgnoreDependencies = []string{"tslib", "@types/*"}
	got, err := cfg.DependencyIgnoreRegexes()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{`^tslib$`, `^@types/.*$`}; !reflect.DeepEqual(got, want) {
		t.Errorf("regexes = %q, want %q", got, want)
	}
	cfg.IgnoreDependencies = []string{" "}
	if err := cfg.Validate(); err == nil {
		t.Error("an empty pattern must be refused")
	}
	if r, ok := Lookup(RuleUnusedDependency); !ok || r.Severity != SeverityError || !reflect.DeepEqual(r.Options, []string{"ignore"}) {
		t.Errorf("registry = %+v, %v", r, ok)
	}
}
