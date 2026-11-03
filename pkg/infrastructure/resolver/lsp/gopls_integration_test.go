package lsp_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver/lsp"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver/treesitter"
)

// 実際の gopls に対する統合テスト。gopls が無ければ skip する。
func TestGoplsEnrich(t *testing.T) {
	if _, err := exec.LookPath("gopls"); err != nil {
		t.Skip("gopls not in PATH")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/app\n\ngo 1.22\n")
	write("a/a.go", "package a\n\nimport \"fmt\"\n\ntype T struct{}\n\nfunc A() {\n\tfmt.Println(\"x\")\n\tB()\n}\n\nfunc B() {}\n")
	write("c/c.go", "package c\n\nimport \"example.com/app/a\"\n\nfunc C() {\n\ta.A()\n\tvar _ a.T\n}\n")

	ts := treesitter.New()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	m := lsp.NewManager(root, func(ctx context.Context, rel string) ([]graph.Symbol, error) {
		fg, err := ts.ResolveFile(ctx, root, rel)
		if err != nil {
			return nil, err
		}
		return fg.Symbols, nil
	}, t.Logf)
	defer func() {
		sctx, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		m.Close(sctx)
	}()

	fg, err := ts.ResolveFile(ctx, root, "a/a.go")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Enrich(ctx, fg); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range fg.Refs {
		got = append(got, r.Kind+" "+r.FromFile+"#"+r.FromName+" -> "+r.ToFile+"#"+r.ToName)
	}
	sort.Strings(got)
	want := []string{
		"CALLS a/a.go#A -> a/a.go#B",
		"CALLS c/c.go#C -> a/a.go#A",
		"REFERENCES c/c.go#C -> a/a.go#T",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("refs:\n got %q\nwant %q", got, want)
	}
}
