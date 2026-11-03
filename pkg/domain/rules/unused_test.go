package rules

import (
	"strings"
	"testing"
)

func TestUnusedExportFinding(t *testing.T) {
	f := UnusedExport("src/a.ts", 12, "function", "helper")
	if f.Rule != RuleUnusedExport || f.Severity != "warn" {
		t.Errorf("finding = %+v", f)
	}
	// the line is not part of the identity: editing above it is not a new finding
	if f.Key() != UnusedExport("src/a.ts", 40, "function", "helper").Key() {
		t.Error("key depends on the line")
	}
	if !strings.Contains(f.Message, "src/a.ts:12") || !strings.Contains(f.Message, "helper") || !strings.Contains(f.Message, "nothing") {
		t.Errorf("message = %q", f.Message)
	}
}

// The unused-code rules are registered (configurable in .depgraph.yml) and
// their path globs are validated.
func TestUnusedRulesRegistered(t *testing.T) {
	for _, name := range []string{RuleUnusedExport, RuleUnreachableFile, RuleUnreachablePackage} {
		r, ok := Lookup(name)
		if !ok || r.Severity != SeverityWarn || strings.Join(r.Options, ",") != "entries,ignore" {
			t.Errorf("%s: %+v %v", name, r, ok)
		}
	}
	c := DefaultConfig()
	c.UnusedIgnore = []string{"a/**b"}
	if c.Validate() == nil {
		t.Error("bad unused ignore glob accepted")
	}
	c = DefaultConfig()
	c.UnusedEntries = []string{""}
	if c.Validate() == nil {
		t.Error("empty entries glob accepted")
	}
}

func TestUnreachableFindings(t *testing.T) {
	f := UnreachableFile("src/old.ts", []string{"src/b.ts", "src/c.ts"}, false)
	if f.Rule != RuleUnreachableFile || f.Severity != "warn" || f.ID != "src/old.ts" {
		t.Errorf("finding = %+v", f)
	}
	if !strings.Contains(f.Message, "src/old.ts") || !strings.Contains(f.Message, "2 more files") || !strings.Contains(f.Message, "src/b.ts") {
		t.Errorf("message = %q", f.Message)
	}
	if f.Key() != UnreachableFile("src/old.ts", nil, false).Key() {
		t.Error("key depends on the files it takes along")
	}
	many := make([]string, 12)
	for i := range many {
		many[i] = "f" + string(rune('a'+i))
	}
	if m := UnreachableFile("x", many, true).Message; !strings.Contains(m, "+7") || !strings.Contains(m, "only by files") {
		t.Errorf("message = %q", m)
	}
	p := UnreachablePackage("pkg/old", nil, false)
	if p.Rule != RuleUnreachablePackage || p.ID != "pkg/old" || !strings.Contains(p.Message, "pkg/old") {
		t.Errorf("finding = %+v", p)
	}
}
