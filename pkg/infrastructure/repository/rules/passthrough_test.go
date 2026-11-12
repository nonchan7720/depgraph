package rules

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// Chains of forwarding functions are found across packages and files, in
// Go, TypeScript (through a default export) and Python (through a package).
func TestEvaluatePassThrough(t *testing.T) {
	ctx, st := connect(t)
	key := fmt.Sprintf("zz_test_%d_passthrough", time.Now().UnixNano())
	fn := func(name string) graph.Symbol { return graph.Symbol{Name: name, Kind: "function", Exported: true} }
	fwd := func(caller, target, callee string) graph.FuncCalls {
		return graph.FuncCalls{Name: caller, Forwards: true, Calls: []graph.Call{{Target: target, Name: callee}}}
	}
	file := func(path string, lang graph.Language, syms []graph.Symbol, u graph.Usage) *graph.FileGraph {
		u.Scope = "."
		return &graph.FileGraph{Path: path, Package: graph.PackageKey(path), Language: lang, Symbols: syms, Usage: &u}
	}
	upsertAll(ctx, t, st, key, []*graph.FileGraph{
		// Go: api.Get -> svc.Get -> svc.load (works)
		file("api/a.go", graph.Go, []graph.Symbol{fn("Get"), fn("One")},
			graph.Usage{Calls: []graph.FuncCalls{fwd("Get", "svc", "Get"), fwd("One", "svc", "load")}}),
		file("svc/s.go", graph.Go, []graph.Symbol{fn("Get"), fn("load")},
			graph.Usage{Calls: []graph.FuncCalls{fwd("Get", "", "load"), {Name: "load", Calls: []graph.Call{{Target: "db", Name: "Find"}}}}}),
		// a test's helpers are not reported
		file("svc/s_test.go", graph.Go, []graph.Symbol{fn("helper")},
			graph.Usage{Calls: []graph.FuncCalls{fwd("helper", "api", "Get")}}),
		// TypeScript: a -> default export of b -> c
		file("src/a.ts", graph.TypeScript, []graph.Symbol{fn("a")},
			graph.Usage{Calls: []graph.FuncCalls{fwd("a", "src/b.ts", "default")}}),
		file("src/b.ts", graph.TypeScript, []graph.Symbol{fn("b")},
			graph.Usage{Calls: []graph.FuncCalls{fwd("b", "src/c.ts", "c")}, Exports: []graph.ExportName{{Name: "default", Local: "b"}}}),
		file("src/c.ts", graph.TypeScript, []graph.Symbol{fn("c")}, graph.Usage{}),
		// Python: views.show -> services.show (a module of the package) -> repo.load
		file("app/views.py", graph.Python, []graph.Symbol{fn("show")},
			graph.Usage{Calls: []graph.FuncCalls{fwd("show", "app", "services.show")}}),
		file("app/services.py", graph.Python, []graph.Symbol{fn("show")},
			graph.Usage{Calls: []graph.FuncCalls{fwd("show", "app/repo.py", "load")}}),
		file("app/repo.py", graph.Python, []graph.Symbol{fn("load")}, graph.Usage{}),
		// forwards to something that is no function of the project: no chain
		file("src/d.ts", graph.TypeScript, []graph.Symbol{fn("d")},
			graph.Usage{Calls: []graph.FuncCalls{fwd("d", "src/c.ts", "missing")}}),
	})
	fs, err := New(st, key).Evaluate(ctx, rules.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range byRule(fs, rules.RulePassThrough) {
		got = append(got, f.Message)
	}
	want := []string{
		"api/a.go Get -> svc/s.go Get -> svc/s.go load: Get and Get only pass their arguments on",
		"app/views.py show -> app/services.py show -> app/repo.py load: show and show only pass their arguments on",
		"src/a.ts a -> src/b.ts b -> src/c.ts c: a and b only pass their arguments on",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("pass-through =\n %q, want\n %q", got, want)
	}
}
