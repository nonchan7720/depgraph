package graph

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
)

func testStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	get := func(k, d string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return d
	}
	s, err := New(ctx, get("NEO4J_URI", "bolt://localhost:7687"), get("NEO4J_USER", "neo4j"),
		get("NEO4J_PASSWORD", "depgraph123"), get("NEO4J_DATABASE", "neo4j"))
	if err != nil {
		t.Skipf("neo4j not available: %v", err)
	}
	if err := s.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(context.Background()) })
	return s, ctx
}

// testRepo returns a repository bound to a fresh project key whose data is
// removed on cleanup (other projects' data is never touched).
func testRepo(t *testing.T, s *Store) (*Graph, string) {
	t.Helper()
	key := fmt.Sprintf("zz_test_%d", time.Now().UnixNano())
	t.Cleanup(func() { _ = ForProject(s, key).Clear(context.Background()) })
	return ForProject(s, key), key
}

func testFile(pkg, name string, syms ...string) *graph.FileGraph {
	fg := &graph.FileGraph{Path: pkg + "/" + name, Package: pkg, Language: graph.Go, Lines: 10}
	for i, n := range syms {
		fg.Symbols = append(fg.Symbols, graph.Symbol{Name: n, Kind: "function", Line: i*3 + 1, EndLine: i*3 + 2})
	}
	return fg
}

func countEdges(t *testing.T, s *Store, ctx context.Context, project string) map[string]int64 {
	t.Helper()
	rows, err := s.Query(ctx, `
		MATCH (a:Symbol {project: $p})-[r:CALLS|REFERENCES]->(b:Symbol)
		RETURN a.name + '>' + b.name + ':' + type(r) AS e, count(*) AS n`, map[string]any{"p": project})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for _, r := range rows {
		out[r["e"].(string)] = r["n"].(int64)
	}
	return out
}

func TestUpsertRefs(t *testing.T) {
	st, ctx := testStore(t)
	s, pkg := testRepo(t, st)
	a, b := testFile("p", "a.go", "A", "B"), testFile("p", "b.go", "C")
	for _, fg := range []*graph.FileGraph{a, b} {
		if err := s.UpsertFile(ctx, fg); err != nil {
			t.Fatal(err)
		}
	}
	ref := func(kind, ff, fn, tf, tn string) graph.Reference {
		return graph.Reference{Kind: kind, FromFile: ff, FromName: fn, ToFile: tf, ToName: tn}
	}

	// 1. 出る辺(A->B)・入る辺(C->A)・REFERENCES・存在しないシンボル宛(無視される)
	refs := []graph.Reference{
		ref("CALLS", a.Path, "A", a.Path, "B"),
		ref("CALLS", b.Path, "C", a.Path, "A"),
		ref("REFERENCES", b.Path, "C", a.Path, "B"),
		ref("CALLS", a.Path, "A", a.Path, "Nope"),
		ref("BOGUS", a.Path, "A", a.Path, "B"),
	}
	for i := 0; i < 2; i++ { // 冪等: 2 回呼んでも重複しない
		if err := s.UpsertRefs(ctx, a.Path, refs); err != nil {
			t.Fatal(err)
		}
	}
	got := countEdges(t, st, ctx, pkg)
	want := map[string]int64{"A>B:CALLS": 1, "C>A:CALLS": 1, "C>B:REFERENCES": 1}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("edges = %v, want %v", got, want)
	}
	stats, err := s.Stats(ctx)
	if err != nil || stats.Calls < 2 {
		t.Errorf("stats.Calls = %d err=%v", stats.Calls, err)
	}

	// 2. a.go の出る辺は置き換わる。入る辺(C->A)は MERGE のみなので、refs に無くても残る。
	if err := s.UpsertRefs(ctx, a.Path, []graph.Reference{ref("CALLS", a.Path, "B", a.Path, "A")}); err != nil {
		t.Fatal(err)
	}
	got = countEdges(t, st, ctx, pkg)
	want = map[string]int64{"B>A:CALLS": 1, "C>A:CALLS": 1, "C>B:REFERENCES": 1}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("after replace edges = %v, want %v", got, want)
	}

	// 3. a.go を再 upsert するとシンボルごと辺が消える（入る辺も）。b.go 側の辺は元々無い。
	if err := s.UpsertFile(ctx, a); err != nil {
		t.Fatal(err)
	}
	if got = countEdges(t, st, ctx, pkg); len(got) != 0 {
		t.Errorf("edges after UpsertFile = %v", got)
	}
}

// Two projects with identical paths and package keys must not see or touch
// each other's data.
func TestProjectIsolation(t *testing.T) {
	st, ctx := testStore(t)
	a, ka := testRepo(t, st)
	b, kb := testRepo(t, st)
	if ka == kb {
		kb += "b"
		b = ForProject(st, kb)
		t.Cleanup(func() { _ = b.Clear(context.Background()) })
	}
	cyc := func(pkg, other string) *graph.FileGraph {
		fg := testFile(pkg, "x.go", "Dup")
		fg.Imports = []graph.Import{{Spec: other, Target: other}, {Spec: "fmt", Target: "ext:fmt", External: true}}
		return fg
	}
	for _, r := range []*Graph{a, b} {
		for _, fg := range []*graph.FileGraph{cyc("x", "y"), cyc("y", "x")} {
			if err := r.UpsertFile(ctx, fg); err != nil {
				t.Fatal(err)
			}
		}
		if err := r.RebuildAllPackageImports(ctx); err != nil {
			t.Fatal(err)
		}
	}
	want := graph.Stats{Packages: 2, Files: 2, Symbols: 2, Imports: 4}
	for _, r := range []*Graph{a, b} {
		if st, err := r.Stats(ctx); err != nil || st != want {
			t.Fatalf("%s stats = %+v, %v; want %+v", r.project, st, err, want)
		}
	}
	ov, err := a.Overview(ctx)
	if err != nil || len(ov.Packages) != 3 || len(ov.Edges) != 4 {
		t.Fatalf("overview = %+v, %v", ov, err)
	}
	d, err := a.PackageDetail(ctx, "x")
	if err != nil || len(d.Files) != 1 || len(d.ImportedBy) != 1 || len(d.Imports) != 2 {
		t.Fatalf("detail = %+v, %v", d, err)
	}

	// changes in b leave a alone
	if err := b.DeleteFiles(ctx, []string{"y/x.go"}); err != nil {
		t.Fatal(err)
	}
	if err := b.DeleteFile(ctx, "x/x.go"); err != nil {
		t.Fatal(err)
	}
	for _, step := range []func(context.Context) error{b.RebuildAllPackageImports, b.PruneEmptyPackages, b.PruneOrphanExternals} {
		if err := step(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if paths, err := b.FilePaths(ctx); err != nil || len(paths) != 0 {
		t.Fatalf("b paths = %v, %v", paths, err)
	}
	if st, err := b.Stats(ctx); err != nil || st != (graph.Stats{}) {
		t.Fatalf("b stats after delete = %+v, %v", st, err)
	}
	if st, err := a.Stats(ctx); err != nil || st != want {
		t.Fatalf("a stats after b's delete = %+v, %v", st, err)
	}
	if err := b.UpsertFile(ctx, cyc("x", "y")); err != nil {
		t.Fatal(err)
	}
	if err := b.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if paths, err := a.FilePaths(ctx); err != nil || len(paths) != 2 {
		t.Fatalf("a paths after b.Clear = %v, %v", paths, err)
	}
	rows, err := st.Query(ctx, `MATCH (n {project: $p}) RETURN count(n) AS n`, map[string]any{"p": kb})
	if err != nil || rows[0]["n"].(int64) != 0 {
		t.Fatalf("b leftovers = %v, %v", rows, err)
	}
}

func TestSchema(t *testing.T) {
	st, ctx := testStore(t)
	rows, err := st.Query(ctx, `SHOW CONSTRAINTS YIELD name RETURN collect(name) AS names`, nil)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, n := range rows[0]["names"].([]any) {
		names[n.(string)] = true
	}
	for _, n := range []string{"package_project_key", "file_project_path", "project_key"} {
		if !names[n] {
			t.Errorf("constraint %s missing (have %v)", n, names)
		}
	}
	for _, n := range []string{"package_key", "file_path"} {
		if names[n] {
			t.Errorf("old single-property constraint %s must be dropped", n)
		}
	}
}
