package imports

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
)

func TestMultiDispatch(t *testing.T) {
	root := tree(t, map[string]string{
		"go.mod":          "module example.com/app\n",
		"handler/h.go":    "package handler\n",
		"repo/r.go":       "package repo\n",
		"web/a.ts":        "",
		"web/b.ts":        "",
		"app/__init__.py": "",
		"app/m.py":        "",
		"app/n.py":        "",
		"README.md":       "",
	})
	m := New()
	t.Cleanup(m.Close)
	runCases(t, m, root, []resolveCase{
		{"go", "handler/h.go", "example.com/app/repo", inRepo("repo")},
		{"ts", "web/a.ts", "./b", inRepo("web")},
		{"py", "app/m.py", ".n", inRepo("app")},
		{"unknown language", "README.md", "x", unresolved()},
	})
	if got := m.ResolveLang(context.Background(), graph.JavaScript, root, "web/a.ts", "./b"); got.Kind != InRepo || got.Target != "web" {
		t.Errorf("ResolveLang = %+v", got)
	}
}

func TestIsConfigFile(t *testing.T) {
	for rel, want := range map[string]bool{
		"go.mod": true, "svc/go.mod": true, "go.work": true,
		"tsconfig.json": true, "apps/web/tsconfig.app.json": true, "jsconfig.json": true,
		"packages/ui/package.json": true,
		"pyproject.toml":           true, "setup.cfg": true, "backend/app/__init__.py": true,
		"main.go": false, "src/a.ts": false, "go.sum": false, "tsconfig.ts": false,
	} {
		if got := IsConfigFile(rel); got != want {
			t.Errorf("IsConfigFile(%q) = %v, want %v", rel, got, want)
		}
	}
}

func TestMultiInvalidateFor(t *testing.T) {
	root := tree(t, map[string]string{"go.mod": "module example.com/app\n", "main.go": "", "lib/l.go": ""})
	m := New()
	t.Cleanup(m.Close)
	runCases(t, m, root, []resolveCase{{"before", "main.go", "example.com/lib", ext("example.com/lib")}})
	writeFiles(t, root, map[string]string{"lib/go.mod": "module example.com/lib\n"})
	if m.InvalidateFor(root, "lib/l.go") {
		t.Fatal("a source file is not a config file")
	}
	if !m.InvalidateFor(root, "lib/go.mod") {
		t.Fatal("go.mod is a config file")
	}
	runCases(t, m, root, []resolveCase{{"after", "main.go", "example.com/lib", inRepo("lib")}})
}

func TestConcurrentResolve(t *testing.T) {
	root := tree(t, map[string]string{
		"tsconfig.json": `{"compilerOptions": {"paths": {"@/*": ["./src/*"]}}}`,
		"src/a.ts":      "",
		"src/lib/x.ts":  "",
		"go.mod":        "module example.com/app\n",
		"pkg/p.go":      "",
	})
	m := New()
	t.Cleanup(m.Close)
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if i%5 == 0 {
				m.Invalidate(root)
			}
			if got := m.Resolve(context.Background(), root, "src/a.ts", "@/lib/x"); got.Target != "src/lib" {
				t.Errorf("ts: %+v", got)
			}
			if got := m.Resolve(context.Background(), root, "main.go", "example.com/app/pkg"); got.Target != "pkg" {
				t.Errorf("go: %+v", got)
			}
		}()
	}
	wg.Wait()
}

// BenchmarkTypeScriptResolve measures one esbuild resolve (tsconfig paths)
// with a warm per-root context.
func BenchmarkTypeScriptResolve(b *testing.B) {
	files := map[string]string{
		"tsconfig.json":                     `{"compilerOptions": {"baseUrl": ".", "paths": {"@/*": ["./src/*"]}}}`,
		"node_modules/react/package.json":   `{"name": "react", "main": "index.js"}`,
		"node_modules/react/index.js":       "",
		"src/features/a/b/c/d/component.ts": "",
	}
	for i := range 200 {
		files[fmt.Sprintf("src/lib/m%d.ts", i)] = ""
	}
	root := b.TempDir()
	writeFiles(b, root, files)
	r := NewTypeScript()
	defer r.Close()
	ctx := context.Background()
	from := "src/features/a/b/c/d/component.ts"
	for _, spec := range []string{"@/lib/m42", "../../../../../lib/m7", "react"} {
		b.Run(spec, func(b *testing.B) {
			for b.Loop() {
				if res := r.Resolve(ctx, root, from, spec); res.Kind == Unresolved {
					b.Fatal(res.Reason)
				}
			}
		})
	}
}

// BenchmarkTypeScriptResolveBatch resolves 3 imports of each of 200 files in
// one batch.
func BenchmarkTypeScriptResolveBatch(b *testing.B) {
	files := map[string]string{
		"tsconfig.json":                   `{"compilerOptions": {"baseUrl": ".", "paths": {"@/*": ["./src/*"]}}}`,
		"node_modules/react/package.json": `{"name": "react", "main": "index.js"}`,
		"node_modules/react/index.js":     "",
	}
	var reqs []Request
	for i := range 200 {
		from := fmt.Sprintf("src/features/f%d/a/b/component.ts", i)
		files[from] = ""
		files[fmt.Sprintf("src/lib/m%d.ts", i)] = ""
		reqs = append(reqs,
			Request{FromFile: from, Spec: fmt.Sprintf("@/lib/m%d", i)},
			Request{FromFile: from, Spec: "../../../../lib/m7"},
			Request{FromFile: from, Spec: "react"})
	}
	root := b.TempDir()
	writeFiles(b, root, files)
	r := NewTypeScript()
	defer r.Close()
	for b.Loop() {
		for _, res := range r.ResolveBatch(context.Background(), root, reqs) {
			if res.Kind == Unresolved {
				b.Fatal(res.Reason)
			}
		}
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*len(reqs)), "ns/import")
}
