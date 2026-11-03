package treesitter

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
)

// usage parses rel under root and returns its Usage (fails when missing).
func usage(t *testing.T, r *Resolver, root, rel string) *graph.Usage {
	t.Helper()
	fg, err := r.ResolveFile(context.Background(), root, rel)
	if err != nil {
		t.Fatal(err)
	}
	if fg.Usage == nil {
		t.Fatalf("%s: no usage", rel)
	}
	return fg.Usage
}

// useSet renders uses as sorted "target#name" ("target#name>as" for a
// re-export) strings.
func useSet(us []graph.Use) []string {
	out := make([]string, 0, len(us))
	seen := map[string]bool{}
	for _, u := range us {
		s := u.Target + "#" + u.Name
		if u.ReExport {
			s += ">" + u.As
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func wantUses(t *testing.T, got []graph.Use, want ...string) {
	t.Helper()
	have := map[string]bool{}
	for _, s := range useSet(got) {
		have[s] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("missing use %q in\n  %s", w, strings.Join(useSet(got), "\n  "))
		}
	}
}

func noUses(t *testing.T, got []graph.Use, unwanted ...string) {
	t.Helper()
	for _, s := range useSet(got) {
		for _, u := range unwanted {
			if s == u {
				t.Errorf("unexpected use %q", s)
			}
		}
	}
}

func TestUsageTypeScript(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"name": "app", "private": true}`)
	for _, f := range []string{"x", "y", "side", "z", "w", "dyn", "worker", "locales/en", "eq"} {
		writeFile(t, root, "src/"+f+".ts", "export const v = 1;\n")
	}
	writeFile(t, root, "src/req.js", "module.exports = {};\n")
	writeFile(t, root, "src/a.ts", `import D, { a, type B, c as cc } from './x';
import * as ns from './y';
import './side';
import type { T } from './x';
export * from './z';
export { e, f as g } from './w';
export { default as H } from './w';
export default function foo() { return ns.foo(); }
const v = ns;
let q: ns.Typ;
function loc() {}
export { loc as other };
export function helper() {}
export function main() { helper(); }
const m = import('./dyn');
const r = require('./req');
import(`+"`./locales/${l}.ts`"+`);
new URL('./worker.ts', import.meta.url);
import x = require('./eq');
`)
	r := New()
	t.Cleanup(r.Close)
	u := usage(t, r, root, "src/a.ts")
	wantUses(t, u.Uses,
		"src/x.ts#default", "src/x.ts#a", "src/x.ts#B", "src/x.ts#c", "src/x.ts#T",
		"src/y.ts#foo", "src/y.ts#Typ", "src/y.ts#*",
		"src/side.ts#",
		"src/z.ts#*>*", "src/w.ts#e>e", "src/w.ts#f>g", "src/w.ts#default>H",
		"src/dyn.ts#*", "src/req.js#*", "src/locales/#*", "src/worker.ts#", "src/eq.ts#*",
		"src/a.ts#helper", // used in its own file
	)
	// `export { loc as other }` exports loc, it does not use it
	noUses(t, u.Uses, "src/a.ts#main", "src/a.ts#foo", "src/a.ts#loc")
	exp := map[string]string{}
	for _, e := range u.Exports {
		exp[e.Name] = e.Local
	}
	if exp["default"] != "foo" || exp["other"] != "loc" {
		t.Errorf("exports = %+v", u.Exports)
	}
	if u.Scope != "." {
		t.Errorf("scope = %q", u.Scope)
	}
	if u.Entry != "" {
		t.Errorf("entry = %q, want none", u.Entry)
	}
}

// A local name re-exported (`import { a } from './x'; export { a }`) is a
// re-export of x's a.
// An import of a package the project does not declare is an alias the
// resolver does not know (a bundler alias, Hugo's assets/, Docusaurus'
// @site): it is kept as an unknown target ("?<spec>") instead of dropped.
func TestUsageUndeclaredPackage(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"name": "a", "dependencies": {"react": "19.0.0"}}`)
	writeFile(t, root, "src/a.ts", `import { scrollToActive } from 'js/helpers/index';
import Home from '@site/src/components/Home';
import { useState } from 'react';
import fs from 'node:fs';
import path from 'path';
import { gone } from './missing';
`)
	r := New()
	t.Cleanup(r.Close)
	u := usage(t, r, root, "src/a.ts")
	wantUses(t, u.Uses, "?js/helpers/index#scrollToActive", "?@site/src/components/Home#default", "?./missing#gone")
	for _, s := range useSet(u.Uses) {
		if strings.Contains(s, "react") || strings.Contains(s, "fs") || strings.Contains(s, "path") {
			t.Errorf("declared or builtin package kept as unknown: %s", s)
		}
	}
}

func TestUsageTypeScriptLocalReExport(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "x.ts", "export const a = 1; export const b = 2;\n")
	writeFile(t, root, "i.ts", "import { a, b as bb } from './x';\nexport { a, bb as c };\nexport default bb;\n")
	r := New()
	t.Cleanup(r.Close)
	u := usage(t, r, root, "i.ts")
	wantUses(t, u.Uses, "x.ts#a>a", "x.ts#b>c", "x.ts#b>default")
}

func TestUsageEntriesTypeScript(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{
  "name": "lib",
  "main": "./dist/index.js",
  "bin": {"tool": "./dist/cli.js"},
  "exports": {".": {"import": "./dist/index.mjs"}, "./feature": "./src/feature.ts"},
  "scripts": {"seed": "tsx scripts/seed.ts && node ./tasks/run.mjs"}
}`)
	writeFile(t, root, "tsconfig.json", `{"compilerOptions": {"outDir": "dist", "rootDir": "src"}}`)
	writeFile(t, root, "index.html", `<script type="module" src="/src/web.ts"></script>`)
	writeFile(t, root, "Dockerfile", "CMD [\"node\", \"dist/server.js\"]\n")
	for _, f := range []string{
		"src/index.ts", "src/cli.ts", "src/feature.ts", "src/web.ts", "src/server.ts", "src/lib.ts",
		"scripts/seed.ts", "tasks/run.mjs", "src/a.test.ts", "src/__tests__/b.ts", "vite.config.ts",
		"src/types.d.ts", "src/gen.ts", "test-d/types.ts", "src/x.test-d.ts",
	} {
		writeFile(t, root, f, "export const v = 1;\n")
	}
	writeFile(t, root, "src/gen.ts", "// Code generated by tool. DO NOT EDIT.\nexport const v = 1;\n")
	writeFile(t, root, "src/marked.ts", "// depgraph:entry loaded by a plugin host\nexport const v = 1;\n")
	r := New()
	t.Cleanup(r.Close)
	for _, f := range []string{
		"src/index.ts", "src/cli.ts", "src/feature.ts", "src/web.ts", "src/server.ts",
		"scripts/seed.ts", "tasks/run.mjs", "src/a.test.ts", "src/__tests__/b.ts", "vite.config.ts",
		"src/types.d.ts", "src/marked.ts", "test-d/types.ts", "src/x.test-d.ts",
	} {
		if u := usage(t, r, root, f); u.Entry == "" {
			t.Errorf("%s: not an entry", f)
		}
	}
	if u := usage(t, r, root, "src/lib.ts"); u.Entry != "" {
		t.Errorf("src/lib.ts: entry %q", u.Entry)
	}
	if u := usage(t, r, root, "src/gen.ts"); !u.Generated || u.Entry != "" {
		t.Errorf("src/gen.ts: generated=%v entry=%q", u.Generated, u.Entry)
	}
}

// Files of a framework whose component files depgraph does not parse
// (.vue, .svelte, ...) can be used by those files: the whole scope is an
// entry, so nothing in it is reported.
func TestUsageEntriesOpaqueFramework(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "web/package.json", `{"name": "web", "dependencies": {"vue": "3.5.0"}}`)
	writeFile(t, root, "web/src/util.ts", "export const v = 1;\n")
	writeFile(t, root, "other/package.json", `{"name": "other"}`)
	writeFile(t, root, "other/src/App.svelte", "<script>import { v } from './util'</script>\n")
	writeFile(t, root, "other/src/util.ts", "export const v = 1;\n")
	writeFile(t, root, "next/package.json", `{"name": "n", "dependencies": {"next": "15.0.0"}}`)
	writeFile(t, root, "next/app/blog/page.tsx", "export default function Page() { return null }\n")
	writeFile(t, root, "next/lib/x.ts", "export const v = 1;\n")
	// react-email renders every template of emails/
	writeFile(t, root, "mail/package.json", `{"name": "m", "dependencies": {"react-email": "6.0.0"}}`)
	writeFile(t, root, "mail/emails/welcome.tsx", "export default function Welcome() { return null }\n")
	// Docusaurus loads swizzled theme components; examples are run by hand
	writeFile(t, root, "site/package.json", `{"name": "s", "dependencies": {"@docusaurus/core": "3.0.0"}}`)
	writeFile(t, root, "site/src/theme/Root.js", "export default function Root() { return null }\n")
	writeFile(t, root, "examples/demo/package.json", `{"name": "demo"}`)
	writeFile(t, root, "examples/demo/utils.ts", "export const v = 1;\n")
	// a build output path whose layout differs from the sources
	writeFile(t, root, "lib2/package.json", `{"name": "lib2", "main": "./dist/prod/entry.js"}`)
	writeFile(t, root, "lib2/src/entry.ts", "export const v = 1;\n")
	r := New()
	t.Cleanup(r.Close)
	for _, f := range []string{"web/src/util.ts", "other/src/util.ts", "next/app/blog/page.tsx", "mail/emails/welcome.tsx",
		"site/src/theme/Root.js", "examples/demo/utils.ts", "lib2/src/entry.ts"} {
		u := usage(t, r, root, f)
		if u.Entry == "" {
			t.Errorf("%s: not an entry", f)
		}
	}
	if u := usage(t, r, root, "next/lib/x.ts"); u.Entry != "" || u.Scope != "next" {
		t.Errorf("next/lib/x.ts: entry=%q scope=%q", u.Entry, u.Scope)
	}
}

// MDX files are not parsed, but their imports are explicit: what they import
// is an entry, the rest of the package is still analysed.
func TestUsageEntriesMDXImports(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "docs/package.json", `{"name": "docs", "dependencies": {"@mdx-js/react": "3.0.0"}}`)
	writeFile(t, root, "docs/content/post.mdx", "import { Chart } from '../components/Chart'\n\n# Post\n\n<Chart />\n")
	writeFile(t, root, "docs/components/Chart.tsx", "export function Chart() { return null }\n")
	writeFile(t, root, "docs/lib/other.ts", "export const v = 1;\n")
	r := New()
	t.Cleanup(r.Close)
	if u := usage(t, r, root, "docs/components/Chart.tsx"); u.Entry == "" {
		t.Error("a component an MDX file imports is not an entry")
	}
	if u := usage(t, r, root, "docs/lib/other.ts"); u.Entry != "" {
		t.Errorf("docs/lib/other.ts: entry %q (the package is not opaque)", u.Entry)
	}
}

// Static assets are served as they are; Wagtail loads wagtail_hooks.py.
// shadcn/ui copies a component kit into the project (components.json says
// where): its files export every part of each component on purpose.
func TestUsageVendoredShadcn(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "web/package.json", `{"name": "web"}`)
	writeFile(t, root, "web/components.json", `{"aliases": {"components": "@/components", "ui": "@/components/ui"}}`)
	writeFile(t, root, "web/src/components/ui/dialog.tsx", "function DialogPortal() {}\nexport { DialogPortal }\n")
	writeFile(t, root, "web/src/components/nav.tsx", "export function Nav() {}\n")
	writeFile(t, root, "kit/package.json", `{"name": "kit"}`)
	writeFile(t, root, "kit/components.json", `{}`)
	writeFile(t, root, "kit/components/ui/button.tsx", "export function Button() {}\n")
	// before components.json: the kit is components/ui over Radix
	writeFile(t, root, "old/package.json", `{"name": "old", "dependencies": {"@radix-ui/react-dialog": "1.0.0"}}`)
	writeFile(t, root, "old/components/ui/sheet.tsx", "export const Sheet = 1\n")
	r := New()
	t.Cleanup(r.Close)
	for _, f := range []string{"web/src/components/ui/dialog.tsx", "kit/components/ui/button.tsx", "old/components/ui/sheet.tsx"} {
		if u := usage(t, r, root, f); !u.Generated {
			t.Errorf("%s: not marked generated (vendored)", f)
		}
	}
	if u := usage(t, r, root, "web/src/components/nav.tsx"); u.Generated {
		t.Error("nav.tsx is not part of the kit")
	}
}

func TestUsageEntriesStaticAndWagtail(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "manage.py", "")
	writeFile(t, root, "site/__init__.py", "")
	writeFile(t, root, "site/base/__init__.py", "")
	writeFile(t, root, "site/base/wagtail_hooks.py", "def register(): pass\n")
	writeFile(t, root, "site/static/js/main.js", "export function f() {}\n")
	writeFile(t, root, "web/public/sw.js", "self.addEventListener('fetch', () => {});\n")
	r := New()
	t.Cleanup(r.Close)
	for _, f := range []string{"site/base/wagtail_hooks.py", "site/static/js/main.js", "web/public/sw.js"} {
		if u := usage(t, r, root, f); u.Entry == "" {
			t.Errorf("%s: not an entry", f)
		}
	}
}

func TestUsagePython(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pyproject.toml", "[project]\nname = \"pkg\"\n\n[project.scripts]\napp = \"pkg.cli:main\"\n")
	for _, f := range []string{"pkg/__init__.py", "pkg/sub.py", "pkg/util.py", "pkg/a/__init__.py", "pkg/a/b.py", "pkg/plugins/p1.py", "pkg/lib.py"} {
		writeFile(t, root, f, "")
	}
	writeFile(t, root, "pkg/cli.py", `import pkg.a.b
import pkg.util as u
from . import sub
from .util import x, y as yy
from pkg.a import *
__all__ = ["main"]

pkg.a.b.f()
u.g
sub.h()

@click.command()
def main():
    helper()
    import pkg.lib

def helper():
    pass

def unused():
    pass

s = "pkg.util:z"
importlib.import_module(f"pkg.plugins.{n}")
`)
	writeFile(t, root, "pkg/run.py", "if __name__ == \"__main__\":\n    pass\n")
	writeFile(t, root, "hooks/post_gen_project.py", "print()\n") // run by cookiecutter / copier
	r := New()
	t.Cleanup(r.Close)
	u := usage(t, r, root, "pkg/cli.py")
	wantUses(t, u.Uses,
		"pkg/a/b.py#", "pkg/a/b.py#f", "pkg/util.py#g", "pkg#sub", "pkg#sub.h",
		"pkg/util.py#x", "pkg/util.py#y", "pkg/a#*", "pkg/util.py#z", "pkg/plugins/#*",
		"pkg/lib.py#", "pkg/cli.py#helper",
	)
	noUses(t, u.Uses, "pkg/cli.py#unused")
	keep := strings.Join(u.Keep, ",")
	if !strings.Contains(keep, "main") {
		t.Errorf("keep = %v", u.Keep)
	}
	if u.Entry == "" {
		t.Error("pkg/cli.py (a [project.scripts] target) is not an entry")
	}
	if u := usage(t, r, root, "pkg/run.py"); u.Entry == "" {
		t.Error("a file with a __main__ guard is not an entry")
	}
	if u := usage(t, r, root, "hooks/post_gen_project.py"); u.Entry == "" {
		t.Error("a project template hook is not an entry")
	}
	if u := usage(t, r, root, "pkg/lib.py"); u.Entry != "" {
		t.Errorf("pkg/lib.py: entry %q", u.Entry)
	}
}

func TestUsageGo(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/m\n")
	writeFile(t, root, "b/b.go", "package bee\n")
	writeFile(t, root, "c/c.go", "package c\n")
	writeFile(t, root, "d/d.go", "package d\n")
	writeFile(t, root, "a/a.go", `package a

import (
	f "fmt"

	"example.com/m/b"
	. "example.com/m/c"
	_ "example.com/m/d"
)

type T struct{ X int }

func (t *T) M() { bee.Fn(); f.Println(Other, bee.Typ{}) }

var _ bee.I = nil

func Exported() { Helper2() }

func Recursive() { Recursive() }
`)
	writeFile(t, root, "a/a_test.go", "package a\n")
	writeFile(t, root, "cmd/x/main.go", "package main\nfunc main() {}\n")
	writeFile(t, root, "a/gen.go", "// Code generated by tool. DO NOT EDIT.\n\npackage a\n")
	r := New()
	t.Cleanup(r.Close)
	u := usage(t, r, root, "a/a.go")
	wantUses(t, u.Uses, "b#Fn", "b#Typ", "b#I", "c#*", "d#", "b#", "a#Other", "a#Helper2")
	noUses(t, u.Uses, "a#T", "a#Recursive", "a#Exported", "a#X")
	if u.Scope != "." || u.Entry != "" {
		t.Errorf("scope=%q entry=%q", u.Scope, u.Entry)
	}
	if u := usage(t, r, root, "a/a_test.go"); u.Entry == "" {
		t.Error("test file is not an entry")
	}
	if u := usage(t, r, root, "cmd/x/main.go"); u.Entry == "" {
		t.Error("main package is not an entry")
	}
	if u := usage(t, r, root, "a/gen.go"); !u.Generated {
		t.Error("generated file not detected")
	}
}

// Entry points declared in manifests are cached per root: ConfigChanged on a
// manifest drops them.
func TestUsageEntriesConfigChanged(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"name": "a"}`)
	writeFile(t, root, "src/lib.ts", "export const v = 1;\n")
	r := New()
	t.Cleanup(r.Close)
	if u := usage(t, r, root, "src/lib.ts"); u.Entry != "" {
		t.Fatalf("entry %q", u.Entry)
	}
	writeFile(t, root, "package.json", `{"name": "a", "main": "src/lib.ts"}`)
	if !r.IsConfigFile("package.json") || !r.IsConfigFile("Dockerfile") || !r.IsConfigFile("web/index.html") {
		t.Error("IsConfigFile")
	}
	r.ConfigChanged(root, "package.json")
	if u := usage(t, r, root, "src/lib.ts"); u.Entry == "" {
		t.Error("entry not refreshed after ConfigChanged")
	}
}
