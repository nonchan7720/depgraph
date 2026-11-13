package rules

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// Go functions and methods of one package, in different files, that call
// each other in a cycle are reported. Not: recursion inside one file, test
// files, a name that is no function of the package, TypeScript (its cycle
// across files is a file-cycle).
func TestEvaluateFunctionCycle(t *testing.T) {
	ctx, st := connect(t)
	key := fmt.Sprintf("zz_test_%d_funccycle", time.Now().UnixNano())
	fn := func(name string) graph.Symbol { return graph.Symbol{Name: name, Kind: "function"} }
	method := func(recv, name string) graph.Symbol { return graph.Symbol{Name: name, Kind: "method", Receiver: recv} }
	calls := func(caller string, callees ...string) graph.FuncCalls {
		fc := graph.FuncCalls{Name: caller}
		for _, c := range callees {
			fc.Calls = append(fc.Calls, graph.Call{Name: c})
		}
		return fc
	}
	file := func(path string, lang graph.Language, syms []graph.Symbol, cs ...graph.FuncCalls) *graph.FileGraph {
		return &graph.FileGraph{Path: path, Package: graph.PackageKey(path), Language: lang, Symbols: syms,
			Usage: &graph.Usage{Scope: ".", Calls: cs}}
	}
	upsertAll(ctx, t, st, key, []*graph.FileGraph{
		// Parse (parse.go) -> expr (expr.go) -> Parse
		file("p/parse.go", graph.Go, []graph.Symbol{fn("Parse"), fn("skip")}, calls("Parse", "expr", "skip", "len")),
		file("p/expr.go", graph.Go, []graph.Symbol{fn("expr"), fn("term")}, calls("expr", "term", "Parse"), calls("term", "expr")),
		file("p/parse_test.go", graph.Go, []graph.Symbol{fn("TestParse")}, calls("TestParse", "Parse")),
		// a method calls a function, in another file, that calls a method of the type back
		file("q/a.go", graph.Go, []graph.Symbol{method("T", "Run")}, calls("T.Run", "step")),
		file("q/b.go", graph.Go, []graph.Symbol{method("T", "Next"), fn("step")},
			calls("T.Next", "T.Run"), calls("step", "T.Next")),
		// across packages nothing calls back
		file("r/r.go", graph.Go, []graph.Symbol{fn("R")},
			graph.FuncCalls{Name: "R", Calls: []graph.Call{{Target: "p", Name: "Parse"}}}),
		// TypeScript: left to file-cycle
		file("src/a.ts", graph.TypeScript, []graph.Symbol{fn("f")},
			graph.FuncCalls{Name: "f", Calls: []graph.Call{{Target: "src/b.ts", Name: "g"}}}),
		file("src/b.ts", graph.TypeScript, []graph.Symbol{fn("g")},
			graph.FuncCalls{Name: "g", Calls: []graph.Call{{Target: "src/a.ts", Name: "f"}}}),
	})
	fs, err := New(st, key).Evaluate(ctx, rules.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range byRule(fs, rules.RuleFunctionCycle) {
		got = append(got, f.Message)
	}
	want := []string{
		"p/expr.go expr -> p/parse.go Parse -> p/expr.go expr",
		"q/a.go T.Run -> q/b.go step -> q/b.go T.Next -> q/a.go T.Run",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("function-cycle =\n %q, want\n %q", got, want)
	}
}

// A cycle through methods and a function is reported; one among the
// methods of a single type is not.
func TestEvaluateFunctionCycleThroughMethods(t *testing.T) {
	ctx, st := connect(t)
	key := fmt.Sprintf("zz_test_%d_funccycle_m", time.Now().UnixNano())
	file := func(path string, syms []graph.Symbol, cs ...graph.FuncCalls) *graph.FileGraph {
		return &graph.FileGraph{Path: path, Package: graph.PackageKey(path), Language: graph.Go, Symbols: syms,
			Usage: &graph.Usage{Scope: ".", Calls: cs}}
	}
	m := func(recv, name string) graph.Symbol { return graph.Symbol{Name: name, Kind: "method", Receiver: recv} }
	call := func(caller, callee string) graph.FuncCalls {
		return graph.FuncCalls{Name: caller, Calls: []graph.Call{{Name: callee}}}
	}
	upsertAll(ctx, t, st, key, []*graph.FileGraph{
		file("q/a.go", []graph.Symbol{m("T", "Run"), {Name: "step", Kind: "function"}}, call("T.Run", "step")),
		file("q/b.go", []graph.Symbol{m("T", "Next")}, call("T.Next", "T.Run")),
		file("q/c.go", nil, call("step", "T.Next")), // calls of a function declared in a.go, were it here
		file("p/a.go", []graph.Symbol{m("P", "stmt")}, call("P.stmt", "P.expr")),
		file("p/b.go", []graph.Symbol{m("P", "expr")}, call("P.expr", "P.stmt")),
	})
	fs, err := New(st, key).Evaluate(ctx, rules.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if got := byRule(fs, rules.RuleFunctionCycle); len(got) != 0 {
		t.Errorf("function-cycle = %+v, want none (step in q/c.go is not declared there; P is one type)", got)
	}
}
