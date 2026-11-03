package cmd

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	graphrepo "github.com/nonchan7720/depgraph/pkg/infrastructure/repository/graph"
	projectrepo "github.com/nonchan7720/depgraph/pkg/infrastructure/repository/project"
	rulesrepo "github.com/nonchan7720/depgraph/pkg/infrastructure/repository/rules"
)

// depgraph's own repository has no findings: what depgraph reports as debt
// is fixed here before it is merged (dogfooding). A new finding fails CI.
func TestOwnRepositoryHasNoFindings(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	st, err := graphrepo.New(ctx, env("NEO4J_URI", "bolt://localhost:7687"), env("NEO4J_USER", "neo4j"),
		env("NEO4J_PASSWORD", "depgraph123"), env("NEO4J_DATABASE", "neo4j"))
	if err != nil {
		t.Skipf("neo4j not available: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	flagKey := fmt.Sprintf("zz_test_%d_dogfood", time.Now().UnixNano())
	p, _, err := resolveProject(ctx, projectrepo.New(st), root, flagKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = projectrepo.New(st).Delete(context.Background(), p.Key) })

	var out bytes.Buffer
	c := newRootCommand(&options{})
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetArgs([]string{"scan", "--root", root, "--project", flagKey, "--claude-md", ""})
	if err := c.ExecuteContext(ctx); err != nil {
		t.Fatalf("scan: %v\n%s", err, out.String())
	}
	cfg, err := loadRules(root)
	if err != nil {
		t.Fatal(err)
	}
	findings, err := rulesrepo.New(st, p.Key).Evaluate(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var msgs []string
	for _, f := range findings {
		msgs = append(msgs, fmt.Sprintf("[%s] %s: %s", f.Severity, f.Rule, f.Message))
	}
	if len(msgs) != 0 {
		t.Errorf("depgraph reports %d finding(s) on its own repository; fix them:\n%s", len(msgs), strings.Join(msgs, "\n"))
	}
}
