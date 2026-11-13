package rules

import "testing"

func TestWideFunctionFinding(t *testing.T) {
	f := WideFunction("cmd/app/main.go", 12, "run", []string{"pkg/a", "pkg/b", "pkg/c"}, 2)
	want := "cmd/app/main.go:12 run calls functions of 3 packages (limit 2): pkg/a, pkg/b, pkg/c"
	if f.Rule != RuleWideFunction || f.Message != want || f.ID != "cmd/app/main.go#run" {
		t.Errorf("finding = %+v", f)
	}
	many := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l"}
	if got := WideFunction("x.go", 1, "f", many, 8).Message; got != "x.go:1 f calls functions of 12 packages (limit 8): a, b, c, d, e, f, g, h, i, j, and 2 more" {
		t.Errorf("message = %q", got)
	}
	r, ok := Lookup(RuleWideFunction)
	if !ok || len(r.Options) != 1 || r.Options[0] != "maxPackages" {
		t.Errorf("rule = %+v", r)
	}
	if DefaultConfig().MaxCalledPackages != DefaultMaxCalledPackages {
		t.Errorf("default = %d", DefaultConfig().MaxCalledPackages)
	}
	if !IgnoresRule([]string{"pass-through", "wide-function"}, RuleWideFunction) || IgnoresRule(nil, RuleWideFunction) {
		t.Error("IgnoresRule")
	}
}
