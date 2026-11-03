package rules

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
	graphrepo "github.com/nonchan7720/depgraph/pkg/infrastructure/repository/graph"
)

func get(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

// Each project's findings come only from its own graph.
func TestEvaluateIsScopedByProject(t *testing.T) {
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
	now := time.Now().UnixNano()
	dirty, clean := fmt.Sprintf("zz_test_%d_dirty", now), fmt.Sprintf("zz_test_%d_clean", now)
	file := func(pkg, imp string, syms ...string) *graph.FileGraph {
		fg := &graph.FileGraph{Path: pkg + "/f.go", Package: pkg, Language: graph.Go}
		for _, s := range syms {
			fg.Symbols = append(fg.Symbols, graph.Symbol{Name: s, Kind: "function", Exported: true})
		}
		if imp != "" {
			fg.Imports = []graph.Import{{Spec: imp, Target: imp}}
		}
		return fg
	}
	for _, p := range []struct {
		key   string
		files []*graph.FileGraph
	}{
		{dirty, []*graph.FileGraph{file("handler", "repository", "Dup"), file("repository", "handler", "Dup")}},
		{clean, []*graph.FileGraph{file("handler", "", "Dup"), file("repository", "", "Other")}},
	} {
		r := graphrepo.ForProject(st, p.key)
		t.Cleanup(func() { _ = r.Clear(context.Background()) })
		for _, fg := range p.files {
			if err := r.UpsertFile(ctx, fg); err != nil {
				t.Fatal(err)
			}
		}
		if err := r.RebuildAllPackageImports(ctx); err != nil {
			t.Fatal(err)
		}
	}
	cfg := rules.DefaultConfig()
	cfg.GodPackageFanIn = 1
	count := func(key string) map[string]int {
		fs, err := New(st, key).Evaluate(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]int{}
		for _, f := range fs {
			out[f.Rule]++
		}
		return out
	}
	if got := count(dirty); got["import-cycle"] != 1 || got["layer-violation"] != 1 || got["god-package"] != 2 || got["duplicate-symbol"] != 1 {
		t.Errorf("dirty findings = %v", got)
	}
	if got := count(clean); len(got) != 0 {
		t.Errorf("clean project sees other project's findings: %v", got)
	}
}

// An import that resolves to nothing (a path or alias that does not exist,
// often written by an AI agent from memory) is a finding per file and spec.
func TestEvaluateUnresolvedImports(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	st, err := graphrepo.New(ctx, get("NEO4J_URI", "bolt://localhost:7687"), get("NEO4J_USER", "neo4j"),
		get("NEO4J_PASSWORD", "depgraph123"), get("NEO4J_DATABASE", "neo4j"))
	if err != nil {
		t.Skipf("neo4j not available: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	if err := st.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf("zz_test_%d_unresolved", time.Now().UnixNano())
	g := graphrepo.ForProject(st, key)
	t.Cleanup(func() { _ = g.Clear(context.Background()) })
	fg := &graph.FileGraph{Path: "src/a.ts", Package: "src", Language: graph.TypeScript, Imports: []graph.Import{
		{Spec: "react", Target: "ext:react", External: true},
		{Spec: "@/missing", Unresolved: true, Reason: `Could not resolve "@/missing"`},
	}}
	if err := g.UpsertFile(ctx, fg); err != nil {
		t.Fatal(err)
	}
	findings, err := New(st, key).Evaluate(ctx, rules.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	var got []rules.Finding
	for _, f := range findings {
		if f.Rule == "unresolved-import" {
			got = append(got, f)
		}
	}
	if len(got) != 1 {
		t.Fatalf("unresolved-import findings = %+v", got)
	}
	f := got[0]
	if f.Severity != "warn" || f.ID != "src/a.ts|@/missing" ||
		f.Message != `src/a.ts imports "@/missing", which does not resolve (Could not resolve "@/missing")` {
		t.Errorf("finding = %+v", f)
	}
}
