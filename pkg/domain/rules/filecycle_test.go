package rules

import "testing"

func TestFileCycleFinding(t *testing.T) {
	f := FileCycle([]string{"src/a.ts", "src/b.ts", "src/a.ts"})
	if f.Rule != RuleFileCycle || f.Severity != "warn" || f.Message != "src/a.ts -> src/b.ts -> src/a.ts" {
		t.Errorf("finding = %+v", f)
	}
	if r, ok := Lookup(RuleFileCycle); !ok || r.Severity != SeverityWarn {
		t.Errorf("rule not registered: %+v", r)
	}
}
