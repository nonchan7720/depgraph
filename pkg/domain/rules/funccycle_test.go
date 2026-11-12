package rules

import (
	"reflect"
	"testing"
)

// Functions of different files that call each other in a cycle are
// reported; recursion inside one file is not, and neither is a cycle that
// only leaves the file through a call that is not on it.
func TestFunctionCycles(t *testing.T) {
	n := func(file, name string) FuncNode { return FuncNode{File: file, Name: name} }
	got := FunctionCycles([][2]FuncNode{
		// across two files
		{n("p/a.go", "Parse"), n("p/b.go", "expr")},
		{n("p/b.go", "expr"), n("p/a.go", "Parse")},
		// mutual recursion in one file
		{n("p/c.go", "walk"), n("p/c.go", "visit")},
		{n("p/c.go", "visit"), n("p/c.go", "walk")},
		{n("p/c.go", "visit"), n("p/a.go", "Parse")}, // leaves, never comes back
		// three functions, two files
		{n("q/x.go", "A"), n("q/x.go", "B")},
		{n("q/x.go", "B"), n("q/y.go", "T.C")},
		{n("q/y.go", "T.C"), n("q/x.go", "A")},
		{n("p/a.go", "Parse"), n("p/a.go", "Parse")}, // direct recursion
		// the methods of one type, in two files: the type is the unit
		{n("r/a.go", "parser.stmt"), n("r/b.go", "parser.expr")},
		{n("r/b.go", "parser.expr"), n("r/a.go", "parser.stmt")},
	})
	want := [][]FuncNode{
		{n("p/a.go", "Parse"), n("p/b.go", "expr"), n("p/a.go", "Parse")},
		{n("q/x.go", "A"), n("q/x.go", "B"), n("q/y.go", "T.C"), n("q/x.go", "A")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FunctionCycles =\n %v, want\n %v", got, want)
	}
}

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
