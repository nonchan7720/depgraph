package rules

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// projectTree writes files (path -> content) under a new directory.
func projectTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// importing is a file of lang governed by the manifest in scope that
// imports the external packages.
func importing(rel string, lang graph.Language, scope string, ext ...string) *graph.FileGraph {
	fg := &graph.FileGraph{Path: rel, Package: graph.PackageKey(rel), Language: lang, Usage: &graph.Usage{Scope: scope}}
	for _, e := range ext {
		fg.Imports = append(fg.Imports, graph.Import{Spec: e, Target: "ext:" + e, External: true})
	}
	return fg
}

// sevMessages are the findings as "severity message".
func sevMessages(fs []rules.Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Severity+" "+f.Message)
	}
	return out
}

const depsGoMod = `module example.com/app

go 1.24

require (
	github.com/used/lib v1.0.0
	github.com/unused/lib v1.2.0
	github.com/aws/sdk v1.0.0
	github.com/aws/sdk/service/s3 v1.0.0
	github.com/tool/gen v0.1.0
	github.com/excluded/lib v1.0.0
	github.com/elsewhere/lib v1.0.0
	golang.org/x/indirect v0.1.0 // indirect
)

tool github.com/tool/gen/cmd/gen
`

const depsPackageJSON = `{
  "name": "web",
  "scripts": {"build": "tsc -p . && vite build"},
  "dependencies": {
    "react": "18.3.1",
    "left-pad": "1.3.0",
    "clsx": "2.1.0",
    "moved": "1.0.0"
  },
  "devDependencies": {
    "@types/react": "18.3.1",
    "eslint": "9.0.0",
    "eslint-plugin-x": "1.0.0",
    "typescript": "5.4.5",
    "vite": "5.2.0",
    "hoisted": "1.0.0",
    "in-ci": "1.0.0"
  }
}
`

func depsProject(t *testing.T) (string, []*graph.FileGraph, rules.Config) {
	t.Helper()
	files := map[string]string{
		"go.mod":                       depsGoMod,
		"main.go":                      "package main\n",
		"gen/zz_generated.go":          "package gen\n\nimport _ \"github.com/excluded/lib\"\n",
		"tools/go.mod":                 "module example.com/app/tools\n\nrequire github.com/elsewhere/lib v1.0.0\n",
		"tools/tools.go":               "package tools\n",
		"web/package.json":             depsPackageJSON,
		"web/src/main.ts":              "",
		"web/eslint.config.js":         "",
		"web/packages/ui/ui.ts":        "",
		"web/packages/ui/package.json": `{"name": "ui", "dependencies": {"web": "*"}}`,
		"other/package.json":           `{"name": "other", "dependencies": {"moved": "1.0.0"}}`,
		"other/index.js":               "",
		"smoke.mjs":                    "",
		".github/workflows/ci.yml":     "steps:\n  - run: npx in-ci --check\n",
	}
	for _, p := range []string{"react", "left-pad", "clsx", "moved", "@types/react", "eslint", "eslint-plugin-x", "vite", "hoisted", "in-ci"} {
		files["web/node_modules/"+p+"/package.json"] = `{}`
	}
	files["web/node_modules/typescript/package.json"] = `{"bin": {"tsc": "bin/tsc"}}`
	files["other/node_modules/moved/package.json"] = `{}`
	root := projectTree(t, files)
	graphs := []*graph.FileGraph{
		// the s3 client is its own module: the import does not use github.com/aws/sdk
		importing("main.go", graph.Go, ".", "github.com/used/lib/sub", "github.com/aws/sdk/service/s3/types", "fmt"),
		// another module's import of a module this one requires too
		importing("tools/tools.go", graph.Go, "tools", "github.com/elsewhere/lib"),
		importing("web/src/main.ts", graph.TypeScript, "web", "react"),
		importing("web/eslint.config.js", graph.JavaScript, "web", "eslint"),
		// a workspace package uses what the package.json above it installs
		importing("web/packages/ui/ui.ts", graph.TypeScript, "web/packages/ui", "clsx"),
		// another package's import of a package web declares too
		importing("other/index.js", graph.JavaScript, "other", "moved"),
		// no package.json governs this file: what it imports is installed by one below it
		importing("smoke.mjs", graph.JavaScript, ".", "hoisted"),
	}
	cfg := rules.DefaultConfig()
	cfg.Modules = []string{"other", "tools", "web", "web/packages/ui"}
	return root, graphs, cfg
}

// A dependency no code of its module imports is reported: for certain
// (error) when nothing else names it either, as doubtful (warn) when
// something may load it without an import. Not reported: what a file other
// than code names (a script, a workflow at the root, an excluded Go file),
// a go.mod tool, the types of a used package. An import uses the
// declaration of the nearest manifest that names the package: what another
// module declares and imports itself is that module's, what a file outside
// every declaring module imports is used.
func TestEvaluateUnusedDependency(t *testing.T) {
	ctx, st := connect(t)
	key := fmt.Sprintf("zz_test_%d_deps", time.Now().UnixNano())
	root, graphs, cfg := depsProject(t)
	upsertAll(ctx, t, st, key, graphs)
	repo := New(st, key).WithRoot(root)
	fs, err := repo.Evaluate(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := sevMessages(byRule(fs, rules.RuleUnusedDependency))
	want := []string{
		"error go.mod:8 github.com/aws/sdk is declared but nothing imports it or names it",
		"error go.mod:12 github.com/elsewhere/lib is declared but nothing imports it or names it",
		"error go.mod:7 github.com/unused/lib is declared but nothing imports it or names it",
		"warn web/package.json:13 eslint-plugin-x is declared but nothing imports it or names it (not certain: its name marks it as an add-on of eslint, which may load it without an import)",
		"error web/package.json:6 left-pad is declared but nothing imports it or names it",
		"error web/package.json:8 moved is declared but nothing imports it or names it",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("unused-dependency =\n %q, want\n %q", got, want)
	}

	// the same graph again: nothing is linked twice, the findings are the same
	again, err := repo.Evaluate(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(sevMessages(byRule(again, rules.RuleUnusedDependency)), want) {
		t.Errorf("second evaluation = %q", sevMessages(byRule(again, rules.RuleUnusedDependency)))
	}

	// a manifest that changed is read again, without any change of a source file
	if err := os.WriteFile(filepath.Join(root, "other/package.json"), []byte(`{"name": "other", "dependencies": {"moved": "1.0.0", "added": "1.0.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	fs, err = repo.Evaluate(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	added := "warn other/package.json:1 added is declared but nothing imports it or names it (not certain: it is not installed, so its binaries and its peers could not be read)"
	if got := sevMessages(byRule(fs, rules.RuleUnusedDependency)); len(got) != len(want)+1 || got[len(got)-4] != added {
		t.Errorf("after adding a dependency = %q", got)
	}
}

// ignore names dependencies never reported, the package ignore whole
// directories; the severity setting moves both levels; without the
// project's files, or with the rule off, nothing is reported.
func TestUnusedDependencySettings(t *testing.T) {
	ctx, st := connect(t)
	key := fmt.Sprintf("zz_test_%d_deps_cfg", time.Now().UnixNano())
	root, graphs, cfg := depsProject(t)
	upsertAll(ctx, t, st, key, graphs)
	repo := New(st, key).WithRoot(root)
	eval := func(r *Repository, cfg rules.Config) []string {
		t.Helper()
		fs, err := r.Evaluate(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		return sevMessages(byRule(fs, rules.RuleUnusedDependency))
	}

	c := cfg
	c.IgnoreDependencies = []string{"github.com/*", "left-pad"}
	c.Ignore = []string{"other"}
	c.Settings = map[string]rules.RuleSetting{rules.RuleUnusedDependency: {Severity: rules.SeverityWarn}}
	want := []string{
		"info web/package.json:13 eslint-plugin-x is declared but nothing imports it or names it (not certain: its name marks it as an add-on of eslint, which may load it without an import)",
		"warn web/package.json:8 moved is declared but nothing imports it or names it",
	}
	if got := eval(repo, c); !reflect.DeepEqual(got, want) {
		t.Errorf("with settings =\n %q, want\n %q", got, want)
	}

	c = cfg
	c.Settings = map[string]rules.RuleSetting{rules.RuleUnusedDependency: {Disabled: true}}
	if got := eval(repo, c); len(got) != 0 {
		t.Errorf("disabled = %q", got)
	}
	if got := eval(New(st, key), cfg); len(got) != 0 {
		t.Errorf("without the project's files = %q", got)
	}
	if got := eval(repo, cfg); len(got) != 6 {
		t.Errorf("enabled again = %q", got)
	}
}
