package rules

import "testing"

func TestFunctionCycleFinding(t *testing.T) {
	f := FunctionCycle([]FuncNode{{"p/a.go", "Parse"}, {"p/b.go", "expr"}, {"p/a.go", "Parse"}})
	want := "p/a.go Parse -> p/b.go expr -> p/a.go Parse"
	if f.Rule != RuleFunctionCycle || f.Severity != "info" || f.Message != want {
		t.Errorf("finding = %+v", f)
	}
	if r, ok := Lookup(RuleFunctionCycle); !ok || r.Severity != SeverityInfo {
		t.Errorf("rule = %+v", r)
	}
}
