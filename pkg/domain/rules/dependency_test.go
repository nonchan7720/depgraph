package rules

import (
	"reflect"
	"regexp"
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

// An ignore entry is a name pattern, alone or after the language or the
// manifest (a path glob) it applies to.
func TestDependencyIgnores(t *testing.T) {
	cfg := DefaultConfig()
	cfg.IgnoreDependencies = []string{"tslib", "@types/*", "ruby:pg", "python:*", "javascript:left-pad", "go:github.com/acme/*",
		"web/package.json:lodash", "services/**/requirements.txt:*", "Gemfile:tzinfo-*"}
	got, err := cfg.DependencyIgnores()
	if err != nil {
		t.Fatal(err)
	}
	want := []DependencyIgnore{
		{Name: `^tslib$`},
		{Name: `^@types/.*$`},
		{Ecosystems: []string{"gem"}, Name: `^pg$`},
		{Ecosystems: []string{"pypi"}, Name: `^.*$`},
		{Ecosystems: []string{"npm"}, Name: `^left-pad$`},
		{Ecosystems: []string{"go"}, Name: `^github\.com/acme/.*$`},
		{Manifest: got[6].Manifest, Name: `^lodash$`},
		{Manifest: got[7].Manifest, Name: `^.*$`},
		{Manifest: got[8].Manifest, Name: `^tzinfo-.*$`},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ignores =\n %+v, want\n %+v", got, want)
	}
	for i, c := range []struct {
		manifest string
		match    bool
	}{{"web/package.json", true}, {"package.json", false}, {"services/a/b/requirements.txt", true}, {"Gemfile", true}} {
		rx := got[[]int{6, 6, 7, 8}[i]].Manifest
		if ok, _ := regexp.MatchString(rx, c.manifest); ok != c.match {
			t.Errorf("%q against %q = %v, want %v", c.manifest, rx, ok, c.match)
		}
	}
	for _, bad := range []string{" ", "ruby:", ":pg", "web/package.json:"} {
		cfg.IgnoreDependencies = []string{bad}
		if err := cfg.Validate(); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
	if r, ok := Lookup(RuleUnusedDependency); !ok || r.Severity != SeverityError || !reflect.DeepEqual(r.Options, []string{"ignore"}) {
		t.Errorf("registry = %+v, %v", r, ok)
	}
}
