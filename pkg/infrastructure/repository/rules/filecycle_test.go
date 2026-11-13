package rules

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// Files of one directory that load each other are reported; a cycle across
// directories is import-cycle's, and a module loaded for nothing (no loads)
// closes no cycle.
func TestEvaluateFileCycle(t *testing.T) {
	ctx, st := connect(t)
	key := fmt.Sprintf("zz_test_%d_filecycle", time.Now().UnixNano())
	file := func(path string, lang graph.Language, loads ...graph.Use) *graph.FileGraph {
		fg := &graph.FileGraph{Path: path, Package: graph.PackageKey(path), Language: lang,
			Usage: &graph.Usage{Scope: ".", Loads: loads}}
		for _, l := range loads { // the dependency edges the resolver stores with them
			if dir := graph.PackageKey(l.Target); l.Target != "" && dir != fg.Package {
				fg.Imports = append(fg.Imports, graph.Import{Spec: l.Target, Target: dir})
			}
		}
		return fg
	}
	load := func(target string, name ...string) graph.Use {
		u := graph.Use{Target: target}
		if len(name) > 0 {
			u.Name = name[0]
		}
		return u
	}
	withSyms := func(fg *graph.FileGraph, names ...string) *graph.FileGraph {
		for _, n := range names {
			fg.Symbols = append(fg.Symbols, graph.Symbol{Name: n, Kind: "variable"})
		}
		return fg
	}
	ts, py := graph.TypeScript, graph.Python
	upsertAll(ctx, t, st, key, []*graph.FileGraph{
		// a pair in one directory
		file("src/store/cart.ts", ts, load("src/store/user.ts")),
		file("src/store/user.ts", ts, load("src/store/cart.ts"), load("src/store/util.ts")),
		file("src/store/util.ts", ts),
		// one way only: types erased on the way back
		file("src/model/order.ts", ts, load("src/model/item.ts")),
		file("src/model/item.ts", ts),
		// across directories: import-cycle reports it
		file("src/x/a.ts", ts, load("src/y/b.ts")),
		file("src/y/b.ts", ts, load("src/x/a.ts")),
		// Python: a package whose __init__ imports a module that imports a
		// name of the package back, and two modules through `from . import`
		withSyms(file("app/__init__.py", py, load("app/core.py", "run")), "VERSION"),
		file("app/core.py", py, load("app", "VERSION"), load("app", "jobs")),
		file("app/jobs.py", py, load("app", "tasks.send")),
		file("app/tasks.py", py, load("app/jobs.py", "Job")),
		// hidden behind an import inside a function
		file("app/views.py", py, load("app/forms.py", "Form")),
		file("app/forms.py", py, graph.Use{Target: "app/views.py", Name: "index", As: graph.LoadDeferred}),
		// a module imports a name of its package that the package only passes
		// on from another module: it depends on that module, not on the package
		withSyms(file("lib/__init__.py", py, load("lib/registry.py", "register"), load("lib/checks.py", "check"), load("lib/star.py")),
			"__version__"),
		file("lib/registry.py", py),
		file("lib/checks.py", py, load("lib", "register")),
		file("lib/star.py", py, load("lib", "from_star")), // may come from a wildcard import: not known
		// ... unless the two modules do form a cycle
		withSyms(file("pkg2/__init__.py", py, load("pkg2/a.py", "A"), load("pkg2/b.py", "B"))),
		file("pkg2/a.py", py, load("pkg2", "B")),
		file("pkg2/b.py", py, load("pkg2", "A")),
		// a module that imports its own package as a module (models.X at call
		// time): the package was loaded before the module, whatever it imports
		withSyms(file("orm/__init__.py", py, load("orm/deletion.py", "Collector"))),
		file("orm/deletion.py", py, load("orm"), load(".", "orm")),
		file("__init__.py", py),
		// ignored package
		file("vendor/p.ts", ts, load("vendor/q.ts")),
		file("vendor/q.ts", ts, load("vendor/p.ts")),
	})
	cfg := rules.DefaultConfig()
	cfg.Ignore = []string{"vendor/**"}
	fs, err := New(st, key).Evaluate(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range byRule(fs, rules.RuleFileCycle) {
		got = append(got, f.Message)
		if f.Severity != "warn" {
			t.Errorf("severity = %s", f.Severity)
		}
	}
	want := []string{
		"app/__init__.py -> app/core.py -> app/__init__.py",
		"app/forms.py -> app/views.py -> app/forms.py (hidden by an import inside a function of app/forms.py)",
		"app/jobs.py -> app/tasks.py -> app/jobs.py",
		"pkg2/a.py -> pkg2/b.py -> pkg2/a.py",
		"src/store/cart.ts -> src/store/user.ts -> src/store/cart.ts",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("file-cycle =\n %q, want\n %q", got, want)
	}
	if n := len(byRule(fs, rules.RuleImportCycle)); n != 1 {
		t.Errorf("import-cycle findings = %d, want the one across src/x and src/y", n)
	}

	cfg.FileCycleDeferred = false
	fs, err = New(st, key).Evaluate(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(byRule(fs, rules.RuleFileCycle)); n != 4 {
		t.Errorf("deferred: false reported %d findings, want 4", n)
	}

	cfg.Settings = map[string]rules.RuleSetting{rules.RuleFileCycle: {Disabled: true}}
	fs, err = New(st, key).Evaluate(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(byRule(fs, rules.RuleFileCycle)); n != 0 {
		t.Errorf("disabled rule reported %d findings", n)
	}
}
