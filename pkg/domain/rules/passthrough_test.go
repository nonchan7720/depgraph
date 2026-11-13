package rules

import "testing"

func TestPassThroughFinding(t *testing.T) {
	f := PassThrough([]FuncNode{{"api/a.go", "Get"}, {"svc/s.go", "Get"}, {"db/d.go", "Find"}})
	want := "api/a.go Get -> svc/s.go Get -> db/d.go Find: Get and Get only pass their arguments on"
	if f.Rule != RulePassThrough || f.Severity != "warn" || f.Message != want || f.ID != "api/a.go#Get" {
		t.Errorf("finding = %+v", f)
	}
	if r, ok := Lookup(RulePassThrough); !ok || r.Severity != SeverityWarn {
		t.Errorf("rule = %+v", r)
	}
}
