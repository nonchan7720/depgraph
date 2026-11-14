package graph

import (
	"context"
	"os"
	"testing"
	"time"
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

// seedDense stores n Function nodes of project key with degree CALLS each.
func seedDense(t *testing.T, st *Store, ctx context.Context, key string, n, degree int) {
	t.Helper()
	const chunk = 2000
	for from := 0; from < n; from += chunk {
		if _, err := st.Write(ctx, `UNWIND range($from, $to - 1) AS i CREATE (:Function {project: $key, key: toString(i)})`,
			map[string]any{"key": key, "from": from, "to": min(from+chunk, n)}); err != nil {
			t.Fatal(err)
		}
	}
	for from := 0; from < n; from += chunk {
		if _, err := st.Write(ctx, `
			MATCH (f:Function {project: $key}) WITH f ORDER BY toInteger(f.key) WITH collect(f) AS fs
			UNWIND range($from, $to - 1) AS i UNWIND range(1, $degree) AS d
			WITH fs[i] AS a, fs[(i + d * 37) % size(fs)] AS b
			CREATE (a)-[:CALLS {via: false}]->(b)`,
			map[string]any{"key": key, "from": from, "to": min(from+chunk, n), "degree": degree}); err != nil {
			t.Fatal(err)
		}
	}
}

// boundedWriter fails the test when one write removes more relationships
// of the project than limit.
type boundedWriter struct {
	t    *testing.T
	st   *Store
	ctx  context.Context
	key  string
	left int64
	max  int64
}

func (w *boundedWriter) rels() int64 {
	w.t.Helper()
	rows, err := w.st.Query(w.ctx, `MATCH (n {project: $key}) RETURN sum(COUNT { (n)-[]->() }) AS n`, map[string]any{"key": w.key})
	if err != nil {
		w.t.Fatal(err)
	}
	n, _ := rows[0]["n"].(int64)
	return n
}

func (w *boundedWriter) Write(ctx context.Context, cypher string, params map[string]any) ([]map[string]any, error) {
	rows, err := w.st.Write(ctx, cypher, params)
	if err == nil {
		now := w.rels()
		w.max = max(w.max, w.left-now)
		w.left = now
	}
	return rows, err
}

// A node can have any number of relationships (a function many call, a
// package every file imports), so deleting a batch of nodes with their
// relationships is not a bounded transaction: a large linked graph went
// over Neo4j's transaction memory, and `--reset` / `projects rm` failed.
// The relationships go first, a batch at a time, then the nodes.
func TestDeleteBoundsRelationshipsPerTransaction(t *testing.T) {
	st, ctx := testStore(t)
	_, key := testRepo(t, st)
	seedDense(t, st, ctx, key, 60, 20) // 1,200 relationships
	old := deleteBatch
	deleteBatch = 50
	t.Cleanup(func() { deleteBatch = old })
	w := &boundedWriter{t: t, st: st, ctx: ctx, key: key}
	w.left = w.rels()
	if w.left != 1200 {
		t.Fatalf("seed: %d relationships", w.left)
	}
	if err := DeleteProjectData(ctx, w, key); err != nil {
		t.Fatal(err)
	}
	if w.max > 50 {
		t.Errorf("one transaction removed %d relationships, want at most %d", w.max, 50)
	}
	rows, err := st.Query(ctx, `MATCH (n {project: $key}) RETURN count(n) AS n`, map[string]any{"key": key})
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := rows[0]["n"].(int64); n != 0 {
		t.Errorf("%d nodes left", n)
	}
}

// DEPGRAPH_BIG_DELETE=1: a linked graph the size of a large repository
// (30,000 functions, 3 million calls) is deleted with the default batch.
func TestDeleteLargeLinkedGraph(t *testing.T) {
	if os.Getenv("DEPGRAPH_BIG_DELETE") == "" {
		t.Skip("set DEPGRAPH_BIG_DELETE=1 to run (minutes, and a Neo4j with the default memory settings)")
	}
	st, _ := testStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	t.Cleanup(cancel)
	_, key := testRepo(t, st)
	seedDense(t, st, ctx, key, 30000, 100)
	start := time.Now()
	if err := DeleteProjectData(ctx, st, key); err != nil {
		t.Fatal(err)
	}
	t.Logf("deleted in %s", time.Since(start))
}
