package rules

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// A function calling into more packages than maxPackages is reported with
// the packages; its own package, calls that name no function and a function
// with a depgraph:ignore comment are not counted.
func TestEvaluateWideFunction(t *testing.T) {
	ctx, st := connect(t)
	key := fmt.Sprintf("zz_test_%d_wide", time.Now().UnixNano())
	fn := func(name string, line int) graph.Symbol {
		return graph.Symbol{Name: name, Kind: "function", Exported: true, Line: line}
	}
	lib := func(pkg string) *graph.FileGraph {
		return &graph.FileGraph{Path: pkg + "/x.go", Package: pkg, Language: graph.Go, Symbols: []graph.Symbol{fn("Do", 1)},
			Usage: &graph.Usage{Scope: "."}}
	}
	calls := func(name string, ignore []string, pkgs ...string) graph.FuncCalls {
		fc := graph.FuncCalls{Name: name, Line: map[string]int{"Run": 10}[name], Ignore: ignore, Calls: []graph.Call{{Name: "local"}, {Target: "p1", Name: "Missing"}}}
		for _, p := range pkgs {
			fc.Calls = append(fc.Calls, graph.Call{Target: p, Name: "Do"})
		}
		return fc
	}
	files := []*graph.FileGraph{lib("p1"), lib("p2"), lib("p3"), lib("p4"),
		{Path: "app/run.go", Package: "app", Language: graph.Go,
			Symbols: []graph.Symbol{fn("Run", 10), fn("Wire", 30), fn("Narrow", 50), fn("local", 70)},
			Usage: &graph.Usage{Scope: ".", Calls: []graph.FuncCalls{
				calls("Run", nil, "p1", "p2", "p3", "p4"),
				calls("Wire", []string{"wide-function"}, "p1", "p2", "p3", "p4"),
				calls("Narrow", nil, "p1", "p2", "p3"),
			}}},
		{Path: "app/run_test.go", Package: "app", Language: graph.Go, Symbols: []graph.Symbol{fn("TestRun", 1)},
			Usage: &graph.Usage{Scope: ".", Calls: []graph.FuncCalls{calls("TestRun", nil, "p1", "p2", "p3", "p4")}}},
	}
	// a function that builds what it needs from many packages wires them: a
	// constructor (Go NewX, a class in TypeScript / Python) is not counted
	ctor := func(pkg string) *graph.FileGraph {
		return &graph.FileGraph{Path: pkg + "/x.go", Package: pkg, Language: graph.Go,
			Symbols: []graph.Symbol{fn("New", 1), fn("NewClient", 3), fn("Do", 5)}, Usage: &graph.Usage{Scope: "."}}
	}
	news := graph.FuncCalls{Name: "NewApp", Line: 90}
	mixed := graph.FuncCalls{Name: "Mixed", Line: 95}
	for i, p := range []string{"c1", "c2", "c3", "c4"} {
		files = append(files, ctor(p))
		news.Calls = append(news.Calls, graph.Call{Target: p, Name: []string{"New", "NewClient"}[i%2]})
		mixed.Calls = append(mixed.Calls, graph.Call{Target: p, Name: "New"}, graph.Call{Target: p, Name: "Do"})
	}
	files = append(files, &graph.FileGraph{Path: "app/wire.go", Package: "app", Language: graph.Go,
		Symbols: []graph.Symbol{fn("NewApp", 90), fn("Mixed", 95)}, Usage: &graph.Usage{Scope: ".", Calls: []graph.FuncCalls{news, mixed}}})
	cls := func(name string) graph.Symbol { return graph.Symbol{Name: name, Kind: "class", Exported: true} }
	build := graph.FuncCalls{Name: "build", Line: 3}
	for _, d := range []string{"a", "b", "c", "d"} {
		files = append(files, &graph.FileGraph{Path: "src/" + d + "/svc.ts", Package: "src/" + d, Language: graph.TypeScript,
			Symbols: []graph.Symbol{cls("Svc")}, Usage: &graph.Usage{Scope: "."}})
		build.Calls = append(build.Calls, graph.Call{Target: "src/" + d + "/svc.ts", Name: "Svc"})
	}
	files = append(files, &graph.FileGraph{Path: "src/main.ts", Package: "src", Language: graph.TypeScript,
		Symbols: []graph.Symbol{fn("build", 3)}, Usage: &graph.Usage{Scope: ".", Calls: []graph.FuncCalls{build}}})
	upsertAll(ctx, t, st, key, files)
	cfg := rules.DefaultConfig()
	cfg.MaxCalledPackages = 3
	fs, err := New(st, key).Evaluate(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range byRule(fs, rules.RuleWideFunction) {
		got = append(got, f.Severity+" "+f.Message)
	}
	want := []string{
		"info app/run.go:10 Run calls functions of 4 packages (limit 3): p1, p2, p3, p4",
		"info app/wire.go:95 Mixed calls functions of 4 packages (limit 3): c1, c2, c3, c4",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("wide-function =\n %q, want\n %q", got, want)
	}
	if fs, err = New(st, key).Evaluate(ctx, rules.DefaultConfig()); err != nil || len(byRule(fs, rules.RuleWideFunction)) != 0 {
		t.Errorf("default limit: %v, %v", byRule(fs, rules.RuleWideFunction), err)
	}
}
