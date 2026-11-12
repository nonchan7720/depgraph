package treesitter

import (
	"reflect"
	"sort"
	"testing"
)

// forwardersOf returns the callers of rel whose whole body passes their
// parameters on, as "caller>target#name", sorted.
func forwardersOf(t *testing.T, root, rel string) []string {
	t.Helper()
	var out []string
	for _, fc := range usage(t, New(), root, rel).Calls {
		if fc.Forwards {
			if len(fc.Calls) != 1 {
				t.Errorf("%s forwards but has %d calls", fc.Name, len(fc.Calls))
				continue
			}
			out = append(out, fc.Name+">"+fc.Calls[0].Target+"#"+fc.Calls[0].Name)
		}
	}
	sort.Strings(out)
	return out
}

func wantForwarders(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("forwarders =\n %q, want\n %q", got, want)
	}
}

// Go: a function whose only statement calls another function with exactly
// its own parameters, in order. A method is never one: it may exist to
// satisfy an interface.
func TestForwardersGo(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/app\ngo 1.22\n")
	writeFile(t, root, "b/b.go", "package b\nfunc Do(x int, ys ...string) error { return nil }\n")
	writeFile(t, root, "a/a.go", `package a

import "example.com/app/b"

// A forwards.
func A(x int, ys ...string) error { return B(x, ys...) }

func B(x int, ys ...string) error {
	// a comment is not a statement
	return b.Do(x, ys...)
}
func Reordered(x, y int) int { return E(y, x) }
func E(x, y int) int        { return x + y }
func Adds(x int) int        { return E(x, 1) }
func Drops(x, y int) int    { return Neg(x) }
func Neg(x int) int         { return -x }
func Two(x int) {
	E(x, x)
	Stmt(x)
}
func Stmt(x int)   { I(x) }
func I(x int)      {}
func NoArgs() int  { return K() }
func K() int       { return 1 }
func Unnamed(int)  { K() }
func Wraps(x int) int { return Neg(Neg(x)) }

type T struct{}

func (t T) M(x int) int { return t.N(x) }
func (t T) N(x int) int { return x }
`)
	wantForwarders(t, forwardersOf(t, root, "a/a.go"), "A>#B", "B>b#Do", "NoArgs>#K", "Stmt>#I")
}

// TypeScript: function declarations and arrow / function expressions bound
// to a constant; `await` in front of the call is still passing on.
func TestForwardersTypeScript(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"name": "app", "private": true}`)
	writeFile(t, root, "src/c.ts", "export function k(x: number, y?: string) { return x; }\n")
	writeFile(t, root, "src/a.ts", `import { k } from './c';

export function a(x: number, y?: string) {
  return b(x, y);
}
export const b = (x: number, y = 'd') => k(x, y);
export const viaAwait = async (...xs: number[]) => { return await rest(...xs); };
function rest(...xs: number[]) { return xs; }
export const bare = x => rest(x);
export function changes(x: number) { return k(x + 1); }
export function picks(o: { x: number }) { return k(o.x); }
export function two(x: number) { k(x); return rest(x); }
export class K { m(x: number) { return k(x); } }
`)
	wantForwarders(t, forwardersOf(t, root, "src/a.ts"), "a>#b", "b>src/c.ts#k", "bare>#rest", "viaAwait>#rest")
}

// Python: a docstring does not count as a statement; *args / **kwargs and
// keyword arguments named like the parameters are passed on.
func TestForwardersPython(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "app/__init__.py", "")
	writeFile(t, root, "app/c.py", "def k(x, y=None):\n    return x\n")
	writeFile(t, root, "app/a.py", `from . import c


def a(x, y=None):
    """Pass it on."""
    return b(x, y)


def b(x: int, y: str = "d"):
    return c.k(x, y)


def splat(*args, **kwargs):
    return b(*args, **kwargs)


def keywords(x, y):
    return b(x=x, y=y)


def renames(x, y):
    return b(x=y, y=x)


def changes(x):
    return b(x + 1)


def two(x):
    b(x)
    return b(x)
`)
	wantForwarders(t, forwardersOf(t, root, "app/a.py"), "a>#b", "b>app#c.k", "keywords>#b", "splat>#b")
}
