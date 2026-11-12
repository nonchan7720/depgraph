package treesitter

import (
	"reflect"
	"sort"
	"testing"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
)

// callsOf renders the calls of rel as caller -> sorted "target#name".
func callsOf(t *testing.T, root, rel string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, fc := range usage(t, New(), root, rel).Calls {
		for _, c := range fc.Calls {
			out[fc.Name] = append(out[fc.Name], c.Target+"#"+c.Name)
		}
		sort.Strings(out[fc.Name])
	}
	return out
}

func wantCalls(t *testing.T, got, want map[string][]string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("calls =\n %v, want\n %v", got, want)
	}
}

// Go: a function calls the functions of its package by name, those of
// other packages through the import, and a method the methods of its own
// receiver. A parameter or local variable of the same name is not a call of
// the function, and builtins and conversions are no calls of the repository.
func TestCallsGo(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/app\ngo 1.22\n")
	writeFile(t, root, "b/b.go", "package b\nfunc Do() {}\n")
	writeFile(t, root, "c/c.go", "package cc\nfunc Go() {}\n")
	writeFile(t, root, "a/a.go", `package a

import (
	"fmt"

	"example.com/app/b"
	alias "example.com/app/c"
)

func Run(xs []int) {
	helper()
	b.Do()
	alias.Go()
	fmt.Println(len(xs), string(rune(1)))
	go func() { inner() }()
}

func helper() {}
func inner()  {}

type T struct{ f func() }

func (t *T) A() {
	t.B()
	t.f()
	other()
}
func (t T) B() {}
func other()   {}

func Shadow(helper func()) { helper() }
func Local() {
	inner := func() {}
	inner()
}
func Generic[V any](v V) { helper() }
`)
	wantCalls(t, callsOf(t, root, "a/a.go"), map[string][]string{
		"Run":     {"#helper", "#inner", "b#Do", "c#Go"},
		"T.A":     {"#T.B", "#T.f", "#other"},
		"Generic": {"#helper"},
	})
}

// TypeScript: calls and `new` of the file's own declarations and of what
// its imports bind; a class is one caller.
func TestCallsTypeScript(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"name": "app", "private": true}`)
	for _, f := range []string{"b", "c", "d"} {
		writeFile(t, root, "src/"+f+".ts", "export const g = 1;\n")
	}
	writeFile(t, root, "src/a.ts", `import { g, h as hh } from './b';
import * as ns from './c';
import D from './d';
import { x } from 'external';

export function f() {
  g();
  hh();
  ns.k();
  local();
  new D();
  x();
  console.log(1);
}
function local() { f(); }
export const arrow = () => local();
export class K {
  m() { g(); this.m(); }
}
const value = g();
`)
	wantCalls(t, callsOf(t, root, "src/a.ts"), map[string][]string{
		"f":     {"#local", "src/b.ts#g", "src/b.ts#h", "src/c.ts#k", "src/d.ts#default"},
		"local": {"#f"},
		"arrow": {"#local"},
		"K":     {"src/b.ts#g"},
	})
}

// Python: calls of the module's own functions and classes, of imported
// names and of functions reached through an imported module.
func TestCallsPython(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"__init__", "b", "c", "d"} {
		writeFile(t, root, "app/"+f+".py", "x = 1\n")
	}
	writeFile(t, root, "app/a.py", `import os
from .b import g, h as hh
from . import c
import app.d


def f():
    g()
    hh()
    c.k()
    app.d.run()
    local()
    os.getcwd()
    print(K())


def local():
    f()


class K:
    def m(self):
        g()
        self.m()
`)
	wantCalls(t, callsOf(t, root, "app/a.py"), map[string][]string{
		"f":     {"#K", "#local", "app#c.k", "app/b.py#g", "app/b.py#h", "app/d.py#run"},
		"local": {"#f"},
		"K":     {"app/b.py#g"},
	})
}

func TestFuncCallsEncoding(t *testing.T) {
	fc := graph.FuncCalls{Name: "T.A", Calls: []graph.Call{{Name: "other"}, {Target: "pkg/b", Name: "Do"}}}
	got, ok := graph.DecodeFuncCalls(fc.Encode())
	if !ok || !reflect.DeepEqual(got, fc) {
		t.Errorf("round trip = %+v, %v", got, ok)
	}
	if _, ok := graph.DecodeFuncCalls("broken"); ok {
		t.Error("a string without a separator decoded")
	}
}
