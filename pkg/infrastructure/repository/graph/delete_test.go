package graph

import (
	"testing"
)

// Clearing a project deletes its data in batches (one transaction for a
// large repository exceeds Neo4j's transaction memory) and leaves other
// projects alone.
func TestClearDeletesInBatches(t *testing.T) {
	st, ctx := testStore(t)
	a, ka := testRepo(t, st)
	b, kb := testRepo(t, st)
	if ka == kb {
		t.Skip("same key twice")
	}
	for _, r := range []*Graph{a, b} {
		for _, pkg := range []string{"p", "q", "r"} {
			if err := r.UpsertFile(ctx, testFile(pkg, "x.go", "A", "B", "C")); err != nil {
				t.Fatal(err)
			}
		}
	}
	count := func(key string) int64 {
		t.Helper()
		rows, err := st.Query(ctx, `MATCH (n {project: $key}) RETURN count(n) AS n`, map[string]any{"key": key})
		if err != nil {
			t.Fatal(err)
		}
		n, _ := rows[0]["n"].(int64)
		return n
	}
	before := count(kb)
	if count(ka) < 12 || before < 12 {
		t.Fatalf("seed: %d and %d nodes", count(ka), before)
	}
	old := deleteBatch
	deleteBatch = 4 // several batches
	t.Cleanup(func() { deleteBatch = old })
	if err := a.Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(ka); n != 0 {
		t.Errorf("%d nodes left after Clear", n)
	}
	if n := count(kb); n != before {
		t.Errorf("the other project has %d nodes, had %d", n, before)
	}
	if err := DeleteProjectData(ctx, st, kb); err != nil {
		t.Fatal(err)
	}
	if n := count(kb); n != 0 {
		t.Errorf("%d nodes left after DeleteProjectData", n)
	}
}
