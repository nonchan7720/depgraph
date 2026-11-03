package cmd

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/project"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/config"
	graphrepo "github.com/nonchan7720/depgraph/pkg/infrastructure/repository/graph"
	"github.com/nonchan7720/depgraph/pkg/services/analysis"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// End to end on Neo4j: scan / check evaluate the layers, forbidden pairs and
// severities of <root>/.depgraph.yml; an invalid file fails the command.
func TestScanAppliesDepgraphYml(t *testing.T) {
	t.Setenv("NEO4J_URI", env("NEO4J_URI", "bolt://localhost:7687")) // the commands below use this external Neo4j
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	st, err := graphrepo.New(ctx, env("NEO4J_URI", "bolt://localhost:7687"), env("NEO4J_USER", "neo4j"), env("NEO4J_PASSWORD", "depgraph123"), env("NEO4J_DATABASE", "neo4j"))
	if err != nil {
		t.Skipf("neo4j not available: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	key := fmt.Sprintf("zz_test_%d_yml", time.Now().UnixNano())
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod":                "module example.com/app\n\ngo 1.22\n",
		"core/model/m.go":       "package model\n\nimport _ \"example.com/app/adapters/sql\"\n",
		"adapters/sql/s.go":     "package sql\n",
		"pkg/feedback/f.go":     "package feedback\n",
		"pkg/eventhandler/e.go": "package eventhandler\n\nimport _ \"example.com/app/pkg/feedback\"\n",
		config.FileName: fmt.Sprintf(`project: %s
layers:
  core: [core/**]
  adapters: [adapters/**]
forbidden:
  core: [adapters]
rules:
  layer-violation: {severity: warn}
`, key),
	})
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		c := newRootCommand(&options{})
		c.SetOut(&out)
		c.SetErr(&out)
		c.SetArgs(args)
		err := c.ExecuteContext(ctx)
		return out.String(), err
	}
	t.Cleanup(func() { _, _ = run("projects", "rm", key) })

	out, err := run("scan", "--root", root, "--claude-md", "")
	if err != nil {
		t.Fatalf("scan: %v\n%s", err, out)
	}
	if !strings.Contains(out, "[warn] layer-violation: core/model -> adapters/sql (core must not import adapters)") {
		t.Errorf("configured layer violation missing:\n%s", out)
	}
	if strings.Count(out, "layer-violation") != 1 {
		t.Errorf("only the configured layers must be checked:\n%s", out)
	}

	writeTree(t, root, map[string]string{config.FileName: fmt.Sprintf("project: %s\nrules:\n  layer-violaton: {severity: warn}\n", key)})
	out, err = run("check", "--root", root, "--claude-md", "")
	if err == nil || !strings.Contains(err.Error(), `unknown rule "layer-violaton"`) || !strings.Contains(err.Error(), config.FileName) {
		t.Errorf("invalid .depgraph.yml must fail check, got %v\n%s", err, out)
	}
}

// The hook evaluates with the root's .depgraph.yml.
func TestHookProjectDataUsesConfig(t *testing.T) {
	ctx := context.Background()
	root := mkdir(t, t.TempDir(), "app")
	writeTree(t, root, map[string]string{config.FileName: "rules:\n  god-package: {fanIn: 33}\n"})
	repo := fakeProjects{"app": project.Project{Key: "app", Root: root}}
	r := &recordingRules{}
	pd := projectData{projects: repo, scoped: func(string) (statsReader, rules.Repository) {
		return fakeHookStore{stats: graph.Stats{Files: 1}}, r
	}, config: loadRules}
	if _, ok, err := pd.Load(ctx, root); !ok || err != nil {
		t.Fatalf("Load: ok=%v err=%v", ok, err)
	}
	if r.cfg.GodPackageFanIn != 33 {
		t.Errorf("hook evaluated with %+v", r.cfg)
	}
}

type recordingRules struct{ cfg rules.Config }

func (r *recordingRules) Evaluate(_ context.Context, cfg rules.Config) ([]rules.Finding, error) {
	r.cfg = cfg
	return nil, nil
}

// fakeService records what watch asks of the analysis service.
type fakeService struct {
	analysis.Service
	cfgs    []rules.Config
	changes [][]string
	evals   int
}

func (s *fakeService) SetConfig(c rules.Config) { s.cfgs = append(s.cfgs, c) }
func (s *fakeService) HandleChanges(_ context.Context, changed []string) error {
	s.changes = append(s.changes, changed)
	return nil
}
func (s *fakeService) Evaluate(context.Context) error { s.evals++; return nil }

// A change of .depgraph.yml reloads the rules and re-evaluates; an invalid
// file keeps the previous rules; source files go to HandleChanges as before.
func TestWatchReloadsConfig(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	svc := &fakeService{}
	var out bytes.Buffer
	b := watchBatch{root: root, svc: svc, out: &out}

	writeTree(t, root, map[string]string{config.FileName: "rules:\n  god-package: {fanIn: 5}\n"})
	b.handle(ctx, []string{config.FileName})
	if len(svc.cfgs) != 1 || svc.cfgs[0].GodPackageFanIn != 5 || svc.evals != 1 || len(svc.changes) != 0 {
		t.Fatalf("reload: cfgs=%+v evals=%d changes=%v", svc.cfgs, svc.evals, svc.changes)
	}

	// source files and the config together: one HandleChanges (which evaluates)
	writeTree(t, root, map[string]string{config.FileName: "rules:\n  god-package: {fanIn: 6}\n"})
	b.handle(ctx, []string{"a.go", config.FileName})
	if len(svc.cfgs) != 2 || svc.cfgs[1].GodPackageFanIn != 6 || svc.evals != 1 || len(svc.changes) != 1 || strings.Join(svc.changes[0], ",") != "a.go" {
		t.Fatalf("mixed: cfgs=%d evals=%d changes=%v", len(svc.cfgs), svc.evals, svc.changes)
	}

	writeTree(t, root, map[string]string{config.FileName: "rules:\n  nope: {}\n"})
	b.handle(ctx, []string{config.FileName})
	if len(svc.cfgs) != 2 || svc.evals != 1 || !strings.Contains(out.String(), `unknown rule "nope"`) || !strings.Contains(out.String(), "keeping the previous rules") {
		t.Fatalf("invalid: cfgs=%d evals=%d out=%q", len(svc.cfgs), svc.evals, out.String())
	}

	// removed: back to the defaults
	if err := os.Remove(filepath.Join(root, config.FileName)); err != nil {
		t.Fatal(err)
	}
	b.handle(ctx, []string{config.FileName})
	if len(svc.cfgs) != 3 || svc.cfgs[2].GodPackageFanIn != rules.DefaultConfig().GodPackageFanIn {
		t.Fatalf("removed: %+v", svc.cfgs)
	}
}
