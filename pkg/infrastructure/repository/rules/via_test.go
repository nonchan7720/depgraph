package rules

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// A call through a field follows the field's declared type: to the method
// of a struct, or through an interface to its only implementation. An
// interface with two implementations is not followed.
func TestEvaluateFunctionCycleThroughMembers(t *testing.T) {
	ctx, st := connect(t)
	key := fmt.Sprintf("zz_test_%d_via", time.Now().UnixNano())
	sym := func(name, kind string, methods ...string) graph.Symbol {
		return graph.Symbol{Name: name, Kind: kind, Exported: true, Methods: methods}
	}
	method := func(recv, name string) graph.Symbol { return graph.Symbol{Name: name, Kind: "method", Receiver: recv} }
	viaCall := func(caller, owner, member, name string) graph.FuncCalls {
		return graph.FuncCalls{Name: caller, Calls: []graph.Call{{Name: name, Via: owner + "|" + member}}}
	}
	file := func(path string, syms []graph.Symbol, u graph.Usage) *graph.FileGraph {
		u.Scope = "."
		return &graph.FileGraph{Path: path, Package: graph.PackageKey(path), Language: graph.Go, Symbols: syms, Usage: &u}
	}
	own := func(owner, member, typ string) graph.OwnMember {
		return graph.OwnMember{Owner: owner, Member: member, Type: typ}
	}
	upsertAll(ctx, t, st, key, []*graph.FileGraph{
		// svc.Svc.Run -> (Store, one implementation) store.Pg.Save -> svc.audit -> (param *Svc) Svc.Run
		file("svc/svc.go", []graph.Symbol{sym("Store", "interface", "Save"), sym("Svc", "struct"), method("Svc", "Run"),
			{Name: "audit", Kind: "function"}},
			graph.Usage{OwnMembers: []graph.OwnMember{own("Svc", "store", "Store"), own("audit", "s", "Svc")},
				Calls: []graph.FuncCalls{viaCall("Svc.Run", "Svc", "store", "Save"), viaCall("audit", "audit", "s", "Run")}}),
		{Path: "store/pg.go", Package: "store", Language: graph.Go,
			Symbols: []graph.Symbol{sym("Pg", "struct"), method("Pg", "Save")},
			Imports: []graph.Import{{Spec: "svc", Target: "svc"}},
			Usage:   &graph.Usage{Scope: ".", Calls: []graph.FuncCalls{{Name: "Pg.Save", Calls: []graph.Call{{Target: "svc", Name: "audit"}}}}}},
		// two implementations: which Send runs is not known
		file("mail/mail.go", []graph.Symbol{sym("Sender", "interface", "Send"), sym("Box", "struct"), method("Box", "Flush"),
			sym("SMTP", "struct"), method("SMTP", "Send"), sym("Fake", "struct"), method("Fake", "Send")},
			graph.Usage{OwnMembers: []graph.OwnMember{own("Box", "out", "Sender")},
				Calls: []graph.FuncCalls{viaCall("Box.Flush", "Box", "out", "Send")}}),
		// an interface embedding another: its method set is not all known
		file("io/io.go", []graph.Symbol{{Name: "ReadCloser", Kind: "interface", Methods: []string{"Close"}, Embeds: true},
			sym("Pipe", "struct"), method("Pipe", "Drain")},
			graph.Usage{OwnMembers: []graph.OwnMember{own("Pipe", "rc", "ReadCloser"), own("closeAll", "p", "Pipe")},
				Calls: []graph.FuncCalls{viaCall("Pipe.Drain", "Pipe", "rc", "Close")}}),
		file("io/file.go", []graph.Symbol{{Name: "closeAll", Kind: "function"}, sym("File", "struct"), method("File", "Close")}, graph.Usage{
			Calls: []graph.FuncCalls{{Name: "File.Close", Calls: []graph.Call{{Name: "closeAll"}}}, viaCall("closeAll", "closeAll", "p", "Drain")}}),
		file("mail/smtp.go", nil, graph.Usage{Calls: []graph.FuncCalls{viaCall("SMTP.Send", "SMTP", "box", "Flush")},
			OwnMembers: []graph.OwnMember{own("SMTP", "box", "Box")}}),
	})
	fs, err := New(st, key).Evaluate(ctx, rules.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range byRule(fs, rules.RuleFunctionCycle) {
		got = append(got, f.Message)
	}
	want := []string{"store/pg.go Pg.Save -> svc/svc.go audit -> svc/svc.go Svc.Run -> store/pg.go Pg.Save"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("function-cycle =\n %q, want\n %q", got, want)
	}
}

// wide-function counts the package that declares the type of a member a
// function calls through (the interface), not the packages that implement it.
func TestEvaluateWideFunctionThroughMembers(t *testing.T) {
	ctx, st := connect(t)
	key := fmt.Sprintf("zz_test_%d_via_wide", time.Now().UnixNano())
	var files []*graph.FileGraph
	run := graph.FuncCalls{Name: "App.Run", Line: 7}
	app := &graph.FileGraph{Path: "app/app.go", Package: "app", Language: graph.Go,
		Symbols: []graph.Symbol{{Name: "App", Kind: "struct"}, {Name: "Run", Kind: "method", Receiver: "App"}},
		Usage:   &graph.Usage{Scope: "."}}
	for _, p := range []string{"p1", "p2", "p3", "p4"} {
		files = append(files, &graph.FileGraph{Path: p + "/port.go", Package: p, Language: graph.Go,
			Symbols: []graph.Symbol{{Name: "Port", Kind: "interface", Exported: true, Methods: []string{"Do"}}}, Usage: &graph.Usage{Scope: "."}})
		app.TypeUses = append(app.TypeUses, graph.TypeUse{Owner: "App", Member: p, Role: graph.RoleField, Line: 3, Target: p, Type: "Port"})
		run.Calls = append(run.Calls, graph.Call{Name: "Do", Via: "App|" + p})
	}
	app.Usage.Calls = []graph.FuncCalls{run}
	upsertAll(ctx, t, st, key, append(files, app))
	cfg := rules.DefaultConfig()
	cfg.MaxCalledPackages = 3
	fs, err := New(st, key).Evaluate(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := byRule(fs, rules.RuleWideFunction)
	if len(got) != 1 || got[0].Message != "app/app.go:7 App.Run calls functions of 4 packages (limit 3): p1, p2, p3, p4" {
		t.Errorf("wide-function = %+v", got)
	}
}
