package rules

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
	graphrepo "github.com/nonchan7720/depgraph/pkg/infrastructure/repository/graph"
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
	ext := func(name string, thirdParty bool) graph.Import {
		im := graph.Import{Spec: name, Target: "ext:" + name, External: true}
		if thirdParty {
			im.Class = graph.ClassThirdParty
		}
		return im
	}
	upsertAll(ctx, t, st, key, []*graph.FileGraph{
		{Path: "pkg/domain/user.go", Package: "pkg/domain", Language: graph.Go, Imports: []graph.Import{
			ext("fmt", false), ext("net/http", false), ext("gorm.io/gorm", true), ext("github.com/google/uuid", true),
		}},
		{Path: "pkg/infra/db.go", Package: "pkg/infra", Language: graph.Go, Imports: []graph.Import{ext("gorm.io/gorm", true)}},
	})
	c := rules.DefaultConfig()
	c.DefaultLayers = false
	c.Layers = []rules.Layer{
		{Name: "domain", Packages: []string{"pkg/domain/**"}},
		{Name: "frameworks", Packages: []string{"ext:*", "!ext:github.com/google/uuid"}},
		{Name: "http", Packages: []string{"ext:net/http"}},
	}
	c.AbstractLayers = nil
	c.ForbiddenLayers = [][2]string{{"domain", "frameworks"}, {"domain", "http"}}
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
	external := []rules.PackageRef{ // ThirdParty / Python: set per import below
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
	c.AbstractLayers = nil
	c.ForbiddenLayers = [][2]string{{"domain", "infra"}, {"domain", "frameworks"}, {"domain", "http"}, {"usecase", "infra"}, {"infra", "http"}}
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
					im := graph.Import{Spec: to.Key[4:], Target: to.Key, External: true}
					// what the resolver may have classified: some imports
					// third-party, the others standard or unknown
					if rng.Float64() < 0.6 {
						im.Class = graph.ClassThirdParty
					}
					fg.Imports = append(fg.Imports, im)
					// a Python import is classified when the rules are
					// evaluated, not from the edge: without a languages:
					// setting it is unknown (only exact patterns apply)
					to.Python = lang == graph.Python
					to.ThirdParty = im.Class == graph.ClassThirdParty && !to.Python
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

// A wildcard follows the class the resolver gave each import, not the
// package node: ext:yaml is an npm package one file imports and a Ruby
// library another one requires, whatever order they were stored in; an
// import without a class (an older graph, an unclassified language) is
// never matched by a wildcard.
func TestEvaluateExternalLayersPerImport(t *testing.T) {
	ctx, st := connect(t)
	c := rules.DefaultConfig()
	c.DefaultLayers = false
	c.Layers = []rules.Layer{
		{Name: "domain", Packages: []string{"lib/**", "web/**", "go/**"}},
		{Name: "frameworks", Packages: []string{"ext:*"}},
	}
	c.AbstractLayers = nil
	c.ForbiddenLayers = [][2]string{{"domain", "frameworks"}}
	rb := &graph.FileGraph{Path: "lib/a.rb", Package: "lib", Language: graph.Ruby,
		Imports: []graph.Import{{Spec: "yaml", Target: "ext:yaml", External: true}}}
	ts := &graph.FileGraph{Path: "web/b.ts", Package: "web", Language: graph.TypeScript,
		Imports: []graph.Import{{Spec: "yaml", Target: "ext:yaml", External: true, Class: graph.ClassThirdParty, Basis: "installed in node_modules"}}}
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

// An import a wildcard would match if it were known to be third-party is
// not silently dropped: one info finding per language lists them with why
// they could not be classified. Imports a layer names exactly, or leaves
// out, are decided and not listed; nothing is listed without a wildcard.
func TestEvaluateExternalUnclassified(t *testing.T) {
	ctx, st := connect(t)
	key := fmt.Sprintf("zz_test_%d_unclassified", time.Now().UnixNano())
	upsertAll(ctx, t, st, key, []*graph.FileGraph{
		{Path: "app/domain/a.py", Package: "app/domain", Language: graph.Python, Imports: []graph.Import{
			{Spec: "yaml", Target: "ext:yaml", External: true},
			{Spec: "requests", Target: "ext:requests", External: true},
			{Spec: "sqlalchemy.orm", Target: "ext:sqlalchemy.orm", External: true}, // named exactly
			{Spec: "attr", Target: "ext:attr", External: true},                     // left out
		}},
		{Path: "app/domain/b.go", Package: "app/domain", Language: graph.Go, Imports: []graph.Import{
			{Spec: "github.com/x/y", Target: "ext:github.com/x/y", External: true, Basis: "go.mod does not require it"},
			{Spec: "fmt", Target: "ext:fmt", External: true, Class: graph.ClassStandard, Basis: "Go standard library"},
		}},
		// not in a layer that must not import frameworks
		{Path: "app/infra/c.py", Package: "app/infra", Language: graph.Python, Imports: []graph.Import{
			{Spec: "boto3", Target: "ext:boto3", External: true},
		}},
	})
	c := rules.DefaultConfig()
	c.DefaultLayers = false
	c.Layers = []rules.Layer{
		{Name: "domain", Packages: []string{"app/domain/**"}},
		{Name: "frameworks", Packages: []string{"ext:*", "ext:sqlalchemy", "!ext:attr"}},
	}
	c.AbstractLayers = nil
	c.ForbiddenLayers = [][2]string{{"domain", "frameworks"}}
	fs, err := New(st, key).Evaluate(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	got := byRule(fs, rules.RuleExternalUnclassified)
	if len(got) != 2 {
		t.Fatalf("findings = %+v", got)
	}
	for _, f := range got {
		if f.Severity != "info" {
			t.Errorf("%s: severity %s", f.ID, f.Severity)
		}
	}
	if got[0].ID != "go" || !strings.Contains(got[0].Message, "ext:github.com/x/y (go.mod does not require it)") {
		t.Errorf("go: %+v", got[0])
	}
	if got[1].ID != "python" || !strings.Contains(got[1].Message, "2 imports") ||
		!strings.Contains(got[1].Message, "ext:requests") || !strings.Contains(got[1].Message, "ext:yaml") ||
		strings.Contains(got[1].Message, "sqlalchemy") || strings.Contains(got[1].Message, "ext:attr") {
		t.Errorf("python: %+v", got[1])
	}

	// a package the importing layer names itself does not cross the pair
	c.Layers[0].Packages = append(c.Layers[0].Packages, "ext:github.com/x/y")
	fs, err = New(st, key).Evaluate(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range byRule(fs, rules.RuleExternalUnclassified) {
		if f.ID == "go" {
			t.Errorf("listed although the domain layer names it: %+v", f)
		}
	}
	c.Layers[1].Packages = []string{"ext:sqlalchemy"} // no wildcard: nothing to classify
	if fs, err := New(st, key).Evaluate(ctx, c); err != nil || len(byRule(fs, rules.RuleExternalUnclassified)) != 0 {
		t.Errorf("without a wildcard: %v, %v", byRule(fs, rules.RuleExternalUnclassified), err)
	}
}

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// Python imports are classified when the project is evaluated, from the
// version its languages: setting names and the typeshed copy of its type
// checker: what is not the standard library of that version is
// third-party. A file without a setting, or under a directory that pins
// another version, is not classified, and is listed instead.
func TestEvaluatePythonExternalLayers(t *testing.T) {
	ctx, st := connect(t)
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".python-version": "3.12.4\n",
		"node_modules/pyright/dist/typeshed-fallback/stdlib/VERSIONS": "os: 3.0-\ntomllib: 3.11-\n",
		"lib/legacy/.python-version":                                  "3.9\n",
	})
	pyimp := func(name string) graph.Import { return graph.Import{Spec: name, Target: "ext:" + name, External: true} }
	key := fmt.Sprintf("zz_test_%d_pylayers", time.Now().UnixNano())
	upsertAll(ctx, t, st, key, []*graph.FileGraph{
		{Path: "app/domain/a.py", Package: "app/domain", Language: graph.Python,
			Imports: []graph.Import{pyimp("yaml"), pyimp("os.path"), pyimp("tomllib"), pyimp("sqlalchemy.orm")}},
		{Path: "lib/legacy/domain/b.py", Package: "lib/legacy/domain", Language: graph.Python, Imports: []graph.Import{pyimp("requests")}},
		{Path: "tools/domain/c.py", Package: "tools/domain", Language: graph.Python, Imports: []graph.Import{pyimp("click")}},
	})
	c := rules.DefaultConfig()
	c.DefaultLayers = false
	c.Layers = []rules.Layer{
		{Name: "domain", Packages: []string{"**/domain/**"}},
		{Name: "frameworks", Packages: []string{"ext:*"}},
		{Name: "orm", Packages: []string{"ext:sqlalchemy"}},
	}
	c.AbstractLayers = nil
	c.ForbiddenLayers = [][2]string{{"domain", "frameworks"}, {"domain", "orm"}}
	c.Languages = []rules.LanguageSetting{
		{Dir: "app", Lang: "python", VersionFile: "../.python-version"},
		{Dir: "lib", Lang: "python", VersionFile: "../.python-version"},
	}
	fs, err := New(st, key).WithRoot(root).Evaluate(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	got := messages(byRule(fs, rules.RuleLayerViolation))
	want := []string{
		"app/domain -> ext:sqlalchemy.orm (domain must not import frameworks)",
		"app/domain -> ext:sqlalchemy.orm (domain must not import orm)",
		"app/domain -> ext:yaml (domain must not import frameworks)",
	}
	if !slices.Equal(got, want) {
		t.Errorf("layer violations = %q, want %q", got, want)
	}
	un := byRule(fs, rules.RuleExternalUnclassified)
	if len(un) != 1 || un[0].ID != "python" {
		t.Fatalf("unclassified = %+v", un)
	}
	for _, s := range []string{"ext:requests (lib/legacy/.python-version pins 3.9", "ext:click (no languages.python setting"} {
		if !strings.Contains(un[0].Message, s) {
			t.Errorf("unclassified %q lacks %q", un[0].Message, s)
		}
	}
	// without the project's files nothing is classified
	fs, err = New(st, key).Evaluate(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if got := messages(byRule(fs, rules.RuleLayerViolation)); slices.Contains(got, "app/domain -> ext:yaml (domain must not import frameworks)") {
		t.Errorf("classified without the files: %q", got)
	}
}

// Ruby: requires and constants are classified from the bundle's
// Gemfile.lock: a gem the Gemfile asks for is third-party; a gem locked only
// as another's dependency and a constant that is probably a gem's but not
// by its name are listed; one that is likely Ruby's own is not.
func TestEvaluateRubyExternalLayers(t *testing.T) {
	ctx, st := connect(t)
	root := t.TempDir()
	writeTree(t, root, map[string]string{"Gemfile.lock": "GEM\n  specs:\n    actionpack (7.1.3)\n    activerecord (7.1.3)\n    rails (7.1.3)\n      actionpack (= 7.1.3)\n      activerecord (= 7.1.3)\n    sidekiq (7.2.0)\n\nDEPENDENCIES\n  rails\n  sidekiq\n"})
	key := fmt.Sprintf("zz_test_%d_rblayers", time.Now().UnixNano())
	upsertAll(ctx, t, st, key, []*graph.FileGraph{
		{Path: "app/domain/order.rb", Package: "app/domain", Language: graph.Ruby,
			Imports: []graph.Import{{Spec: "sidekiq", Target: "ext:sidekiq", External: true}},
			Usage:   &graph.Usage{Scope: ".", ExtConstants: []string{"ActionController", "ActiveRecord", "Hash", "Rails"}}},
	})
	c := rules.DefaultConfig()
	c.DefaultLayers = false
	c.Layers = []rules.Layer{{Name: "domain", Packages: []string{"app/domain/**"}}, {Name: "frameworks", Packages: []string{"ext:*"}}}
	c.AbstractLayers = nil
	c.ForbiddenLayers = [][2]string{{"domain", "frameworks"}}
	fs, err := New(st, key).WithRoot(root).Evaluate(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	got := messages(byRule(fs, rules.RuleLayerViolation))
	want := []string{
		"app/domain -> ext:Rails (domain must not import frameworks)",
		"app/domain -> ext:sidekiq (domain must not import frameworks)",
	}
	if !slices.Equal(got, want) {
		t.Errorf("violations = %q, want %q", got, want)
	}
	un := byRule(fs, rules.RuleExternalUnclassified)
	if len(un) != 1 || un[0].ID != "ruby" || !strings.Contains(un[0].Message, "ext:ActionController (probably gem actionpack") ||
		!strings.Contains(un[0].Message, "ext:ActiveRecord (gem activerecord is locked only as a dependency") ||
		strings.Contains(un[0].Message, "Hash") {
		t.Errorf("unclassified = %+v", un)
	}
}
