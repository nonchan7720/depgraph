package imports

import (
	"context"
	"testing"
)

func newTS(t *testing.T) *TypeScript {
	t.Helper()
	r := NewTypeScript()
	t.Cleanup(r.Close)
	return r
}

func TestTypeScriptRegression(t *testing.T) {
	// cases the old hard-coded resolver already handled
	root := tree(t, map[string]string{
		"src/api/users.ts":  `import { query } from "../db";`,
		"src/api/b.ts":      "export const b = 1;",
		"src/db/index.ts":   "export function query() {}",
		"src/lib/util.tsx":  "export const u = 1;",
		"src/plain/a.js":    "module.exports = 1;",
		"tsconfig.json":     `{"compilerOptions": {"baseUrl": ".", "paths": {"@/*": ["./src/*"]}}}`,
		"src/comp/Btn.jsx":  "export default 1;",
		"src/comp/index.ts": "export * from './Btn';",
	})
	runCases(t, newTS(t), root, []resolveCase{
		{"relative file", "src/api/users.ts", "./b", inRepo("src/api")},
		{"relative dir index", "src/api/users.ts", "../db", inRepo("src/db")},
		{"relative tsx", "src/api/users.ts", "../lib/util", inRepo("src/lib")},
		{"js importer", "src/plain/a.js", "../db", inRepo("src/db")},
		{"@/ alias to src", "src/api/users.ts", "@/lib/util", inRepo("src/lib")},
		{"@/ alias dir", "src/api/users.ts", "@/comp", inRepo("src/comp")},
	})
}

func TestTypeScriptTsconfigPaths(t *testing.T) {
	// "@/*" points at app/, not src/ (the old resolver assumed src/)
	root := tree(t, map[string]string{
		"tsconfig.json":      `{"compilerOptions": {"paths": {"@/*": ["./app/*"], "~lib/*": ["./shared/lib/*"]}}}`,
		"app/main.ts":        `import "@/core/x";`,
		"app/core/x.ts":      "export {};",
		"src/core/x.ts":      "export {};", // decoy: must not be chosen
		"shared/lib/str.ts":  "export {};",
		"app/feature/f.ts":   "export {};",
		"app/feature/g.ts":   "export {};",
		"node_modules/.keep": "",
	})
	runCases(t, newTS(t), root, []resolveCase{
		{"@/ to app", "app/main.ts", "@/core/x", inRepo("app/core")},
		{"custom alias", "app/main.ts", "~lib/str", inRepo("shared/lib")},
		{"missing aliased file is unresolved", "app/main.ts", "@/nope", unresolved()},
	})
}

func TestTypeScriptTsconfigExtends(t *testing.T) {
	root := tree(t, map[string]string{
		"tsconfig.base.json":       `{"compilerOptions": {"baseUrl": ".", "paths": {"#app/*": ["packages/app/src/*"]}}}`,
		"tsconfig.json":            `{"extends": "./tsconfig.base.json"}`,
		"packages/app/src/a/x.ts":  "export {};",
		"packages/app/src/main.ts": "export {};",
	})
	runCases(t, newTS(t), root, []resolveCase{
		{"paths from extended config", "packages/app/src/main.ts", "#app/a/x", inRepo("packages/app/src/a")},
	})
}

func TestTypeScriptBaseURL(t *testing.T) {
	root := tree(t, map[string]string{
		"tsconfig.json":       `{"compilerOptions": {"baseUrl": "src"}}`,
		"src/utils/str.ts":    "export {};",
		"src/pages/home.tsx":  `import "utils/str";`,
		"src/models/index.ts": "export {};",
	})
	runCases(t, newTS(t), root, []resolveCase{
		{"baseUrl file", "src/pages/home.tsx", "utils/str", inRepo("src/utils")},
		{"baseUrl dir", "src/pages/home.tsx", "models", inRepo("src/models")},
	})
}

func TestTypeScriptNearestTsconfig(t *testing.T) {
	// each app has its own tsconfig; esbuild must pick the nearest one above the importer
	root := tree(t, map[string]string{
		"apps/web/tsconfig.json":   `{"compilerOptions": {"paths": {"@/*": ["./src/*"]}}}`,
		"apps/web/src/main.ts":     "export {};",
		"apps/web/src/ui/b.ts":     "export {};",
		"apps/admin/tsconfig.json": `{"compilerOptions": {"paths": {"@/*": ["./lib/*"]}}}`,
		"apps/admin/lib/ui/b.ts":   "export {};",
		"apps/admin/lib/main.ts":   "export {};",
	})
	runCases(t, newTS(t), root, []resolveCase{
		{"web", "apps/web/src/main.ts", "@/ui/b", inRepo("apps/web/src/ui")},
		{"admin", "apps/admin/lib/main.ts", "@/ui/b", inRepo("apps/admin/lib/ui")},
	})
}

func TestTypeScriptESMJsExtension(t *testing.T) {
	root := tree(t, map[string]string{
		"src/a.ts":        `import { foo } from "./lib/foo.js";`,
		"src/lib/foo.ts":  "export const foo = 1;",
		"src/b.mts":       `import "./lib/bar.mjs";`,
		"src/lib/bar.mts": "export {};",
	})
	runCases(t, newTS(t), root, []resolveCase{
		{".js -> .ts", "src/a.ts", "./lib/foo.js", inRepo("src/lib")},
		{".mjs -> .mts", "src/b.mts", "./lib/bar.mjs", inRepo("src/lib")},
	})
}

func TestTypeScriptWorkspacePackages(t *testing.T) {
	root := tree(t, map[string]string{
		"package.json":                    `{"name": "root", "private": true, "workspaces": ["apps/*", "packages/*"]}`,
		"packages/ui/package.json":        `{"name": "@acme/ui", "exports": {".": "./src/index.ts", "./button": "./src/button/index.ts"}}`,
		"packages/ui/src/index.ts":        "export {};",
		"packages/ui/src/button/index.ts": "export {};",
		"packages/util/package.json":      `{"name": "acme-util", "main": "lib/index.js"}`,
		"packages/util/lib/index.js":      "module.exports = {};",
		"apps/web/package.json":           `{"name": "web", "dependencies": {"@acme/ui": "workspace:*"}}`,
		"apps/web/src/main.ts":            `import "@acme/ui";`,
	})
	// npm/yarn/pnpm workspaces link packages into node_modules
	symlink(t, root, "../../packages/ui", "node_modules/@acme/ui")
	symlink(t, root, "../packages/util", "node_modules/acme-util")
	runCases(t, newTS(t), root, []resolveCase{
		{"workspace root export", "apps/web/src/main.ts", "@acme/ui", inRepo("packages/ui/src")},
		{"workspace subpath export", "apps/web/src/main.ts", "@acme/ui/button", inRepo("packages/ui/src/button")},
		{"workspace main", "apps/web/src/main.ts", "acme-util", inRepo("packages/util/lib")},
	})
}

func TestTypeScriptPackageImports(t *testing.T) {
	root := tree(t, map[string]string{
		"package.json":        `{"name": "app", "type": "module", "imports": {"#internal/*": "./src/internal/*.ts", "#config": "./src/config/index.ts"}}`,
		"src/main.ts":         `import "#internal/db";`,
		"src/internal/db.ts":  "export {};",
		"src/config/index.ts": "export {};",
	})
	runCases(t, newTS(t), root, []resolveCase{
		{"#internal/*", "src/main.ts", "#internal/db", inRepo("src/internal")},
		{"#config", "src/main.ts", "#config", inRepo("src/config")},
		{"unknown # import is unresolved", "src/main.ts", "#nope", unresolved()},
	})
}

func TestTypeScriptExternal(t *testing.T) {
	root := tree(t, map[string]string{
		"src/main.ts":                          `import React from "react";`,
		"node_modules/react/package.json":      `{"name": "react", "main": "index.js"}`,
		"node_modules/react/index.js":          "module.exports = {};",
		"node_modules/lodash/package.json":     `{"name": "lodash", "main": "lodash.js"}`,
		"node_modules/lodash/lodash.js":        "",
		"node_modules/lodash/fp.js":            "",
		"node_modules/@scope/pkg/package.json": `{"name": "@scope/pkg", "exports": {".": "./i.js", "./sub": "./sub.js"}}`,
		"node_modules/@scope/pkg/i.js":         "",
		"node_modules/@scope/pkg/sub.js":       "",
	})
	runCases(t, newTS(t), root, []resolveCase{
		{"installed", "src/main.ts", "react", ext("react")},
		{"subpath", "src/main.ts", "lodash/fp", ext("lodash")},
		{"scoped subpath", "src/main.ts", "@scope/pkg/sub", ext("@scope/pkg")},
		{"not installed", "src/main.ts", "left-pad", ext("left-pad")},
		{"not installed scoped subpath", "src/main.ts", "@types/node/fs", ext("@types/node")},
		{"node builtin", "src/main.ts", "node:fs", ext("node:fs")},
		{"bare builtin", "src/main.ts", "path", ext("path")},
		{"missing relative is unresolved, not external", "src/main.ts", "./missing", unresolved()},
		{"missing parent relative", "src/main.ts", "../nope/x", unresolved()},
	})
}

func TestTypeScriptInvalidate(t *testing.T) {
	root := tree(t, map[string]string{
		"tsconfig.json": `{"compilerOptions": {"paths": {"@/*": ["./a/*"]}}}`,
		"a/x.ts":        "export {};",
		"b/x.ts":        "export {};",
		"main.ts":       "export {};",
	})
	r := newTS(t)
	runCases(t, r, root, []resolveCase{{"before", "main.ts", "@/x", inRepo("a")}})
	writeFiles(t, root, map[string]string{"tsconfig.json": `{"compilerOptions": {"paths": {"@/*": ["./b/*"]}}}`})
	r.Invalidate(root)
	runCases(t, r, root, []resolveCase{{"after", "main.ts", "@/x", inRepo("b")}})
}

func TestTypeScriptCanceledContext(t *testing.T) {
	root := tree(t, map[string]string{"a.ts": "", "b.ts": ""})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := newTS(t)
	if got := r.Resolve(ctx, root, "a.ts", "./b"); got.Kind != Unresolved {
		t.Errorf("canceled ctx: got %s %q", got.Kind, got.Target)
	}
	if got := r.ResolveBatch(ctx, root, []Request{{FromFile: "a.ts", Spec: "./b"}}); got[0].Kind != Unresolved {
		t.Errorf("canceled batch: got %s %q", got[0].Kind, got[0].Target)
	}
}

func TestTypeScriptBatchEdgeCases(t *testing.T) {
	root := tree(t, map[string]string{
		"src/a.ts":      "",
		"src/b.ts":      "",
		`src/q"x<y>.ts`: "",
	})
	got := newTS(t).ResolveBatch(context.Background(), root, []Request{
		{FromFile: "src/a.ts", Spec: "./b"},
		{FromFile: "src/gone.ts", Spec: "./b"}, // importer deleted meanwhile
		{FromFile: "src/a.ts", Spec: `./q"x<y>`},
		{FromFile: "src/a.ts", Spec: "./nope"},
		{FromFile: "src/a.ts", Spec: "./b"}, // duplicate
	})
	want := []Result{inRepo("src"), unresolved(), inRepo("src"), unresolved(), inRepo("src")}
	for i := range want {
		if got[i].Kind != want[i].Kind || got[i].Target != want[i].Target {
			t.Errorf("[%d] = %s %q (%s), want %s %q", i, got[i].Kind, got[i].Target, got[i].Reason, want[i].Kind, want[i].Target)
		}
	}
}
