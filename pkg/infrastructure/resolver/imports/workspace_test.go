package imports

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// Workspace packages resolve to their directory without node_modules: a
// fresh clone, CI before install, the gate's worktrees and exported trees.
// Before, they were external packages (ext:@acme/ui) and every import
// between the packages of a monorepo was invisible to the rules.
func TestTypeScriptWorkspacePackagesWithoutNodeModules(t *testing.T) {
	for name, manifest := range map[string]map[string]string{
		"npm workspaces":         {"package.json": `{"name": "root", "private": true, "workspaces": ["apps/*", "packages/*", "!packages/legacy"]}`},
		"yarn workspaces object": {"package.json": `{"name": "root", "private": true, "workspaces": {"packages": ["apps/*", "packages/*", "!packages/legacy"]}}`},
		"pnpm-workspace.yaml": {
			"package.json":        `{"name": "root", "private": true}`,
			"pnpm-workspace.yaml": "packages:\n  - 'apps/*'\n  - \"packages/*\"\n  - '!packages/legacy'\n",
		},
	} {
		t.Run(name, func(t *testing.T) {
			files := map[string]string{
				"packages/ui/package.json":        `{"name": "@acme/ui", "exports": {".": "./src/index.ts", "./button": "./src/button/index.ts", "./icons/*": "./src/icons/*.ts"}}`,
				"packages/ui/src/index.ts":        `import "@acme/ui/button";`, // self-reference
				"packages/ui/src/button/index.ts": "export {};",
				"packages/ui/src/icons/star.ts":   "export {};",
				"packages/util/package.json":      `{"name": "acme-util", "main": "lib/index.js"}`,
				"packages/util/lib/index.js":      "module.exports = {};",
				"packages/legacy/package.json":    `{"name": "acme-legacy", "main": "index.js"}`,
				"packages/legacy/index.js":        "",
				"tools/x/package.json":            `{"name": "acme-tools-x", "main": "index.js"}`,
				"tools/x/index.js":                "",
				"apps/web/package.json":           `{"name": "web", "dependencies": {"@acme/ui": "workspace:*"}}`,
				"apps/web/src/main.ts":            `import "@acme/ui";`,
			}
			maps.Copy(files, manifest)
			root := tree(t, files)
			runCases(t, newTS(t), root, []resolveCase{
				{"root export", "apps/web/src/main.ts", "@acme/ui", inRepo("packages/ui/src")},
				{"subpath export", "apps/web/src/main.ts", "@acme/ui/button", inRepo("packages/ui/src/button")},
				{"pattern export", "apps/web/src/main.ts", "@acme/ui/icons/star", inRepo("packages/ui/src/icons")},
				{"main", "apps/web/src/main.ts", "acme-util", inRepo("packages/util/lib")},
				{"self-reference", "packages/ui/src/index.ts", "@acme/ui/button", inRepo("packages/ui/src/button")},
				// excluded by "!" and not a member: not the repository's packages
				{"negated member", "apps/web/src/main.ts", "acme-legacy", ext("acme-legacy")},
				{"not a member", "apps/web/src/main.ts", "acme-tools-x", ext("acme-tools-x")},
				{"a subpath the package does not export", "apps/web/src/main.ts", "@acme/ui/secret", unresolved()},
			})
		})
	}
}

// An installed package of the same name wins, as it does for Node and the
// bundlers (the workspace is only the fallback).
func TestTypeScriptInstalledPackageWinsOverWorkspace(t *testing.T) {
	root := tree(t, map[string]string{
		"package.json":                       `{"name": "root", "private": true, "workspaces": ["packages/*"]}`,
		"packages/ui/package.json":           `{"name": "@acme/ui", "main": "src/index.ts"}`,
		"packages/ui/src/index.ts":           "export {};",
		"apps/web/src/main.ts":               `import "@acme/ui";`,
		"node_modules/@acme/ui/package.json": `{"name": "@acme/ui", "main": "index.js"}`,
		"node_modules/@acme/ui/index.js":     "",
	})
	runCases(t, newTS(t), root, []resolveCase{
		{"installed copy", "apps/web/src/main.ts", "@acme/ui", ext("@acme/ui")},
	})
}

func TestWorkspacePackages(t *testing.T) {
	root := tree(t, map[string]string{
		"package.json":                             `{"name": "root", "workspaces": ["packages/**", "apps/web"]}`,
		"packages/a/package.json":                  `{"name": "a"}`,
		"packages/group/b/package.json":            `{"name": "@x/b"}`,
		"packages/noname/package.json":             `{"version": "1.0.0"}`,
		"packages/bad/package.json":                `{not json`,
		"packages/a/node_modules/dep/package.json": `{"name": "dep"}`,
		"apps/web/package.json":                    `{"name": "web"}`,
		"apps/other/package.json":                  `{"name": "other"}`,
	})
	got := workspacePackages(root)
	var names []string
	for _, p := range got {
		names = append(names, p.Name+"="+p.Dir)
	}
	want := []string{"@x/b=packages/group/b", "a=packages/a", "web=apps/web"}
	if !slices.Equal(names, want) {
		t.Errorf("workspace packages = %v, want %v", names, want)
	}
	if got := workspacePackages(t.TempDir()); len(got) != 0 {
		t.Errorf("no manifest: %v", got)
	}
}

// The links live in a temporary directory that Close removes.
func TestTypeScriptWorkspaceLinksAreRemoved(t *testing.T) {
	root := tree(t, map[string]string{
		"package.json":             `{"name": "root", "workspaces": ["packages/*"]}`,
		"packages/ui/package.json": `{"name": "@acme/ui", "main": "src/index.ts"}`,
		"packages/ui/src/index.ts": "export {};",
		"apps/web/src/main.ts":     `import "@acme/ui";`,
	})
	ts := NewTypeScript()
	runCases(t, ts, root, []resolveCase{{"linked", "apps/web/src/main.ts", "@acme/ui", inRepo("packages/ui/src")}})
	r := ts.cache.Get(root, loadTS)
	if r.links == "" {
		t.Fatal("no link directory")
	}
	ts.Close()
	if _, err := os.Stat(filepath.Join(r.links)); !os.IsNotExist(err) {
		t.Errorf("link directory %s left behind: %v", r.links, err)
	}
}
