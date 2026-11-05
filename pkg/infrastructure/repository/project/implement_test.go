package project

import (
	"context"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/project"
	graphrepo "github.com/nonchan7720/depgraph/pkg/infrastructure/repository/graph"
)

func get(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func TestRepository(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	st, err := graphrepo.New(ctx, get("NEO4J_URI", "bolt://localhost:7687"), get("NEO4J_USER", "neo4j"),
		get("NEO4J_PASSWORD", "depgraph123"), get("NEO4J_DATABASE", "neo4j"))
	if err != nil {
		t.Skipf("neo4j not available: %v", err)
	}
	// t.Cleanup runs LIFO: the store is closed after the data cleanups below.
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	if err := st.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	repo := New(st)
	now := time.Now().UnixNano()
	ka, kb := fmt.Sprintf("zz_test_%d_a", now), fmt.Sprintf("zz_test_%d_b@wt", now)
	t.Cleanup(func() {
		_ = repo.Delete(context.Background(), ka)
		_ = repo.Delete(context.Background(), kb)
	})

	if p, err := repo.Get(ctx, ka); err != nil || p != nil {
		t.Fatalf("Get missing = %+v, %v", p, err)
	}
	scan := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	a := project.Project{Key: ka, Name: "a", Root: "/r/a", Origin: "github.com/o/a", Base: ka, LastScanAt: scan}
	b := project.Project{Key: kb, Name: "b", Root: "/r/b", Base: "zz_b", Worktree: "wt", Commit: "c0ffee", BaseCommit: "ba5e",
		Dirty: []project.DirtyFile{{Path: "a.go", Hash: "0a1b"}, {Path: "dir with space/c.go", Hash: "-"}}}
	for _, p := range []project.Project{a, b} {
		if err := repo.Upsert(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	got, err := repo.Get(ctx, ka)
	if err != nil || got == nil || got.Commit != "" || got.Dirty != nil || got.Root != a.Root || got.Origin != a.Origin || got.Base != ka || !got.LastScanAt.Equal(scan) {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	gb, err := repo.Get(ctx, kb)
	if err != nil || gb == nil || gb.Worktree != "wt" || gb.Base != "zz_b" || !gb.LastScanAt.IsZero() ||
		gb.Commit != "c0ffee" || gb.BaseCommit != "ba5e" || !slices.Equal(gb.Dirty, b.Dirty) {
		t.Fatalf("Get b = %+v, %v", gb, err)
	}
	// update in place (moved root)
	a.Root = "/r/a2"
	if err := repo.Upsert(ctx, a); err != nil {
		t.Fatal(err)
	}
	if got, _ = repo.Get(ctx, ka); got.Root != "/r/a2" {
		t.Errorf("Root not updated: %+v", got)
	}

	// the sync lease: one holder at a time, taken over once expired; a
	// claim on a missing key creates the node with the project's identity
	kc := fmt.Sprintf("zz_test_%d_c@wt", now)
	t.Cleanup(func() { _ = repo.Delete(context.Background(), kc) })
	c := project.Project{Key: kc, Name: "c", Root: "/r/c", Base: "zz_c", Worktree: "wt"}
	t0 := time.Now()
	if ok, err := repo.ClaimSync(ctx, c, t0, t0.Add(time.Minute)); err != nil || !ok {
		t.Fatalf("first claim = %v, %v", ok, err)
	}
	if gc, _ := repo.Get(ctx, kc); gc == nil || gc.Root != "/r/c" || gc.Worktree != "wt" || !gc.LastScanAt.IsZero() {
		t.Fatalf("claimed node = %+v", gc)
	}
	if ok, _ := repo.ClaimSync(ctx, c, t0.Add(time.Second), t0.Add(2*time.Minute)); ok {
		t.Error("second claim while held")
	}
	if held, _ := repo.SyncHeld(ctx, kc, t0.Add(time.Second)); !held {
		t.Error("not held")
	}
	if ok, _ := repo.ClaimSync(ctx, c, t0.Add(2*time.Minute), t0.Add(3*time.Minute)); !ok {
		t.Error("expired lease not taken over")
	}
	if err := repo.ReleaseSync(ctx, kc); err != nil {
		t.Fatal(err)
	}
	if held, _ := repo.SyncHeld(ctx, kc, t0); held {
		t.Error("held after release")
	}

	// data of a; List counts its files
	ga := graphrepo.ForProject(st, ka)
	gbr := graphrepo.ForProject(st, kb)
	for _, r := range []*graphrepo.Graph{ga, gbr} {
		if err := r.UpsertFile(ctx, &graph.FileGraph{Path: "x/a.go", Package: "x", Language: graph.Go,
			Symbols: []graph.Symbol{{Name: "A", Kind: "function"}},
			Imports: []graph.Import{{Spec: "fmt", Target: "ext:fmt", External: true}}}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := repo.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, p := range list {
		if p.Key == ka || p.Key == kb {
			found++
			if p.Files != 1 {
				t.Errorf("%s files = %d", p.Key, p.Files)
			}
		}
	}
	if found != 2 {
		t.Fatalf("List lacks test projects: %+v", list)
	}

	// Delete removes the project and its data only
	if err := repo.Delete(ctx, ka); err != nil {
		t.Fatal(err)
	}
	if p, _ := repo.Get(ctx, ka); p != nil {
		t.Errorf("project still there: %+v", p)
	}
	rows, err := st.Query(ctx, `MATCH (n {project: $p}) RETURN count(n) AS n`, map[string]any{"p": ka})
	if err != nil || rows[0]["n"].(int64) != 0 {
		t.Errorf("data left after Delete: %v, %v", rows, err)
	}
	if s, err := gbr.Stats(ctx); err != nil || s.Files != 1 || s.Symbols != 1 {
		t.Errorf("other project's data touched: %+v, %v", s, err)
	}
}
