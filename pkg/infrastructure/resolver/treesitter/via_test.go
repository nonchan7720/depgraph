package treesitter

import (
	"reflect"
	"sort"
	"testing"
)

// viaOf renders the calls of rel made through a member, as caller ->
// sorted "owner|member.method".
func viaOf(t *testing.T, root, rel string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, fc := range usage(t, New(), root, rel).Calls {
		for _, c := range fc.Calls {
			if c.Via != "" {
				out[fc.Name] = append(out[fc.Name], c.Via+"."+c.Name)
			}
		}
		sort.Strings(out[fc.Name])
	}
	return out
}

// Go: a method called on a field of the receiver or on a parameter is a
// call through that member; its type is in the members (another package's)
// or the own members (the package's own types, as interfaces usually are).
func TestCallsThroughMembersGo(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/app\ngo 1.22\n")
	writeFile(t, root, "b/b.go", "package b\ntype Client struct{}\nfunc (Client) Do() {}\n")
	writeFile(t, root, "a/a.go", `package a

import "example.com/app/b"

type Repo interface{ Find() }

type S struct {
	repo Repo
	cli  *b.Client
	n    int
	list []Repo
}

func (s *S) Run(r Repo, c b.Client) {
	s.repo.Find()
	s.cli.Do()
	r.Find()
	c.Do()
	local := s.repo
	local.Find()
}

func Free(r Repo, n int) { r.Find() }
`)
	u := usage(t, New(), root, "a/a.go")
	want := map[string][]string{
		"S.Run": {"S.Run|c.Do", "S.Run|r.Find", "S|cli.Do", "S|repo.Find"},
		"Free":  {"Free|r.Find"},
	}
	if got := viaOf(t, root, "a/a.go"); !reflect.DeepEqual(got, want) {
		t.Errorf("calls through members = %v, want %v", got, want)
	}
	var own []string
	for _, m := range u.OwnMembers {
		own = append(own, m.Owner+"|"+m.Member+":"+m.Type)
	}
	sort.Strings(own)
	if want := []string{"Free|r:Repo", "S.Run|r:Repo", "S|repo:Repo"}; !reflect.DeepEqual(own, want) {
		t.Errorf("own members = %v, want %v", own, want)
	}
}

// TypeScript: this.field.method() and param.method(); a constructor's
// parameters belong to the class.
func TestCallsThroughMembersTypeScript(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"name": "app", "private": true}`)
	writeFile(t, root, "src/repo.ts", "export interface Repo { find(): void }\n")
	writeFile(t, root, "src/a.ts", `import { Repo } from './repo';

export class Svc {
  constructor(private repo: Repo, other: Repo) {
    other.find();
  }
  run(r: Repo) {
    this.repo.find();
    r.find();
    this.run(r);
  }
}
export function free(r: Repo) { r.find(); }
`)
	want := map[string][]string{
		"Svc.constructor": {"Svc|other.find"},
		"Svc.run":         {"Svc.run|r.find", "Svc|repo.find"},
		"free":            {"free|r.find"},
	}
	if got := viaOf(t, root, "src/a.ts"); !reflect.DeepEqual(got, want) {
		t.Errorf("calls through members = %v, want %v", got, want)
	}
}

// Python: self.field.method() and param.method(); __init__'s parameters
// belong to the class.
func TestCallsThroughMembersPython(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "app/__init__.py", "")
	writeFile(t, root, "app/repo.py", "class Repo:\n    def find(self): ...\n")
	writeFile(t, root, "app/a.py", `from .repo import Repo


class Svc:
    def __init__(self, repo: Repo, other: Repo):
        self.repo = repo
        other.find()

    def run(self, r: Repo):
        self.repo.find()
        r.find()


def free(r: Repo):
    r.find()
`)
	want := map[string][]string{
		"Svc.__init__": {"Svc|other.find"},
		"Svc.run":      {"Svc.run|r.find", "Svc|repo.find"},
		"free":         {"free|r.find"},
	}
	if got := viaOf(t, root, "app/a.py"); !reflect.DeepEqual(got, want) {
		t.Errorf("calls through members = %v, want %v", got, want)
	}
}
