package rules

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// The cases the Go implementations of these rules were tested with, before
// the rules became Cypher over the linked graph: the same graphs, the same
// findings.

// evaluateGraph stores files as project name and returns the messages of rule.
func evaluateGraph(t *testing.T, name, rule string, cfg rules.Config, files []*graph.FileGraph) []string {
	t.Helper()
	ctx, st := connect(t)
	key := fmt.Sprintf("zz_test_%d_%s", time.Now().UnixNano(), name)
	upsertAll(ctx, t, st, key, files)
	fs, err := New(st, key).Evaluate(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range byRule(fs, rule) {
		out = append(out, f.Message)
	}
	return out
}

// goCalls are Go files declaring the functions of the calls ("file name")
// and calling as given; forwards marks every caller as a pure forwarder.
func goCalls(calls [][2]string, forwards bool) []*graph.FileGraph {
	type fn struct{ file, name string }
	split := func(s string) fn { f, n, _ := strings.Cut(s, " "); return fn{f, n} }
	declared := map[fn]bool{}
	callsOf := map[fn][]graph.Call{}
	for _, c := range calls {
		from, to := split(c[0]), split(c[1])
		declared[from], declared[to] = true, true
		target := ""
		if dir := graph.PackageKey(to.file); dir != graph.PackageKey(from.file) {
			target = dir
		}
		callsOf[from] = append(callsOf[from], graph.Call{Target: target, Name: to.name})
	}
	files := map[string]*graph.FileGraph{}
	for _, f := range slices.SortedFunc(maps.Keys(declared), func(a, b fn) int { return strings.Compare(a.file+a.name, b.file+b.name) }) {
		fg := files[f.file]
		if fg == nil {
			fg = &graph.FileGraph{Path: f.file, Package: graph.PackageKey(f.file), Language: graph.Go, Usage: &graph.Usage{Scope: "."}}
			files[f.file] = fg
		}
		sym := graph.Symbol{Name: f.name, Kind: "function"}
		if recv, name, ok := strings.Cut(f.name, "."); ok {
			sym = graph.Symbol{Name: name, Kind: "method", Receiver: recv}
		}
		fg.Symbols = append(fg.Symbols, sym)
		if cs := callsOf[f]; len(cs) > 0 {
			fg.Usage.Calls = append(fg.Usage.Calls, graph.FuncCalls{Name: f.name, Calls: cs, Forwards: forwards})
		}
	}
	return slices.Collect(maps.Values(files))
}

func TestFunctionCyclesCases(t *testing.T) {
	got := evaluateGraph(t, "fc_cases", rules.RuleFunctionCycle, rules.DefaultConfig(), goCalls([][2]string{
		// across two files
		{"p/a.go Parse", "p/b.go expr"},
		{"p/b.go expr", "p/a.go Parse"},
		// mutual recursion in one file
		{"p/c.go walk", "p/c.go visit"},
		{"p/c.go visit", "p/c.go walk"},
		{"p/c.go visit", "p/a.go Parse"}, // leaves, never comes back
		// three functions, two files
		{"q/x.go A", "q/x.go B"},
		{"q/x.go B", "q/y.go T.C"},
		{"q/y.go T.C", "q/x.go A"},
		{"p/a.go Parse", "p/a.go Parse"}, // direct recursion
		// the methods of one type, in two files: the type is the unit
		{"r/a.go parser.stmt", "r/b.go parser.expr"},
		{"r/b.go parser.expr", "r/a.go parser.stmt"},
	}, false))
	want := []string{
		"p/a.go Parse -> p/b.go expr -> p/a.go Parse",
		"q/x.go A -> q/x.go B -> q/y.go T.C -> q/x.go A",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("function-cycle =\n %q, want\n %q", got, want)
	}
}

// A chain of at least two functions that only pass their parameters on is
// reported once, from the function nothing forwards to; a single wrapper is
// not, and a ring of forwarders is not followed for ever.
func TestPassThroughChainsCases(t *testing.T) {
	files := goCalls([][2]string{
		{"api/a.go Get", "svc/s.go Get"},
		{"svc/s.go Get", "svc/s.go get"},
		{"svc/s.go get", "db/d.go Find"}, // db.Find does the work
		{"api/a.go One", "db/d.go Find"}, // a single wrapper
		{"x/a.go A", "x/b.go B"},         // a ring
		{"x/b.go B", "x/a.go A"},
		{"api/a.go Alias", "svc/s.go get"}, // joins the first chain
	}, true)
	got := evaluateGraph(t, "pt_cases", rules.RulePassThrough, rules.DefaultConfig(), files)
	want := []string{
		"api/a.go Alias -> svc/s.go get -> db/d.go Find: Alias and get only pass their arguments on",
		"api/a.go Get -> svc/s.go Get -> svc/s.go get -> db/d.go Find: Get, Get and get only pass their arguments on",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("pass-through =\n %q, want\n %q", got, want)
	}
}

// tsLoads are TypeScript files loading each other (from, to; deferred
// marks an import inside a function).
func tsLoads(loads [][2]string, deferred map[[2]string]bool) []*graph.FileGraph {
	files := map[string]*graph.FileGraph{}
	file := func(p string) *graph.FileGraph {
		if files[p] == nil {
			files[p] = &graph.FileGraph{Path: p, Package: graph.PackageKey(p), Language: graph.TypeScript, Usage: &graph.Usage{Scope: "."}}
		}
		return files[p]
	}
	for _, l := range loads {
		u := graph.Use{Target: l[1]}
		if deferred[l] {
			u.As = graph.LoadDeferred
		}
		file(l[0]).Usage.Loads = append(file(l[0]).Usage.Loads, u)
		file(l[1])
	}
	return slices.Collect(maps.Values(files))
}

// For every import on a cycle, the shortest cycle through it is reported
// once, starting at its smallest file; imports on no cycle are not.
func TestFileCyclesCases(t *testing.T) {
	got := evaluateGraph(t, "filec_cases", rules.RuleFileCycle, rules.DefaultConfig(), tsLoads([][2]string{
		{"src/b.ts", "src/a.ts"}, {"src/a.ts", "src/b.ts"}, // a pair
		{"src/a.ts", "src/c.ts"},                                                     // a leaf: no cycle
		{"app/x.ts", "app/y.ts"}, {"app/y.ts", "app/z.ts"}, {"app/z.ts", "app/x.ts"}, // a ring of three
		{"app/y.ts", "app/x.ts"}, // and a pair inside it
		{"src/a.ts", "src/a.ts"}, // a file importing itself is not a cycle
	}, nil))
	want := []string{
		"app/x.ts -> app/y.ts -> app/x.ts",
		"app/x.ts -> app/y.ts -> app/z.ts -> app/x.ts",
		"src/a.ts -> src/b.ts -> src/a.ts",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("file-cycle =\n %q, want\n %q", got, want)
	}
}

// A cycle that only closes through a deferred import is told apart from one
// that exists when the files are loaded, and can be left out.
func TestFileCyclesDeferredCases(t *testing.T) {
	loads := [][2]string{
		{"app/a.ts", "app/b.ts"}, {"app/b.ts", "app/a.ts"}, // at load time
		{"app/x.ts", "app/y.ts"}, {"app/y.ts", "app/x.ts"},
		{"app/y.ts", "app/z.ts"}, // on no cycle
	}
	deferred := map[[2]string]bool{{"app/y.ts", "app/x.ts"}: true, {"app/y.ts", "app/z.ts"}: true}
	got := evaluateGraph(t, "filec_def", rules.RuleFileCycle, rules.DefaultConfig(), tsLoads(loads, deferred))
	want := []string{
		"app/a.ts -> app/b.ts -> app/a.ts",
		"app/x.ts -> app/y.ts -> app/x.ts (hidden by an import inside a function of app/y.ts)",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("findings =\n %q, want\n %q", got, want)
	}
	cfg := rules.DefaultConfig()
	cfg.FileCycleDeferred = false
	if got := evaluateGraph(t, "filec_nodef", rules.RuleFileCycle, cfg, tsLoads(loads, deferred)); !reflect.DeepEqual(got, want[:1]) {
		t.Errorf("without deferred imports = %q", got)
	}
}

// A cycle longer than 8 files is not searched for.
func TestFileCyclesIsBoundedCases(t *testing.T) {
	var edges [][2]string
	names := []string{"d/a.ts", "d/b.ts", "d/c.ts", "d/d.ts", "d/e.ts", "d/f.ts", "d/g.ts", "d/h.ts", "d/i.ts"}
	for i, n := range names {
		edges = append(edges, [2]string{n, names[(i+1)%len(names)]})
	}
	cfg := rules.DefaultConfig()
	if got := evaluateGraph(t, "filec_9", rules.RuleFileCycle, cfg, tsLoads(edges, nil)); len(got) != 0 {
		t.Errorf("a ring of 9 files: %v", got)
	}
	if got := evaluateGraph(t, "filec_open", rules.RuleFileCycle, cfg, tsLoads(edges[:8:8], nil)); len(got) != 0 {
		t.Errorf("an open chain: %v", got)
	}
	ring8 := append(edges[:7:7], [2]string{"d/h.ts", "d/a.ts"})
	if got := evaluateGraph(t, "filec_8", rules.RuleFileCycle, cfg, tsLoads(ring8, nil)); len(got) != 1 || strings.Count(got[0], " -> ") != 8 {
		t.Errorf("a ring of 8 files: %v", got)
	}
}
