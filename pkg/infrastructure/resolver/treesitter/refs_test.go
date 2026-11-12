package treesitter

import (
	"sort"
	"testing"
)

// refsOf renders what the functions of rel refer to besides calling it, as
// caller -> sorted "target#name".
func refsOf(t *testing.T, root, rel string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, fc := range usage(t, New(), root, rel).Calls {
		for _, c := range fc.Refs {
			out[fc.Name] = append(out[fc.Name], c.Target+"#"+c.Name)
		}
		sort.Strings(out[fc.Name])
	}
	return out
}

// Go: the names of other packages a function uses without calling them
// (types, values), and the names it uses unqualified (of its own package,
// when the package declares them): not its locals, parameters or builtins.
func TestRefsGo(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/app\ngo 1.22\n")
	writeFile(t, root, "b/b.go", "package b\ntype T struct{ N int }\nvar Default T\nfunc Do(T) {}\n")
	writeFile(t, root, "a/a.go", `package a

import "example.com/app/b"

type Local struct{}

const limit = 3

func Uses(t b.T, l Local) (out []b.T) {
	x := b.Default
	b.Do(x)
	if len(out) < limit {
		out = append(out, t)
	}
	return out
}

func OnlyCalls() { b.Do(b.T{}) }
`)
	wantCalls(t, refsOf(t, root, "a/a.go"), map[string][]string{
		"Uses":      {"#Local", "#limit", "b#Default", "b#T"},
		"OnlyCalls": {"b#T"},
	})
	wantCalls(t, callsOf(t, root, "a/a.go"), map[string][]string{"Uses": {"b#Do"}, "OnlyCalls": {"b#Do"}})
}

// TypeScript: imported names used as values or types, and the other
// top-level declarations of the file.
func TestRefsTypeScript(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"name": "app", "private": true}`)
	writeFile(t, root, "src/b.ts", "export const g = 1;\n")
	writeFile(t, root, "src/a.ts", `import { g, type T, make } from './b';
import * as ns from './b';

const LIMIT = 3;
interface Own { n: number }

export function uses(t: T, o: Own): number {
  const local = make(t);
  return g + LIMIT + ns.other + local;
}
export function onlyCalls() { return make(1); }
`)
	wantCalls(t, refsOf(t, root, "src/a.ts"), map[string][]string{
		"uses": {"#LIMIT", "#Own", "src/b.ts#T", "src/b.ts#g", "src/b.ts#other"},
	})
}

// Python: imported names used without calling them, and the module's own
// top-level names.
func TestRefsPython(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "app/__init__.py", "")
	writeFile(t, root, "app/b.py", "g = 1\n")
	writeFile(t, root, "app/a.py", `from .b import g, T, make
from . import b

LIMIT = 3


def uses(t: T):
    local = make(t)
    return g + LIMIT + b.other + local


def only_calls():
    return make(1)
`)
	wantCalls(t, refsOf(t, root, "app/a.py"), map[string][]string{
		"uses": {"#LIMIT", "app#b.other", "app/b.py#T", "app/b.py#g"},
	})
}
