package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/project"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
	graphrepo "github.com/nonchan7720/depgraph/pkg/infrastructure/repository/graph"
	"github.com/nonchan7720/depgraph/pkg/interfaces/handler/hook"
)

// fakeProjects is an in-memory project.Repository.
type fakeProjects map[string]project.Project

func (f fakeProjects) Get(_ context.Context, key string) (*project.Project, error) {
	if p, ok := f[key]; ok {
		return &p, nil
	}
	return nil, nil
}
func (f fakeProjects) Upsert(_ context.Context, p project.Project) error { f[p.Key] = p; return nil }
func (f fakeProjects) List(context.Context) ([]project.Project, error) {
	var out []project.Project
	for _, p := range f {
		out = append(out, p)
	}
	return out, nil
}
func (f fakeProjects) Delete(_ context.Context, key string) error { delete(f, key); return nil }

func mkdir(t *testing.T, parts ...string) string {
	t.Helper()
	p := filepath.Join(parts...)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestResolveProject(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	a, b := mkdir(t, base, "x", "app"), mkdir(t, base, "y", "app")
	repo := fakeProjects{}

	p, stored, err := resolveProject(ctx, repo, a, "")
	if err != nil || stored || p.Key != "app" || p.Root != a {
		t.Fatalf("new: %+v stored=%v err=%v", p, stored, err)
	}
	repo["app"] = p
	if p, stored, err = resolveProject(ctx, repo, a, ""); err != nil || !stored {
		t.Fatalf("same root: %+v stored=%v err=%v", p, stored, err)
	}
	// another live checkout without a shared origin → conflict
	var ce *project.ConflictError
	if _, _, err = resolveProject(ctx, repo, b, ""); !errors.As(err, &ce) {
		t.Fatalf("conflict expected, got %v", err)
	}
	// --project separates them
	if p, stored, err = resolveProject(ctx, repo, b, "app-y"); err != nil || stored || p.Key != "app-y" {
		t.Fatalf("--project: %+v stored=%v err=%v", p, stored, err)
	}
	// the first checkout is gone → moved
	if err := os.RemoveAll(a); err != nil {
		t.Fatal(err)
	}
	if p, stored, err = resolveProject(ctx, repo, b, ""); err != nil || !stored || p.Root != b {
		t.Fatalf("moved: %+v stored=%v err=%v", p, stored, err)
	}
}

type fakeHookStore struct{ stats graph.Stats }

func (s fakeHookStore) Stats(context.Context) (graph.Stats, error) { return s.stats, nil }

type fakeHookRules struct{ n int }

func (r *fakeHookRules) Evaluate(context.Context, rules.Config) ([]rules.Finding, error) {
	r.n++
	return []rules.Finding{{Rule: "god-package", Severity: "warn", Message: "x"}}, nil
}

func TestHookProjectData(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	root, other := mkdir(t, base, "a", "app"), mkdir(t, base, "b", "app")
	repo := fakeProjects{}
	r := &fakeHookRules{}
	var scopedTo []string
	pd := projectData{projects: repo, flagKey: "", scoped: func(key string) (statsReader, rules.Repository) {
		scopedTo = append(scopedTo, key)
		return fakeHookStore{stats: graph.Stats{Files: 3}}, r
	}}
	var sessionsOf []string
	pd.sessions = func(key string) hook.SessionStore {
		sessionsOf = append(sessionsOf, key)
		return nil
	}

	// not scanned yet → no data, no evaluation
	if _, ok, err := pd.Load(ctx, root); ok || err != nil || r.n != 0 {
		t.Fatalf("unscanned: ok=%v err=%v evals=%d", ok, err, r.n)
	}
	repo["app"] = project.Project{Key: "app", Root: root}
	snap, ok, err := pd.Load(ctx, root)
	if err != nil || !ok || snap.Stats.Files != 3 || len(snap.Findings) != 1 || strings.Join(scopedTo, ",") != "app" {
		t.Fatalf("Load = %+v ok=%v err=%v scoped=%v", snap, ok, err, scopedTo)
	}
	// the session state is the resolved project's
	if strings.Join(sessionsOf, ",") != "app" {
		t.Errorf("sessions of %v, want app", sessionsOf)
	}
	// another live root with the same key is not this project; nothing is written
	if _, ok, _ := pd.Load(ctx, other); ok {
		t.Error("other root with the same key must not load")
	}
	if repo["app"].Root != root {
		t.Error("hook must not update the stored project")
	}
	// --project is honoured
	pd.flagKey = "custom"
	repo["custom"] = project.Project{Key: "custom", Root: other}
	if _, ok, err := pd.Load(ctx, other); !ok || err != nil || scopedTo[len(scopedTo)-1] != "custom" {
		t.Errorf("--project: ok=%v err=%v scoped=%v", ok, err, scopedTo)
	}
}

// End to end on Neo4j: two checkouts named alike collide unless --project
// separates them; each project keeps its own data; projects rm removes one.
func TestScanTwoProjects(t *testing.T) {
	uri := env("NEO4J_URI", "bolt://localhost:7687")
	t.Setenv("NEO4J_URI", uri) // the commands below use this external Neo4j, never a provided one
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	st, err := graphrepo.New(ctx, uri, env("NEO4J_USER", "neo4j"), env("NEO4J_PASSWORD", "depgraph123"), env("NEO4J_DATABASE", "neo4j"))
	if err != nil {
		t.Skipf("neo4j not available: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	key := fmt.Sprintf("zz_test_%d", time.Now().UnixNano())
	base := t.TempDir()
	a, b := mkdir(t, base, "x", key), mkdir(t, base, "y", key)
	for _, d := range []string{a, b} {
		mkdir(t, d, "p")
		if err := os.WriteFile(filepath.Join(d, "p", "p.go"), []byte("package p\nfunc P() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(b, "p", "q.go"), []byte("package p\nfunc Q() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		c := newRootCommand(&options{})
		c.SetOut(&out)
		c.SetErr(&out)
		c.SetArgs(args)
		err := c.ExecuteContext(ctx)
		return out.String(), err
	}
	t.Cleanup(func() {
		_, _ = run("projects", "rm", key)
		_, _ = run("projects", "rm", key+"-b")
	})

	if out, err := run("scan", "--root", a, "--claude-md", ""); err != nil {
		t.Fatalf("scan a: %v\n%s", err, out)
	}
	out, err := run("scan", "--root", b, "--claude-md", "", "--reset")
	var ce *project.ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("scan b with the same key must conflict, got %v\n%s", err, out)
	}
	if out, err := run("scan", "--root", b, "--claude-md", "", "--project", key+"-b", "--reset"); err != nil {
		t.Fatalf("scan b: %v\n%s", err, out)
	}
	files := func(k string) int64 {
		s, err := graphrepo.ForProject(st, k).Stats(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return s.Files
	}
	if fa, fb := files(key), files(key+"-b"); fa != 1 || fb != 2 {
		t.Fatalf("files a=%d b=%d, want 1 and 2", fa, fb)
	}
	out, err = run("projects", "list")
	if err != nil || !strings.Contains(out, key) || !strings.Contains(out, a) || !strings.Contains(out, key+"-b") {
		t.Fatalf("projects list: %v\n%s", err, out)
	}
	if out, err := run("projects", "rm", key+"-b"); err != nil {
		t.Fatalf("rm: %v\n%s", err, out)
	}
	if fa, fb := files(key), files(key+"-b"); fa != 1 || fb != 0 {
		t.Fatalf("after rm files a=%d b=%d", fa, fb)
	}
	if _, err := run("projects", "rm", key+"-b"); err == nil {
		t.Error("rm of an unknown project must fail")
	}

	// prune: a's root disappears
	if err := os.RemoveAll(a); err != nil {
		t.Fatal(err)
	}
	out, err = run("projects", "prune", "--dry-run")
	if err != nil || !strings.Contains(out, key) || files(key) != 1 {
		t.Fatalf("prune --dry-run: %v files=%d\n%s", err, files(key), out)
	}
	if out, err := run("projects", "prune"); err != nil || files(key) != 0 {
		t.Fatalf("prune: %v files=%d\n%s", err, files(key), out)
	}
}
