package treesitter

import (
	"testing"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
)

func wantLoads(t *testing.T, got []graph.Use, want ...string) {
	t.Helper()
	have := useSet(got)
	if len(have) != len(want) {
		t.Errorf("loads = %q, want %q", have, want)
		return
	}
	for i := range want {
		if have[i] != want[i] {
			t.Errorf("loads = %q, want %q", have, want)
			return
		}
	}
}

// A TypeScript file loads the modules whose values it imports: not a module
// it imports for types only (import type, an inline type specifier, a name
// used only in type positions, which the compiler erases), not a module it
// imports on demand (import()), not a path in a string.
func TestLoadsTypeScript(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"name": "app", "private": true}`)
	for _, f := range []string{"value", "side", "typeonly", "inline", "erased", "typeofonly", "lazy", "reexp", "typereexp",
		"ns", "nstype", "jsx", "exported", "unused", "str", "ext", "shorthand", "req"} {
		writeFile(t, root, "src/"+f+".ts", "export const v = 1;\nexport type T = number;\nexport default v;\n")
	}
	writeFile(t, root, "src/a.tsx", `import { v as value } from './value';
import './side';
import type { T as T1 } from './typeonly';
import { type T as T2 } from './inline';
import { T as T3 } from './erased';
import { v as onlyTypeof } from './typeofonly';
import * as ns from './ns';
import * as nst from './nstype';
import Comp from './jsx';
import { v as passed } from './exported';
import { v as never } from './unused';
import { v as short } from './shorthand';
import req = require('./req');
export { v as r } from './reexp';
export type { T as R } from './typereexp';
export { passed };
const lazy = () => import('./lazy');
const s = './str';
let a: T1, b: T2, c: T3, d: typeof onlyTypeof, e: nst.T;
export function f() { return [value, ns.v, <Comp />, { short }, req]; }
`)
	u := usage(t, New(), root, "src/a.tsx")
	wantLoads(t, u.Loads, "src/exported.ts#", "src/jsx.ts#", "src/ns.ts#", "src/reexp.ts#", "src/req.ts#",
		"src/shorthand.ts#", "src/side.ts#", "src/value.ts#")
}

// A value imported only to be the default export is loaded too.
func TestLoadsTypeScriptDefaultExport(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"name": "app", "private": true}`)
	writeFile(t, root, "src/x.ts", "export const v = 1;\n")
	writeFile(t, root, "src/a.ts", "import { v } from './x';\nexport default v;\n")
	wantLoads(t, usage(t, New(), root, "src/a.ts").Loads, "src/x.ts#")
}

// JavaScript has no types to erase: every import statement and require()
// loads its module.
func TestLoadsJavaScript(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"name": "app", "private": true}`)
	for _, f := range []string{"x", "y", "lazy"} {
		writeFile(t, root, "src/"+f+".js", "module.exports = {};\n")
	}
	writeFile(t, root, "src/a.js", `import { unused } from './x';
function g() { return require('./y'); }
const lazy = () => import('./lazy');
`)
	u := usage(t, New(), root, "src/a.js")
	wantLoads(t, u.Loads, "src/x.js#", "src/y.js#")
}

// A Python file loads what its import statements name, wherever they are
// (an import inside a function is how a cycle is hidden, not removed),
// except under `if TYPE_CHECKING:`.
func TestLoadsPython(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"__init__", "a", "b", "c", "d", "e"} {
		writeFile(t, root, "app/"+f+".py", "x = 1\n")
	}
	writeFile(t, root, "app/sub/__init__.py", "")
	writeFile(t, root, "app/main.py", `from typing import TYPE_CHECKING
import typing
import app.a
from .b import x
from . import c, helper
from .sub import *

if TYPE_CHECKING:
    from .d import x as dx
if typing.TYPE_CHECKING:
    from . import e
else:
    import os

def late():
    from . import sub
    return sub

name = "app.d"
`)
	u := usage(t, New(), root, "app/main.py")
	wantLoads(t, u.Loads, "app#c", "app#helper", "app#sub", "app/a.py#", "app/b.py#x", "app/sub#")
	for _, l := range u.Loads {
		if lazy := l.As == graph.LoadDeferred; lazy != (l.Name == "sub") {
			t.Errorf("load %s#%s: deferred = %v", l.Target, l.Name, lazy)
		}
	}
}

// Go and Ruby files have no loads: a Go package cannot import itself, and
// Ruby constants refer to each other by design.
func TestLoadsOtherLanguages(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/app\ngo 1.22\n")
	writeFile(t, root, "a/a.go", "package a\nimport \"example.com/app/b\"\nvar _ = b.X\n")
	writeFile(t, root, "b/b.go", "package b\nvar X = 1\n")
	writeFile(t, root, "lib/a.rb", "require_relative 'b'\n")
	writeFile(t, root, "lib/b.rb", "class B; end\n")
	for _, rel := range []string{"a/a.go", "lib/a.rb"} {
		if u := usage(t, New(), root, rel); len(u.Loads) != 0 {
			t.Errorf("%s: loads = %q", rel, useSet(u.Loads))
		}
	}
}
