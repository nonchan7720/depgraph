package hooksession

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
	graphrepo "github.com/nonchan7720/depgraph/pkg/infrastructure/repository/graph"
	projectrepo "github.com/nonchan7720/depgraph/pkg/infrastructure/repository/project"
)

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func store(t *testing.T) (*graphrepo.Store, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	st, err := graphrepo.New(ctx, env("NEO4J_URI", "bolt://localhost:7687"), env("NEO4J_USER", "neo4j"),
		env("NEO4J_PASSWORD", "depgraph123"), env("NEO4J_DATABASE", "neo4j"))
	if err != nil {
		t.Skipf("neo4j not available: %v", err)
	}
	if err := st.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	return st, ctx
}

func key(t *testing.T, suffix string) string {
	t.Helper()
	k := fmt.Sprintf("zz_test_%d_%s", time.Now().UnixNano(), suffix)
	return k
}

func TestPutGet(t *testing.T) {
	st, ctx := store(t)
	k, other := key(t, "a"), key(t, "b")
	t.Cleanup(func() {
		_ = projectrepo.New(st).Delete(context.Background(), k)
		_ = projectrepo.New(st).Delete(context.Background(), other)
	})
	s := New(st, k)
	if _, ok, err := s.Get(ctx, "s1"); err != nil || ok {
		t.Fatalf("unknown session: ok=%v err=%v", ok, err)
	}
	now := time.Now()
	in := []rules.Finding{{Rule: "god-package", Severity: "warn", Message: "x", ID: "x", Data: map[string]any{"big": 1}}}
	if err := s.Put(ctx, "s1", in, now); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Get(ctx, "s1")
	if err != nil || !ok || len(got) != 1 || got[0].Key() != in[0].Key() || got[0].Data != nil {
		t.Fatalf("Get = %+v ok=%v err=%v (Data is not kept)", got, ok, err)
	}
	// replaced, not appended
	if err := s.Put(ctx, "s1", nil, now); err != nil {
		t.Fatal(err)
	}
	if got, ok, _ := s.Get(ctx, "s1"); !ok || len(got) != 0 {
		t.Fatalf("after replace: %+v ok=%v", got, ok)
	}
	// another project's session with the same id is separate
	if _, ok, _ := New(st, other).Get(ctx, "s1"); ok {
		t.Error("sessions are per project")
	}
}

// Sessions do not pile up: older than MaxAge or beyond MaxSessions per
// project are removed on Put.
func TestPrune(t *testing.T) {
	st, ctx := store(t)
	k := key(t, "prune")
	t.Cleanup(func() { _ = projectrepo.New(st).Delete(context.Background(), k) })
	s := New(st, k)
	now := time.Now()
	if err := s.Put(ctx, "old", nil, now.Add(-MaxAge-time.Hour)); err != nil {
		t.Fatal(err)
	}
	for i := range MaxSessions + 2 {
		if err := s.Put(ctx, fmt.Sprintf("s%02d", i), nil, now.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := st.Query(ctx, `MATCH (h:HookSession {project: $p}) RETURN h.id AS id ORDER BY id`, map[string]any{"p": k})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != MaxSessions {
		t.Fatalf("%d sessions kept, want %d", len(rows), MaxSessions)
	}
	if rows[0]["id"] != "s02" {
		t.Errorf("oldest kept = %v, want s02 (old and s00, s01 pruned)", rows[0]["id"])
	}
}

// Removing a project (or clearing its graph) removes its sessions.
func TestRemovedWithTheProject(t *testing.T) {
	st, ctx := store(t)
	k := key(t, "rm")
	t.Cleanup(func() { _ = projectrepo.New(st).Delete(context.Background(), k) })
	s := New(st, k)
	count := func() int64 {
		rows, err := st.Query(ctx, `MATCH (h:HookSession {project: $p}) RETURN count(h) AS n`, map[string]any{"p": k})
		if err != nil {
			t.Fatal(err)
		}
		return rows[0]["n"].(int64)
	}
	if err := s.Put(ctx, "s1", nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := graphrepo.ForProject(st, k).Clear(ctx); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 0 {
		t.Errorf("Clear kept %d sessions", n)
	}
	if err := s.Put(ctx, "s1", nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := projectrepo.New(st).Delete(ctx, k); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 0 {
		t.Errorf("projects rm kept %d sessions", n)
	}
}

// The baseline is recorded once per session, independently of the injected
// findings, and pruned with the session.
func TestBaseline(t *testing.T) {
	st, ctx := store(t)
	k := key(t, "base")
	t.Cleanup(func() { _ = projectrepo.New(st).Delete(context.Background(), k) })
	s := New(st, k)
	if _, ok, err := s.Baseline(ctx, "s1"); err != nil || ok {
		t.Fatalf("no baseline yet: ok=%v err=%v", ok, err)
	}
	now := time.Now()
	first := []rules.Finding{{Rule: "god-package", Severity: "warn", Message: "x", Data: map[string]any{"big": 1}}}
	if err := s.PutBaseline(ctx, "s1", first, now); err != nil {
		t.Fatal(err)
	}
	if err := s.PutBaseline(ctx, "s1", nil, now); err != nil { // kept: not replaced
		t.Fatal(err)
	}
	if err := s.Put(ctx, "s1", nil, now); err != nil { // the injected state is separate
		t.Fatal(err)
	}
	got, ok, err := s.Baseline(ctx, "s1")
	if err != nil || !ok || len(got) != 1 || got[0].Key() != first[0].Key() || got[0].Data != nil {
		t.Fatalf("Baseline = %+v ok=%v err=%v", got, ok, err)
	}
	// an empty baseline is a baseline
	if err := s.PutBaseline(ctx, "s2", nil, now); err != nil {
		t.Fatal(err)
	}
	if got, ok, _ := s.Baseline(ctx, "s2"); !ok || len(got) != 0 {
		t.Fatalf("empty baseline: %+v ok=%v", got, ok)
	}
	// ... but not injected state: the prompt hook still summarises
	if _, ok, _ := s.Get(ctx, "s2"); ok {
		t.Error("PutBaseline must not create injected state")
	}
	// a session that only has injected state has no baseline
	if err := s.Put(ctx, "s3", first, now); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.Baseline(ctx, "s3"); ok {
		t.Error("Put must not create a baseline")
	}
}

// Debt that reached the checkout through git is added to the baseline of
// every session that has one; sessions without a baseline get none.
func TestAbsorb(t *testing.T) {
	st, ctx := store(t)
	k := key(t, "absorb")
	t.Cleanup(func() { _ = projectrepo.New(st).Delete(context.Background(), k) })
	s := New(st, k)
	now := time.Now()
	mine := rules.Finding{Rule: "import-cycle", Severity: "error", Message: "a -> b -> a"}
	theirs := rules.Finding{Rule: "import-cycle", Severity: "error", Message: "c -> d -> c"}
	if err := s.PutBaseline(ctx, "s1", []rules.Finding{mine}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.PutBaseline(ctx, "s2", nil, now); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "s3", nil, now); err != nil { // injected state only
		t.Fatal(err)
	}
	if err := s.Absorb(ctx, []rules.Finding{theirs, mine}, now); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]int{"s1": 2, "s2": 2} {
		got, ok, err := s.Baseline(ctx, id)
		if err != nil || !ok || len(got) != want {
			t.Errorf("%s baseline = %+v ok=%v err=%v", id, got, ok, err)
		}
	}
	if _, ok, _ := s.Baseline(ctx, "s3"); ok {
		t.Error("Absorb created a baseline")
	}
	if err := s.Absorb(ctx, nil, now); err != nil {
		t.Fatal(err)
	}
}
