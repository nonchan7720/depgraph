package rules

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// A function that only uses another package is reported, in Go (names the
// packages declare) and TypeScript (files of another directory); one that
// uses its own package, a method, main and an exempted function are not.
func TestEvaluateMisplacedFunction(t *testing.T) {
	ctx, st := connect(t)
	key := fmt.Sprintf("zz_test_%d_misplaced", time.Now().UnixNano())
	gofile := func(path string, decls []string, cs ...graph.FuncCalls) *graph.FileGraph {
		return &graph.FileGraph{Path: path, Package: graph.PackageKey(path), Language: graph.Go,
			Usage: &graph.Usage{Scope: ".", Decls: decls, Calls: cs}}
	}
	billing := func(names ...string) []graph.Call {
		var out []graph.Call
		for _, n := range names {
			out = append(out, graph.Call{Target: "pkg/billing", Name: n})
		}
		return out
	}
	upsertAll(ctx, t, st, key, []*graph.FileGraph{
		gofile("pkg/billing/b.go", []string{"Invoice", "Total", "Tax", "Round"}),
		gofile("pkg/report/pdf.go", []string{"Render", "invoiceTotal", "page", "Doc", "main"},
			graph.FuncCalls{Name: "invoiceTotal", Line: 20, Calls: billing("Total", "Tax"),
				Refs: append(billing("Invoice", "NotDeclared"), graph.Call{Name: "x"})},
			graph.FuncCalls{Name: "Render", Line: 40, Calls: billing("Total", "Tax", "Round"), Refs: []graph.Call{{Name: "page"}}},
			graph.FuncCalls{Name: "Doc.Sum", Line: 60, Calls: billing("Total", "Tax", "Round")},
			graph.FuncCalls{Name: "main", Line: 80, Calls: billing("Total", "Tax", "Round")},
			graph.FuncCalls{Name: "kept", Line: 90, Ignore: []string{"misplaced-function"}, Calls: billing("Total", "Tax", "Round")},
			graph.FuncCalls{Name: "few", Line: 95, Calls: billing("Total", "Tax")},
		),
		{Path: "src/cart/total.ts", Package: "src/cart", Language: graph.TypeScript, Usage: &graph.Usage{Scope: ".", Calls: []graph.FuncCalls{
			{Name: "userLabel", Line: 5, Calls: []graph.Call{{Target: "src/user/name.ts", Name: "full"}},
				Refs: []graph.Call{{Target: "src/user/name.ts", Name: "User"}, {Target: "src/user/role.ts", Name: "Role"}}},
			{Name: "cartLabel", Line: 9, Calls: []graph.Call{{Target: "src/user/name.ts", Name: "full"}, {Target: "src/cart/items.ts", Name: "count"}},
				Refs: []graph.Call{{Target: "src/user/name.ts", Name: "User"}, {Target: "src/user/role.ts", Name: "Role"}}},
		}}},
		{Path: "src/user/name.ts", Package: "src/user", Language: graph.TypeScript, Usage: &graph.Usage{Scope: "."}},
		{Path: "src/user/role.ts", Package: "src/user", Language: graph.TypeScript, Usage: &graph.Usage{Scope: "."}},
		{Path: "src/cart/items.ts", Package: "src/cart", Language: graph.TypeScript, Usage: &graph.Usage{Scope: "."}},
	})
	fs, err := New(st, key).Evaluate(ctx, rules.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range byRule(fs, rules.RuleMisplacedFunction) {
		got = append(got, f.Message)
	}
	want := []string{
		"pkg/report/pdf.go:20 invoiceTotal uses nothing of pkg/report and 3 names of pkg/billing (Invoice, Tax, Total): it belongs there",
		"src/cart/total.ts:5 userLabel uses nothing of src/cart and 3 names of src/user (Role, User, full): it belongs there",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("misplaced-function =\n %q, want\n %q", got, want)
	}
}
