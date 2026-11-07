package rules

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
	graphrepo "github.com/nonchan7720/depgraph/pkg/infrastructure/repository/graph"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver/imports"
)

func upsertAll(ctx context.Context, t *testing.T, st *graphrepo.Store, key string, files []*graph.FileGraph) {
	t.Helper()
	r := graphrepo.ForProject(st, key)
	t.Cleanup(func() { _ = r.Clear(context.Background()) })
	for _, fg := range files {
		if err := r.UpsertFile(ctx, fg); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.RebuildAllPackageImports(ctx); err != nil {
		t.Fatal(err)
	}
}

func byRule(fs []rules.Finding, rule string) []rules.Finding {
	var out []rules.Finding
	for _, f := range fs {
		if f.Rule == rule {
			out = append(out, f)
		}
	}
	return out
}

func messages(fs []rules.Finding) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Message
	}
	sort.Strings(out)
	return out
}

// Layers may name external packages: a domain that imports a framework is
// a layer violation; the standard library is not a framework unless it is
// named exactly, and ! leaves packages out.
func TestEvaluateExternalLayers(t *testing.T) {
	ctx, st := connect(t)
	key := fmt.Sprintf("zz_test_%d_extlayers", time.Now().UnixNano())
	ext := func(name string, std bool) graph.Import {
		return graph.Import{Spec: name, Target: "ext:" + name, External: true, Std: std}
	}
	upsertAll(ctx, t, st, key, []*graph.FileGraph{
		{Path: "pkg/domain/user.go", Package: "pkg/domain", Language: graph.Go, Imports: []graph.Import{
			ext("fmt", true), ext("net/http", true), ext("gorm.io/gorm", false), ext("github.com/google/uuid", false),
		}},
		{Path: "pkg/infra/db.go", Package: "pkg/infra", Language: graph.Go, Imports: []graph.Import{ext("gorm.io/gorm", false)}},
	})
	c := rules.DefaultConfig()
	c.DefaultLayers = false
	c.Layers = []rules.Layer{
		{Name: "domain", Packages: []string{"pkg/domain/**"}},
		{Name: "frameworks", Packages: []string{"ext:*", "!ext:github.com/google/uuid"}},
		{Name: "http", Packages: []string{"ext:net/http"}},
	}
	c.ForbiddenLayers = [][2]string{{"domain", "frameworks"}, {"domain", "http"}}
	c.AbstractLayers = nil
	fs, err := New(st, key).Evaluate(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	got := messages(byRule(fs, rules.RuleLayerViolation))
	want := []string{
		"pkg/domain -> ext:gorm.io/gorm (domain must not import frameworks)",
		"pkg/domain -> ext:net/http (domain must not import http)",
	}
	if !slices.Equal(got, want) {
		t.Errorf("layer violations = %q, want %q", got, want)
	}
}

// The store evaluates a layer pair exactly like rules.LayerPair.Reports: on
// random graphs of in-repo, third-party and standard library packages, the
// imports reported are those Reports accepts.
func TestLayerViolationsMatchTheDomain(t *testing.T) {
	ctx, st := connect(t)
	rng := rand.New(rand.NewPCG(7, 11))
	inRepo := []string{"app/domain", "app/domain/legacy", "app/usecase", "app/infra/db", "app/infra/http", "svc/domain", "svc/infra", "tools"}
	external := []rules.PackageRef{ // Std / Python: set per importer below
		{Key: "ext:fmt", External: true},
		{Key: "ext:net/http", External: true},
		{Key: "ext:gorm.io/gorm", External: true},
		{Key: "ext:gorm.io/gorm/clause", External: true},
		{Key: "ext:github.com/google/uuid", External: true},
		{Key: "ext:json", External: true},
		{Key: "ext:sqlalchemy.orm", External: true},
		{Key: "ext:lodash.debounce", External: true},
	}
	c := rules.DefaultConfig()
	c.Layers = []rules.Layer{
		{Name: "domain", Packages: []string{"**/domain/**", "!app/domain/legacy"}},
		{Name: "usecase", Packages: []string{"**/usecase/**"}},
		{Name: "infra", Packages: []string{"**/infra/**", "ext:gorm.io/gorm", "ext:sqlalchemy", "ext:lodash"}},
		{Name: "frameworks", Packages: []string{"ext:*", "!ext:github.com/google/uuid", "!ext:lodash"}},
		{Name: "http", Packages: []string{"ext:net/http", "app/infra/http"}},
	}
	c.ForbiddenLayers = [][2]string{{"domain", "infra"}, {"domain", "frameworks"}, {"domain", "http"}, {"usecase", "infra"}, {"infra", "http"}}
	c.AbstractLayers = nil
	c.Components = []rules.Component{{Dir: "svc", Layers: []rules.Layer{
		{Name: "core", Packages: []string{"domain/**"}}, {Name: "outer", Packages: []string{"infra/**", "ext:*"}},
	}, ForbiddenLayers: [][2]string{{"core", "outer"}}}}
	pairs, err := c.LayerPairs()
	if err != nil {
		t.Fatal(err)
	}
	reported := 0
	for g := range 10 {
		key := fmt.Sprintf("zz_test_%d_layers%d", time.Now().UnixNano(), g)
		var files []*graph.FileGraph
		var want []string
		for i, from := range inRepo {
			// Go and Python importers: the standard library and dotted
			// submodules depend on the importing language
			lang, file := graph.Go, from+"/f.go"
			if (i+g)%2 == 1 {
				lang, file = graph.Python, from+"/f.py"
			}
			fg := &graph.FileGraph{Path: file, Package: from, Language: lang}
			for _, to := range inRepo {
				if to != from && rng.Float64() < 0.4 {
					fg.Imports = append(fg.Imports, graph.Import{Spec: to, Target: to})
					want = appendReported(want, pairs, rules.PackageRef{Key: from}, rules.PackageRef{Key: to})
				}
			}
			for _, to := range external {
				if rng.Float64() < 0.5 {
					fg.Imports = append(fg.Imports, graph.Import{Spec: to.Key[4:], Target: to.Key, External: true})
					to.Std, to.Python = imports.IsStdlib(lang, to.Key), lang == graph.Python
					want = appendReported(want, pairs, rules.PackageRef{Key: from}, to)
				}
			}
			files = append(files, fg)
		}
		upsertAll(ctx, t, st, key, files)
		fs, err := New(st, key).Evaluate(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, f := range byRule(fs, rules.RuleLayerViolation) {
			got = append(got, fmt.Sprintf("%v>%v:%v", f.Data["from"], f.Data["to"], f.Data["toLayer"]))
		}
		sort.Strings(got)
		got = slices.Compact(got)
		sort.Strings(want)
		want = slices.Compact(want)
		if !slices.Equal(got, want) {
			t.Fatalf("graph %d: store reports\n%s\nthe domain\n%s", g, strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
		reported += len(want)
	}
	if reported == 0 {
		t.Fatal("no graph had a violation: the comparison proves nothing")
	}
}

func appendReported(out []string, pairs []rules.LayerPair, from, to rules.PackageRef) []string {
	for _, p := range pairs {
		if p.Reports(from, to) {
			out = append(out, fmt.Sprintf("%s>%s:%s", from.Key, to.Key, p.To))
		}
	}
	return out
}

// concrete-dependency: a use case holding an implementation of the
// repository layer (a struct with methods, a non-abstract class with
// methods) is reported once per type; interfaces, abstract classes and data
// structs are not.
func TestEvaluateConcreteDependency(t *testing.T) {
	ctx, st := connect(t)
	key := fmt.Sprintf("zz_test_%d_concrete", time.Now().UnixNano())
	dep := func(holder, member string, line int, target, typ string) graph.TypeDep {
		return graph.TypeDep{Holder: holder, Member: member, Line: line, Target: target, Type: typ}
	}
	upsertAll(ctx, t, st, key, []*graph.FileGraph{
		{Path: "app/usecase/user.go", Package: "app/usecase", Language: graph.Go,
			Imports: []graph.Import{{Spec: "m/app/repository", Target: "app/repository"}},
			Symbols: []graph.Symbol{{Name: "UserUsecase", Kind: "struct", Exported: true}},
			TypeDeps: []graph.TypeDep{
				dep("UserUsecase", "repo", 8, "app/repository", "MySQLUserRepo"),
				dep("NewUserUsecase", "repo", 12, "app/repository", "MySQLUserRepo"),
				dep("UserUsecase", "users", 9, "app/repository", "UserRepository"),
				dep("UserUsecase", "opts", 10, "app/repository", "Options"),
				dep("UserUsecase", "row", 11, "app/repository", "Row"),
			}},
		{Path: "app/repository/mysql.go", Package: "app/repository", Language: graph.Go, Symbols: []graph.Symbol{
			{Name: "MySQLUserRepo", Kind: "struct", Exported: true},
			{Name: "Options", Kind: "struct", Exported: true},
			{Name: "UserRepository", Kind: "interface", Exported: true},
		}},
		// the methods of a Go type may be in another file of its package
		{Path: "app/repository/mysql_find.go", Package: "app/repository", Language: graph.Go, Symbols: []graph.Symbol{
			{Name: "Find", Kind: "method", Exported: true, Receiver: "MySQLUserRepo"},
			// what data implements (fmt.Stringer, json.Marshaler, ...) is not behaviour
			{Name: "Row", Kind: "struct", Exported: true},
			{Name: "String", Kind: "method", Exported: true, Receiver: "Row"},
			{Name: "MarshalJSON", Kind: "method", Exported: true, Receiver: "Row"},
		}},
		// a test's helper method or its own type of the same name is not the package's
		{Path: "app/repository/mysql_test.go", Package: "app/repository", Language: graph.Go, Symbols: []graph.Symbol{
			{Name: "helper", Kind: "method", Receiver: "Options"},
			{Name: "UserRepository", Kind: "struct", Exported: true},
			{Name: "fake", Kind: "method", Receiver: "UserRepository"},
		}},
		{Path: "web/usecase/order.ts", Package: "web/usecase", Language: graph.TypeScript, TypeDeps: []graph.TypeDep{
			dep("OrderService", "repo", 3, "web/repository/order.ts", "HttpOrderRepo"),
			dep("OrderService", "base", 4, "web/repository/order.ts", "BaseRepo"),
			// a default import: the class the file exports as default
			dep("OrderService", "sql", 5, "web/repository/sql.ts", "default"),
		}},
		{Path: "web/repository/sql.ts", Package: "web/repository", Language: graph.TypeScript,
			Symbols: []graph.Symbol{{Name: "SqlOrderRepo", Kind: "class", Exported: true, Methods: 1}},
			Usage:   &graph.Usage{Exports: []graph.ExportName{{Name: "default", Local: "SqlOrderRepo"}}}},
		{Path: "web/repository/order.ts", Package: "web/repository", Language: graph.TypeScript, Symbols: []graph.Symbol{
			{Name: "HttpOrderRepo", Kind: "class", Exported: true, Methods: 2},
			{Name: "BaseRepo", Kind: "class", Exported: true, Methods: 2, Abstract: true},
		}},
	})
	fs, err := New(st, key).Evaluate(ctx, rules.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	got := byRule(fs, rules.RuleConcreteDependency)
	var ids []string
	for _, f := range got {
		ids = append(ids, f.ID)
		if f.Severity != "warn" {
			t.Errorf("%s: severity %s", f.ID, f.Severity)
		}
	}
	want := []string{"app/usecase -> app/repository.MySQLUserRepo", "web/usecase -> web/repository.HttpOrderRepo",
		"web/usecase -> web/repository.SqlOrderRepo"}
	if !slices.Equal(ids, want) {
		t.Fatalf("concrete dependencies = %q, want %q", ids, want)
	}
	if !strings.Contains(got[0].Message, "app/usecase/user.go:8 UserUsecase.repo, and 1 more") {
		t.Errorf("message = %q", got[0].Message)
	}

	c := rules.DefaultConfig()
	c.Settings = map[string]rules.RuleSetting{rules.RuleConcreteDependency: {Disabled: true}}
	if fs, err := New(st, key).Evaluate(ctx, c); err != nil || len(byRule(fs, rules.RuleConcreteDependency)) != 0 {
		t.Errorf("disabled: %v, %v", byRule(fs, rules.RuleConcreteDependency), err)
	}
}

// large-package counts the lines of a package's source files, without tests
// and generated files.
func TestEvaluateLargePackage(t *testing.T) {
	ctx, st := connect(t)
	key := fmt.Sprintf("zz_test_%d_large", time.Now().UnixNano())
	upsertAll(ctx, t, st, key, []*graph.FileGraph{
		{Path: "pkg/cmd/a.go", Package: "pkg/cmd", Language: graph.Go, Lines: 70},
		{Path: "pkg/cmd/b.go", Package: "pkg/cmd", Language: graph.Go, Lines: 40},
		{Path: "pkg/cmd/a_test.go", Package: "pkg/cmd", Language: graph.Go, Lines: 900},
		{Path: "pkg/cmd/gen.go", Package: "pkg/cmd", Language: graph.Go, Lines: 900, Usage: &graph.Usage{Generated: true}},
		{Path: "pkg/small/a.go", Package: "pkg/small", Language: graph.Go, Lines: 99},
		{Path: "web/src/a.ts", Package: "web/src", Language: graph.TypeScript, Lines: 500},
	})
	c := rules.DefaultConfig()
	c.LargePackage.MaxLines = 100
	fs, err := New(st, key).Evaluate(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	got := messages(byRule(fs, rules.RuleLargePackage))
	want := []string{"pkg/cmd has 110 lines in 2 files (limit 100): split it by responsibility"}
	if !slices.Equal(got, want) {
		t.Errorf("large packages = %q, want %q", got, want)
	}
}

// Whether an external package is the standard library depends on the
// language of the importing file, not on the package node (ext:yaml is
// Ruby's standard library and an npm package), whatever order the files
// were stored in; and it does not depend on what the graph stored (a graph
// from before std existed).
func TestEvaluateExternalLayersPerLanguage(t *testing.T) {
	ctx, st := connect(t)
	c := rules.DefaultConfig()
	c.DefaultLayers = false
	c.Layers = []rules.Layer{
		{Name: "domain", Packages: []string{"lib/**", "web/**", "go/**"}},
		{Name: "frameworks", Packages: []string{"ext:*"}},
	}
	c.ForbiddenLayers = [][2]string{{"domain", "frameworks"}}
	c.AbstractLayers = nil
	rb := &graph.FileGraph{Path: "lib/a.rb", Package: "lib", Language: graph.Ruby,
		Imports: []graph.Import{{Spec: "yaml", Target: "ext:yaml", External: true, Std: true}}}
	ts := &graph.FileGraph{Path: "web/b.ts", Package: "web", Language: graph.TypeScript,
		Imports: []graph.Import{{Spec: "yaml", Target: "ext:yaml", External: true}}}
	// stored without std
	gofile := &graph.FileGraph{Path: "go/c.go", Package: "go", Language: graph.Go,
		Imports: []graph.Import{{Spec: "fmt", Target: "ext:fmt", External: true}}}
	for i, files := range [][]*graph.FileGraph{{rb, ts, gofile}, {ts, rb, gofile}} {
		key := fmt.Sprintf("zz_test_%d_extlang%d", time.Now().UnixNano(), i)
		upsertAll(ctx, t, st, key, files)
		fs, err := New(st, key).Evaluate(ctx, c)
		if err != nil {
			t.Fatal(err)
		}
		got := messages(byRule(fs, rules.RuleLayerViolation))
		want := []string{"web -> ext:yaml (domain must not import frameworks)"}
		if !slices.Equal(got, want) {
			t.Errorf("order %d: %q, want %q", i, got, want)
		}
	}
}
