package rules

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// A Go package whose files fall into two groups nothing connects, used by
// different packages, and a TypeScript module whose declarations do, are
// reported; tests and a package that is used whole are not.
func TestEvaluateLowCohesion(t *testing.T) {
	ctx, st := connect(t)
	key := fmt.Sprintf("zz_test_%d_cohesion", time.Now().UnixNano())
	gof := func(path string, decls, idents []string, uses ...graph.Use) *graph.FileGraph {
		fg := &graph.FileGraph{Path: path, Package: graph.PackageKey(path), Language: graph.Go,
			Usage: &graph.Usage{Scope: ".", Decls: decls, Idents: idents, Uses: uses}}
		for _, d := range decls { // functions, unless a test says otherwise
			fg.Symbols = append(fg.Symbols, graph.Symbol{Name: d, Kind: "function", Exported: true})
		}
		return fg
	}
	use := func(target, name string) graph.Use { return graph.Use{Target: target, Name: name} }
	withSyms := func(fg *graph.FileGraph, syms ...graph.Symbol) *graph.FileGraph { fg.Symbols = syms; return fg }
	upsertAll(ctx, t, st, key, []*graph.FileGraph{
		gof("pkg/util/parse.go", []string{"Parse", "token"}, []string{"Lex", "token"}),
		gof("pkg/util/lex.go", []string{"Lex"}, nil),
		gof("pkg/util/render.go", []string{"Render", "Escape"}, []string{"Escape"}),
		gof("pkg/config/c.go", nil, nil, use("pkg/util", "Parse")),
		gof("pkg/cli/c.go", nil, nil, use("pkg/util", "Lex"), use("pkg/util", "Parse")),
		gof("pkg/ui/u.go", nil, nil, use("pkg/util", "Render")),
		gof("pkg/util/util_test.go", nil, nil, use("pkg/util", "Render"), use("pkg/util", "Parse")), // a test does not join them
		// used whole by a dot import: not reported
		gof("pkg/dot/a.go", []string{"A"}, nil), gof("pkg/dot/b.go", []string{"B"}, nil), gof("pkg/dot/c.go", []string{"C"}, nil),
		gof("pkg/x/x.go", nil, nil, use("pkg/dot", "A"), use("pkg/dot", "*")),
		// entities, one per consumer: data, not two responsibilities
		withSyms(gof("pkg/model/user.go", []string{"User", "NewUser"}, nil),
			graph.Symbol{Name: "User", Kind: "struct", Exported: true}, graph.Symbol{Name: "NewUser", Kind: "function", Exported: true},
			graph.Symbol{Name: "String", Kind: "method", Exported: true, Receiver: "User"}),
		withSyms(gof("pkg/model/order.go", []string{"Order", "Status"}, nil), graph.Symbol{Name: "Order", Kind: "struct", Exported: true}),
		gof("pkg/svcuser/s.go", nil, nil, use("pkg/model", "User"), use("pkg/model", "NewUser")),
		gof("pkg/svcorder/s.go", nil, nil, use("pkg/model", "Order"), use("pkg/model", "Status")),
		gof("pkg/y/y.go", nil, nil, use("pkg/dot", "B"), use("pkg/dot", "C")),
		// TypeScript: render and parse halves of one module
		{Path: "web/lib/text.ts", Package: "web/lib", Language: graph.TypeScript, Symbols: []graph.Symbol{
			{Name: "split", Kind: "function"}, {Name: "join", Kind: "function"}, {Name: "renderHtml", Kind: "function"},
			{Name: "renderText", Kind: "function"}, {Name: "escape", Kind: "function"}, {Name: "SEP", Kind: "variable"},
		}, Usage: &graph.Usage{Scope: ".", DeclRefs: []graph.DeclRef{
			{Name: "SEP"}, {Name: "split", Refs: []string{"SEP"}}, {Name: "join", Refs: []string{"SEP"}},
			{Name: "renderHtml"}, {Name: "escape"}, {Name: "renderText", Refs: []string{"escape"}},
		}}},
		{Path: "web/a.ts", Package: "web", Language: graph.TypeScript, Usage: &graph.Usage{Scope: ".", Uses: []graph.Use{use("web/lib/text.ts", "split"), use("web/lib/text.ts", "join")}}},
		{Path: "web/b.ts", Package: "web/b", Language: graph.TypeScript, Usage: &graph.Usage{Scope: ".", Uses: []graph.Use{use("web/lib/text.ts", "renderHtml")}}},
		{Path: "web/c.ts", Package: "web/c", Language: graph.TypeScript, Usage: &graph.Usage{Scope: ".", Uses: []graph.Use{use("web/lib/text.ts", "renderText")}}},
		// Python: a module of dataclasses, one per consumer, and a constant: data
		{Path: "app/models.py", Package: "app", Language: graph.Python, Symbols: []graph.Symbol{
			{Name: "User", Kind: "class", Data: true, Methods: []string{"full_name"}}, {Name: "Order", Kind: "class", Data: true},
			{Name: "LIMIT", Kind: "variable"},
		}, Usage: &graph.Usage{Scope: ".", DeclRefs: []graph.DeclRef{{Name: "User"}, {Name: "Order"}, {Name: "LIMIT"}}}},
		{Path: "app/users.py", Package: "app/users", Language: graph.Python, Usage: &graph.Usage{Scope: ".", Uses: []graph.Use{use("app/models.py", "User")}}},
		{Path: "app/orders.py", Package: "app/orders", Language: graph.Python, Usage: &graph.Usage{Scope: ".", Uses: []graph.Use{use("app/models.py", "Order")}}},
		{Path: "app/limits.py", Package: "app/limits", Language: graph.Python, Usage: &graph.Usage{Scope: ".", Uses: []graph.Use{use("app/models.py", "LIMIT")}}},
	})
	fs, err := New(st, key).Evaluate(ctx, rules.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	got := byRule(fs, rules.RuleLowCohesion)
	var ids []string
	for _, f := range got {
		ids = append(ids, f.ID)
	}
	// web/lib/text.ts: renderHtml (b) and renderText (c) are two groups
	// with different users too, so three groups
	if strings.Join(ids, ",") != "pkg/util,web/lib/text.ts" {
		t.Fatalf("low cohesion = %v", ids)
	}
	if !strings.Contains(got[0].Message, "lex.go, parse.go (Lex, Parse) used by pkg/cli, pkg/config; render.go (Render) used by pkg/ui") {
		t.Errorf("message = %q", got[0].Message)
	}
	if !strings.Contains(got[1].Message, "module web/lib/text.ts has 3 parts") {
		t.Errorf("message = %q", got[1].Message)
	}
}
