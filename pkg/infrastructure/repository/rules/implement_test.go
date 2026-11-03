package rules

import (
	"context"
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
	graphrepo "github.com/nonchan7720/depgraph/pkg/infrastructure/repository/graph"
)

func get(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// Each project's findings come only from its own graph.
func TestEvaluateIsScopedByProject(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	st, err := graphrepo.New(ctx, get("NEO4J_URI", "bolt://localhost:7687"), get("NEO4J_USER", "neo4j"),
		get("NEO4J_PASSWORD", "depgraph123"), get("NEO4J_DATABASE", "neo4j"))
	if err != nil {
		t.Skipf("neo4j not available: %v", err)
	}
	// t.Cleanup runs LIFO: the store is closed after the data cleanups below.
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	if err := st.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixNano()
	dirty, clean := fmt.Sprintf("zz_test_%d_dirty", now), fmt.Sprintf("zz_test_%d_clean", now)
	file := func(pkg, imp string, syms ...string) *graph.FileGraph {
		fg := &graph.FileGraph{Path: pkg + "/f.go", Package: pkg, Language: graph.Go}
		for _, s := range syms {
			fg.Symbols = append(fg.Symbols, graph.Symbol{Name: s, Kind: "function", Exported: true})
		}
		if imp != "" {
			fg.Imports = []graph.Import{{Spec: imp, Target: imp}}
		}
		return fg
	}
	for _, p := range []struct {
		key   string
		files []*graph.FileGraph
	}{
		{dirty, []*graph.FileGraph{file("handler", "repository", "Dup"), file("repository", "handler", "Dup")}},
		{clean, []*graph.FileGraph{file("handler", "", "Dup"), file("repository", "", "Other")}},
	} {
		r := graphrepo.ForProject(st, p.key)
		t.Cleanup(func() { _ = r.Clear(context.Background()) })
		for _, fg := range p.files {
			if err := r.UpsertFile(ctx, fg); err != nil {
				t.Fatal(err)
			}
		}
		if err := r.RebuildAllPackageImports(ctx); err != nil {
			t.Fatal(err)
		}
	}
	cfg := rules.DefaultConfig()
	cfg.GodPackageFanIn, cfg.GodPackageFanOut = 1, 1
	count := func(key string) map[string]int {
		fs, err := New(st, key).Evaluate(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]int{}
		for _, f := range fs {
			out[f.Rule]++
		}
		return out
	}
	if got := count(dirty); got["import-cycle"] != 1 || got["layer-violation"] != 1 || got["god-package"] != 2 || got["duplicate-symbol"] != 1 {
		t.Errorf("dirty findings = %v", got)
	}
	if got := count(clean); len(got) != 0 {
		t.Errorf("clean project sees other project's findings: %v", got)
	}
}

// Default layers match whole path segments: "db" is not part of "feedback",
// "handler" is not "eventhandler". Before the fix layer names were matched by
// substring on the package key and both edges below were reported.
func TestEvaluateDefaultLayersMatchWholeSegments(t *testing.T) {
	ctx, st := connect(t)
	key := fmt.Sprintf("zz_test_%d_layers", time.Now().UnixNano())
	seed(ctx, t, st, key, map[string][]string{
		"pkg/eventhandler": {"pkg/feedback"},   // substring only: no violation
		"pkg/handler":      {"pkg/repository"}, // whole segments: violation
		"pkg/repository":   nil,
		"pkg/feedback":     nil,
	})
	fs, err := New(st, key).Evaluate(ctx, rules.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range fs {
		if f.Rule == "layer-violation" {
			got = append(got, fmt.Sprintf("%v->%v", f.Data["from"], f.Data["to"]))
		}
	}
	if len(got) != 1 || got[0] != "pkg/handler->pkg/repository" {
		t.Errorf("layer violations = %v, want only pkg/handler->pkg/repository", got)
	}
}

// Layers from the configuration (globs), per-rule severity / enabled, and
// ignored packages.
func TestEvaluateWithConfig(t *testing.T) {
	ctx, st := connect(t)
	key := fmt.Sprintf("zz_test_%d_config", time.Now().UnixNano())
	seed(ctx, t, st, key, map[string][]string{
		"pkg/domain/a":          {"pkg/infrastructure/db", "pkg/domain/b"},
		"pkg/domain/b":          {"pkg/domain/c"}, // a hub once fanOut is 1
		"pkg/domain/c":          nil,
		"pkg/infrastructure/db": {"pkg/domain/b"},
		"pkg/app":               {"pkg/domain/b", "pkg/infrastructure/db"},
		// ignored: neither a violation nor counted in fan-in nor a cycle
		"pkg/domain/testdata/x": {"pkg/infrastructure/db", "pkg/domain/testdata/y"},
		"pkg/domain/testdata/y": {"pkg/domain/testdata/x"},
	})
	base := rules.Config{
		Layers: []rules.Layer{
			{Name: "domain", Packages: []string{"pkg/domain/**"}},
			{Name: "infrastructure", Packages: []string{"pkg/infrastructure/**"}},
		},
		ForbiddenLayers:      [][2]string{{"domain", "infrastructure"}},
		GodPackageFanIn:      3,
		GodPackageFanOut:     1,
		Ignore:               []string{"**/testdata/**"},
		IgnoreDuplicateNames: nil,
	}
	eval := func(cfg rules.Config) map[string][]rules.Finding {
		t.Helper()
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
		fs, err := New(st, key).Evaluate(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string][]rules.Finding{}
		for _, f := range fs {
			out[f.Rule] = append(out[f.Rule], f)
		}
		return out
	}

	got := eval(base)
	if lv := got["layer-violation"]; len(lv) != 1 || lv[0].Data["from"] != "pkg/domain/a" || lv[0].Data["to"] != "pkg/infrastructure/db" || lv[0].Severity != "error" {
		t.Errorf("layer violations = %+v", lv)
	}
	// pkg/domain/b is imported by a, db and app (the ignored x does not count)
	if gp := got["god-package"]; len(gp) != 1 || gp[0].ID != "pkg/domain/b" {
		t.Errorf("god packages = %+v", gp)
	}
	if c := got["import-cycle"]; len(c) != 0 {
		t.Errorf("cycle through ignored packages must not be reported: %+v", c)
	}

	cfg := base
	cfg.Settings = map[string]rules.RuleSetting{
		rules.RuleLayerViolation: {Severity: rules.SeverityWarn},
		rules.RuleGodPackage:     {Disabled: true},
	}
	got = eval(cfg)
	if lv := got["layer-violation"]; len(lv) != 1 || lv[0].Severity != "warn" {
		t.Errorf("severity override: %+v", lv)
	}
	if len(got["god-package"]) != 0 {
		t.Errorf("disabled rule reported: %+v", got["god-package"])
	}

	// without ignore the testdata cycle and its layer violation show up
	cfg = base
	cfg.Ignore = nil
	got = eval(cfg)
	if len(got["import-cycle"]) != 1 || len(got["layer-violation"]) != 2 {
		t.Errorf("without ignore: %+v", got)
	}
}

// Duplicate symbols in ignored packages are not reported.
func TestDuplicateSymbolIgnore(t *testing.T) {
	ctx, st := connect(t)
	key := fmt.Sprintf("zz_test_%d_dup", time.Now().UnixNano())
	r := graphrepo.ForProject(st, key)
	t.Cleanup(func() { _ = r.Clear(context.Background()) })
	for _, p := range []string{"a", "b", "a/testdata"} {
		fg := &graph.FileGraph{Path: p + "/f.go", Package: p, Language: graph.Go,
			Symbols: []graph.Symbol{{Name: "Dup", Kind: "function", Exported: true}, {Name: "Fix", Kind: "function", Exported: true}}}
		if p != "a/testdata" {
			fg.Symbols = fg.Symbols[:1]
		}
		if err := r.UpsertFile(ctx, fg); err != nil {
			t.Fatal(err)
		}
	}
	cfg := rules.DefaultConfig()
	cfg.Ignore = []string{"**/testdata/**"}
	fs, err := New(st, key).Evaluate(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var dups []string
	for _, f := range fs {
		if f.Rule == "duplicate-symbol" {
			dups = append(dups, f.Message)
		}
	}
	if len(dups) != 1 || dups[0] != `function "Dup" declared in 2 files: a/f.go, b/f.go` {
		t.Errorf("duplicates = %q", dups)
	}
}

// connect opens the test store or skips when Neo4j is unreachable.
func connect(t *testing.T) (context.Context, *graphrepo.Store) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	st, err := graphrepo.New(ctx, get("NEO4J_URI", "bolt://localhost:7687"), get("NEO4J_USER", "neo4j"),
		get("NEO4J_PASSWORD", "depgraph123"), get("NEO4J_DATABASE", "neo4j"))
	if err != nil {
		t.Skipf("neo4j not available: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	if err := st.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	return ctx, st
}

// seed stores one Go file per package importing the given packages under
// project key (a zz_test_* key, cleared afterwards).
func seed(ctx context.Context, t *testing.T, st *graphrepo.Store, key string, pkgs map[string][]string) {
	t.Helper()
	r := graphrepo.ForProject(st, key)
	t.Cleanup(func() { _ = r.Clear(context.Background()) })
	for pkg, imps := range pkgs {
		fg := &graph.FileGraph{Path: pkg + "/f.go", Package: pkg, Language: graph.Go}
		for _, imp := range imps {
			fg.Imports = append(fg.Imports, graph.Import{Spec: imp, Target: imp})
		}
		if err := r.UpsertFile(ctx, fg); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.RebuildAllPackageImports(ctx); err != nil {
		t.Fatal(err)
	}
}

// Copies of the same logic are found from the stored code shapes; test code
// (table tests look alike on purpose) and ignored packages are left out, and
// the rule follows .depgraph.yml (enabled / severity).
func TestEvaluateDuplicateCode(t *testing.T) {
	ctx, st := connect(t)
	key := fmt.Sprintf("zz_test_%d_dupcode", time.Now().UnixNano())
	g := graphrepo.ForProject(st, key)
	t.Cleanup(func() { _ = g.Clear(context.Background()) })
	fp := func(lo int64) []int64 {
		var out []int64
		for i := lo; i < lo+20; i++ {
			out = append(out, i)
		}
		return out
	}
	sym := func(name string, line int, hash string, f []int64) graph.Symbol {
		return graph.Symbol{Name: name, Kind: "function", Exported: true, Line: line, EndLine: line + 9,
			Tokens: 120, CloneHash: hash, Fingerprints: f}
	}
	for _, fg := range []*graph.FileGraph{
		{Path: "a/a.go", Package: "a", Language: graph.Go, Symbols: []graph.Symbol{sym("Total", 3, "h1", fp(0))}},
		{Path: "b/b.go", Package: "b", Language: graph.Go, Symbols: []graph.Symbol{sym("Sum", 10, "h1", fp(0)), sym("Other", 30, "h2", fp(500))}},
		{Path: "a/a_test.go", Package: "a", Language: graph.Go, Symbols: []graph.Symbol{sym("TestX", 1, "h1", fp(0))}},
		{Path: "gen/z.go", Package: "gen", Language: graph.Go, Symbols: []graph.Symbol{sym("Generated", 1, "h1", fp(0))}},
	} {
		if err := g.UpsertFile(ctx, fg); err != nil {
			t.Fatal(err)
		}
	}
	eval := func(cfg rules.Config) []rules.Finding {
		t.Helper()
		fs, err := New(st, key).Evaluate(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		var got []rules.Finding
		for _, f := range fs {
			if f.Rule == rules.RuleDuplicateCode {
				got = append(got, f)
			}
		}
		return got
	}
	cfg := rules.DefaultConfig()
	cfg.Ignore = []string{"gen/**"}
	got := eval(cfg)
	want := "identical logic in 2 places: a/a.go:3-12 Total, b/b.go:10-19 Sum"
	if len(got) != 1 || got[0].Message != want || got[0].Severity != "warn" {
		t.Fatalf("duplicate-code = %+v, want [%q]", got, want)
	}
	cfg.Settings = map[string]rules.RuleSetting{rules.RuleDuplicateCode: {Severity: rules.SeverityError}}
	if got := eval(cfg); len(got) != 1 || got[0].Severity != "error" {
		t.Errorf("severity override: %+v", got)
	}
	cfg.Settings = map[string]rules.RuleSetting{rules.RuleDuplicateCode: {Disabled: true}}
	if got := eval(cfg); len(got) != 0 {
		t.Errorf("disabled: %+v", got)
	}
}

// A god package is a hub: many packages import it AND it imports many in-repo
// packages. A stable package many depend on (a domain model importing
// nothing in the repository) is healthy (Stable Dependencies Principle) and
// is not reported.
func TestGodPackageIsAHub(t *testing.T) {
	ctx, st := connect(t)
	key := fmt.Sprintf("zz_test_%d_god", time.Now().UnixNano())
	pkgs := map[string][]string{
		"model": nil,                       // stable: imports nothing
		"hub":   {"model", "dep1", "dep2"}, // imports 3 in-repo packages
		"dep1":  nil, "dep2": nil,
	}
	for i := range 10 {
		pkgs[fmt.Sprintf("user%d", i)] = []string{"model", "hub"}
	}
	seed(ctx, t, st, key, pkgs)
	fs, err := New(st, key).Evaluate(ctx, rules.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range fs {
		if f.Rule == rules.RuleGodPackage {
			got = append(got, f.Message)
		}
	}
	want := []string{"hub is imported by 10 packages and imports 3"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("god packages = %q, want %q", got, want)
	}
	// the fan-out threshold is configurable
	cfg := rules.DefaultConfig()
	cfg.GodPackageFanOut = 4
	if fs, err = New(st, key).Evaluate(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	for _, f := range fs {
		if f.Rule == rules.RuleGodPackage {
			t.Errorf("fanOut 4: %s", f.Message)
		}
	}
}

// In Go a name declared twice in one package does not compile unless build
// constraints separate the files (x_unix.go / x_windows.go): those are one
// declaration per build, not a duplicate. Other packages declaring the name
// still are.
func TestDuplicateSymbolBuildVariants(t *testing.T) {
	ctx, st := connect(t)
	key := fmt.Sprintf("zz_test_%d_dupvariant", time.Now().UnixNano())
	r := graphrepo.ForProject(st, key)
	t.Cleanup(func() { _ = r.Clear(context.Background()) })
	file := func(path, pkg string, lang graph.Language, names ...string) *graph.FileGraph {
		fg := &graph.FileGraph{Path: path, Package: pkg, Language: lang}
		for _, n := range names {
			fg.Symbols = append(fg.Symbols, graph.Symbol{Name: n, Kind: "function", Exported: true})
		}
		return fg
	}
	for _, fg := range []*graph.FileGraph{
		file("a/sig_unix.go", "a", graph.Go, "IgnoreSignals", "Both"),
		file("a/sig_windows.go", "a", graph.Go, "IgnoreSignals", "Both"),
		file("b/b.go", "b", graph.Go, "Both"),
		// TypeScript has no build variants: two files of one directory do clash
		file("web/x.ts", "web", graph.TypeScript, "render"),
		file("web/y.ts", "web", graph.TypeScript, "render"),
	} {
		if err := r.UpsertFile(ctx, fg); err != nil {
			t.Fatal(err)
		}
	}
	cfg := rules.DefaultConfig()
	cfg.IgnoreDuplicateNames = nil
	fs, err := New(st, key).Evaluate(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range fs {
		if f.Rule == rules.RuleDuplicateSymbol {
			got = append(got, f.Message)
		}
	}
	sort.Strings(got)
	want := []string{
		`function "Both" declared in 3 files: a/sig_unix.go, a/sig_windows.go, b/b.go`,
		`function "render" declared in 2 files: web/x.ts, web/y.ts`,
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("duplicates =\n%q\nwant\n%q", got, want)
	}
}
