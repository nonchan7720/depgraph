package rules

import (
	"reflect"
	"testing"
)

// A chain of at least two functions that only pass their parameters on is
// reported once, from the function nothing forwards to; a single wrapper is
// not, and a ring of forwarders is not followed for ever.
func TestPassThroughChains(t *testing.T) {
	n := func(file, name string) FuncNode { return FuncNode{File: file, Name: name} }
	got := PassThroughChains([][2]FuncNode{
		{n("api/a.go", "Get"), n("svc/s.go", "Get")},
		{n("svc/s.go", "Get"), n("svc/s.go", "get")},
		{n("svc/s.go", "get"), n("db/d.go", "Find")}, // db.Find does the work
		{n("api/a.go", "One"), n("db/d.go", "Find")}, // a single wrapper
		{n("x/a.go", "A"), n("x/b.go", "B")},         // a ring
		{n("x/b.go", "B"), n("x/a.go", "A")},
		{n("api/a.go", "Alias"), n("svc/s.go", "get")}, // joins the first chain
	})
	want := [][]FuncNode{
		{n("api/a.go", "Alias"), n("svc/s.go", "get"), n("db/d.go", "Find")},
		{n("api/a.go", "Get"), n("svc/s.go", "Get"), n("svc/s.go", "get"), n("db/d.go", "Find")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("PassThroughChains =\n %v, want\n %v", got, want)
	}
}

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
