package imports

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// tree writes files (repo-relative slash path -> content) under a new temp dir
// and returns its absolute path.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	writeFiles(t, root, files)
	return root
}

func writeFiles(t testing.TB, root string, files map[string]string) {
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

func symlink(t *testing.T, root, target, link string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(link))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.FromSlash(target), p); err != nil {
		t.Fatal(err)
	}
}

type resolveCase struct {
	name string
	from string
	spec string
	want Result // only Kind and Target are compared
}

// runCases checks every case with Resolve and, for a BatchResolver, all
// cases again in one ResolveBatch call (both paths must agree).
func runCases(t *testing.T, r Resolver, root string, cases []resolveCase) {
	t.Helper()
	check := func(t *testing.T, c resolveCase, got Result) {
		t.Helper()
		if got.Kind != c.want.Kind || got.Target != c.want.Target {
			t.Errorf("Resolve(%q, %q) = %s %q (reason %q), want %s %q",
				c.from, c.spec, got.Kind, got.Target, got.Reason, c.want.Kind, c.want.Target)
		}
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			check(t, c, r.Resolve(context.Background(), root, c.from, c.spec))
		})
	}
	br, ok := r.(BatchResolver)
	if !ok {
		return
	}
	reqs := make([]Request, len(cases))
	for i, c := range cases {
		reqs[i] = Request{FromFile: c.from, Spec: c.spec}
	}
	got := br.ResolveBatch(context.Background(), root, reqs)
	for i, c := range cases {
		t.Run("batch/"+c.name, func(t *testing.T) { check(t, c, got[i]) })
	}
}

func inRepo(key string) Result { return Result{Kind: InRepo, Target: key} }
func ext(name string) Result   { return Result{Kind: External, Target: "ext:" + name} }
func unresolved() Result       { return Result{Kind: Unresolved} }
