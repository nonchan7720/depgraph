package treesitter

import (
	"slices"
	"sort"
	"testing"
)

func TestGoDeclsAndIdents(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/app\n")
	writeFile(t, root, "p/a.go", `package p

import "fmt"

const limit = 3

var (
	cache map[string]int
	Hits  int
)

type Parser struct{ n int }

func (p *Parser) Parse(s string) error { return check(s) }

func New() *Parser { fmt.Println(limit); return &Parser{} }
`)
	u := resolve(t, root, "p/a.go").Usage
	wantList(t, "decls", u.Decls, []string{"Hits", "New", "Parser", "cache", "limit"})
	for _, id := range []string{"check", "limit", "Parser", "fmt"} {
		if !slices.Contains(u.Idents, id) {
			t.Errorf("idents lack %q: %v", id, u.Idents)
		}
	}
	if slices.Contains(u.Idents, "Println") || slices.Contains(u.Idents, "Parse") {
		t.Errorf("a selected field or method is not a package-level name: %v", u.Idents)
	}
}

func TestDeclRefsTypeScriptAndPython(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"name": "app"}`)
	writeFile(t, root, "src/util.ts", `const SEP = "/";
function split(s: string) { return s.split(SEP); }
export function join(xs: string[]) { return xs.join(SEP); }
export class Path { parts() { return split(""); } }
export const render = () => "x";
`)
	got := map[string][]string{}
	for _, d := range resolve(t, root, "src/util.ts").Usage.DeclRefs {
		sort.Strings(d.Refs)
		got[d.Name] = d.Refs
	}
	want := map[string][]string{"SEP": nil, "split": {"SEP"}, "join": {"SEP"}, "Path": {"split"}, "render": nil}
	for k, v := range want {
		if !slices.Equal(got[k], v) {
			t.Errorf("ts %s refs %v, want %v (all %v)", k, got[k], v, got)
		}
	}
	writeFile(t, root, "app/__init__.py", "")
	writeFile(t, root, "app/m.py", `import os
LIMIT = 3

def _check(x):
    return x < LIMIT

def load(p):
    return _check(os.path.getsize(p))

class Report:
    def render(self):
        return "r"
`)
	got = map[string][]string{}
	for _, d := range resolve(t, root, "app/m.py").Usage.DeclRefs {
		sort.Strings(d.Refs)
		got[d.Name] = d.Refs
	}
	want = map[string][]string{"LIMIT": nil, "_check": {"LIMIT"}, "load": {"_check"}, "Report": nil}
	for k, v := range want {
		if !slices.Equal(got[k], v) {
			t.Errorf("py %s refs %v, want %v (all %v)", k, got[k], v, got)
		}
	}
}
