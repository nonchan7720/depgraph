package rules

import "testing"

func TestMisplaced(t *testing.T) {
	use := func(pkg string, names ...string) map[string][]string { return map[string][]string{pkg: names} }
	cases := []struct {
		name  string
		f     FuncUse
		where string
	}{
		{"uses three names of one other package and nothing of its own",
			FuncUse{Other: use("pkg/billing", "Invoice", "Total", "Tax")}, "pkg/billing"},
		{"uses its own package too", FuncUse{Own: 1, Other: use("pkg/billing", "Invoice", "Total", "Tax")}, ""},
		{"two names are not enough", FuncUse{Other: use("pkg/billing", "Invoice", "Total")}, ""},
		{"two other packages: it coordinates them",
			FuncUse{Other: map[string][]string{"pkg/billing": {"Invoice", "Total", "Tax"}, "pkg/user": {"User"}}}, ""},
		{"a method belongs to its type", FuncUse{Method: true, Other: use("pkg/billing", "Invoice", "Total", "Tax")}, ""},
		{"nothing of the repository", FuncUse{}, ""},
	}
	for _, c := range cases {
		if got := Misplaced(c.f, 3); got != c.where {
			t.Errorf("%s: Misplaced = %q, want %q", c.name, got, c.where)
		}
	}
	f := MisplacedFunction("pkg/report/pdf.go", 20, "invoiceTotal", "pkg/billing", []string{"Invoice", "Tax", "Total"})
	want := "pkg/report/pdf.go:20 invoiceTotal uses nothing of pkg/report and 3 names of pkg/billing (Invoice, Tax, Total): it belongs there"
	if f.Rule != RuleMisplacedFunction || f.Severity != "info" || f.Message != want || f.ID != "pkg/report/pdf.go#invoiceTotal" {
		t.Errorf("finding = %+v", f)
	}
	if r, ok := Lookup(RuleMisplacedFunction); !ok || len(r.Options) != 1 || r.Options[0] != "minNames" {
		t.Errorf("rule = %+v", r)
	}
}
