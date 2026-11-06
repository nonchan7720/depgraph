package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

func writeFiles(t *testing.T, root string, files map[string]string) {
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

func componentByDir(c rules.Config, dir string) (rules.Component, bool) {
	for _, comp := range c.Components {
		if comp.Dir == dir {
			return comp, true
		}
	}
	return rules.Component{}, false
}

// components: in the root's file declares the layers of parts of the
// repository, by directory or by a pattern over directories.
func TestParseComponents(t *testing.T) {
	f, err := Parse([]byte(`
components:
  "packages/*":
    layers:
      ui: ["src/components/**"]
      api: ["src/api/**"]
    forbidden:
      api: [ui]
  lib/auth:
    forbidden:
      domain: [infra]
`))
	if err != nil {
		t.Fatal(err)
	}
	if !f.Rules.DefaultLayers {
		t.Error("the root's layers are still the defaults")
	}
	pk, ok := componentByDir(f.Rules, "packages/*")
	if !ok || pk.DefaultLayers || len(pk.Layers) != 2 || !reflect.DeepEqual(pk.ForbiddenLayers, [][2]string{{"api", "ui"}}) {
		t.Errorf("packages/* = %+v", pk)
	}
	la, ok := componentByDir(f.Rules, "lib/auth")
	if !ok || !la.DefaultLayers || !reflect.DeepEqual(la.Layers, rules.DefaultConfig().Layers) ||
		!reflect.DeepEqual(la.ForbiddenLayers, [][2]string{{"domain", "infra"}}) {
		t.Errorf("lib/auth = %+v", la)
	}

	g, err := Parse([]byte("layers: {a: [\"a/**\"]}\n"))
	if err != nil || g.Rules.DefaultLayers {
		t.Errorf("declared root layers: DefaultLayers = %v, %v", g.Rules.DefaultLayers, err)
	}

	for src, want := range map[string]string{
		"components: {\"apps/**\": {forbidden: {domain: [infra]}}}\n":         `component "apps/**"`,
		"components: {web: {forbidden: {a: [b]}}}\n":                          `component web: forbidden a -> b: unknown layer "a"`,
		"components: {web: {layers: {a: [\"a/**\"]}, severity: warn}}\n":      "severity",
		"components: {web: {layers: {a: [\"a/**\"]}, forbidden: {a: [a]}}}\n": "cannot forbid itself",
	} {
		if _, err := Parse([]byte(src)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: err = %v, want it to contain %q", src, err, want)
		}
	}
}

// A .depgraph.yml below the root is the configuration of that directory as a
// component: its layers, ignore and unused-code paths are relative to it.
// Settings only the root decides (severity, thresholds) are reported as
// warnings, not applied; project and store apply when it is scanned as the
// root itself.
func TestLoadNestedFiles(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".depgraph.yml": "rules:\n  unreachable-file: {entries: [\"scripts/**\"]}\n",
		"lib/auth/.depgraph.yml": `
project: auth
store: {persist: true}
layers:
  domain: ["domain/**"]
  adapter: ["adapter/**"]
forbidden:
  domain: [adapter]
ignore: ["**/testdata/**"]
rules:
  unused-export: {ignore: ["api/**"], severity: info}
  god-package: {fanIn: 3}
`,
		"lib/ui/.depgraph.yml":              "forbidden:\n  handler: [repository]\n",
		"packages/.depgraph.yml":            "components:\n  \"*\":\n    layers: {ui: [\"src/ui/**\"], api: [\"src/api/**\"]}\n    forbidden: {api: [ui]}\n",
		"node_modules/x/.depgraph.yml":      "this is not: [valid\n",
		".claude/worktrees/a/.depgraph.yml": "layers: {a: [\"a/**\"]}\nforbidden: {a: [b]}\n",
	})
	f, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	c := f.Rules
	if f.Project != "" || f.Store.Persist != nil {
		t.Errorf("project / store of a nested file applied: %q %v", f.Project, f.Store.Persist)
	}
	auth, ok := componentByDir(c, "lib/auth")
	if !ok || auth.DefaultLayers || !reflect.DeepEqual(auth.ForbiddenLayers, [][2]string{{"domain", "adapter"}}) {
		t.Errorf("lib/auth = %+v", auth)
	}
	if ui, ok := componentByDir(c, "lib/ui"); !ok || !ui.DefaultLayers || !reflect.DeepEqual(ui.ForbiddenLayers, [][2]string{{"handler", "repository"}}) {
		t.Errorf("lib/ui = %+v", ui)
	}
	if pk, ok := componentByDir(c, "packages/*"); !ok || len(pk.Layers) != 2 {
		t.Errorf("packages/* (components: of packages/.depgraph.yml) = %+v", pk)
	}
	if !reflect.DeepEqual(c.Ignore, []string{"lib/auth/**/testdata/**"}) {
		t.Errorf("ignore = %v", c.Ignore)
	}
	if !reflect.DeepEqual(c.UnusedIgnore, []string{"lib/auth/api/**"}) || !reflect.DeepEqual(c.UnusedEntries, []string{"scripts/**"}) {
		t.Errorf("unused ignore = %v, entries = %v", c.UnusedIgnore, c.UnusedEntries)
	}
	if c.GodPackageFanIn != rules.DefaultConfig().GodPackageFanIn || c.Severity(rules.RuleUnusedExport) != rules.SeverityWarn {
		t.Errorf("root-only settings of a nested file applied: fanIn %d, severity %s", c.GodPackageFanIn, c.Severity(rules.RuleUnusedExport))
	}
	want := []string{
		"lib/auth/.depgraph.yml: rules.god-package.fanIn applies only where lib/auth is the root (--root lib/auth); the root's setting is used",
		"lib/auth/.depgraph.yml: rules.unused-export.severity applies only where lib/auth is the root (--root lib/auth); the root's setting is used",
	}
	if !reflect.DeepEqual(f.Warnings, want) {
		t.Errorf("warnings =\n  %s\nwant\n  %s", strings.Join(f.Warnings, "\n  "), strings.Join(want, "\n  "))
	}
}

// An invalid nested file is an error that names it, like the root's.
func TestLoadNestedErrorNamesFile(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"services/api/.depgraph.yml": "layers: {a: [\"a/**\"]}\nforbidden: {a: [b]}\n",
	})
	_, err := Load(root)
	if err == nil || !strings.Contains(err.Error(), filepath.Join(root, "services", "api", FileName)) || !strings.Contains(err.Error(), `unknown layer "b"`) {
		t.Errorf("err = %v", err)
	}
}

// The same directory declared by components: of the root and by its own file.
func TestLoadNestedAndComponentsConflict(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		".depgraph.yml":     "components:\n  web: {forbidden: {domain: [infra]}}\n",
		"web/.depgraph.yml": "forbidden: {handler: [db]}\n",
	})
	if _, err := Load(root); err == nil || !strings.Contains(err.Error(), "component web defined twice") {
		t.Errorf("err = %v", err)
	}
}

// The directories of the repository's modules (go.mod, package.json,
// pyproject.toml, setup.py, setup.cfg) are the units of module-cycle; the
// root and skipped directories are not.
func TestLoadFindsModules(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"go.mod":                         "module example.com/m\n",
		"package.json":                   "{}",
		"lib/auth/go.mod":                "module example.com/m/lib/auth\n",
		"lib/ui/package.json":            "{}",
		"packages/web/package.json":      "{}",
		"packages/web/e2e/package.json":  "{}",
		"py/core/pyproject.toml":         "",
		"py/legacy/setup.py":             "",
		"py/old/setup.cfg":               "",
		"node_modules/x/package.json":    "{}",
		"packages/web/dist/package.json": "{}",
		".claude/worktrees/a/go.mod":     "module x\n",
		"docs/readme.md":                 "",
	})
	f, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"lib/auth", "lib/ui", "packages/web", "packages/web/e2e", "py/core", "py/legacy", "py/old"}
	if !reflect.DeepEqual(f.Rules.Modules, want) {
		t.Errorf("modules = %v, want %v", f.Rules.Modules, want)
	}
}
