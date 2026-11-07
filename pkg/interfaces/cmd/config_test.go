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

	"github.com/nonchan7720/depgraph/pkg/domain/project"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
	"github.com/nonchan7720/depgraph/pkg/domain/view"
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

	// --fail-on: exit non-zero on findings of at least that severity (CI)
	if out, err := run("check", "--root", root, "--claude-md", "", "--fail-on", "warn"); err == nil || !strings.Contains(err.Error(), "at least warn") {
		t.Errorf("check --fail-on warn must fail on the warn finding, got %v\n%s", err, out)
	}
	if out, err := run("check", "--root", root, "--claude-md", "", "--fail-on", "error"); err != nil {
		t.Errorf("check --fail-on error must pass without error findings, got %v\n%s", err, out)
	}
	if out, err := run("scan", "--root", root, "--claude-md", "", "--fail-on", "warn"); err == nil {
		t.Errorf("scan --fail-on warn must fail on the warn finding\n%s", out)
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
		return fakeHookStore{stats: view.Stats{Files: 1}}, r
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

// A .depgraph.yml below the root (a package of a monorepo) reloads the rules
// too, and settings it cannot apply are reported.
func TestWatchReloadsNestedConfig(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	svc := &fakeService{}
	var out bytes.Buffer
	b := watchBatch{root: root, svc: svc, out: &out}

	nested := "lib/auth/" + config.FileName
	writeTree(t, root, map[string]string{nested: "layers: {domain: [\"domain/**\"], adapter: [\"adapter/**\"]}\nforbidden: {domain: [adapter]}\nrules: {god-package: {fanIn: 3}}\n"})
	b.handle(ctx, []string{nested})
	if len(svc.cfgs) != 1 || len(svc.cfgs[0].Components) != 1 || svc.cfgs[0].Components[0].Dir != "lib/auth" || svc.evals != 1 {
		t.Fatalf("reload: cfgs=%+v evals=%d", svc.cfgs, svc.evals)
	}
	if !strings.Contains(out.String(), "rules.god-package.fanIn applies only where lib/auth is the root") {
		t.Errorf("warning not printed:\n%s", out.String())
	}
	if !watchedConfig(nested) || !watchedConfig(config.FileName) || watchedConfig("lib/auth/x.yml") {
		t.Error("watchedConfig")
	}
}

// End to end on Neo4j: a monorepo of a Go pkg/ under the default layers, a
// Go library with its own .depgraph.yml and TypeScript packages/* declared by
// components: of the root's file.
func TestScanMonorepo(t *testing.T) {
	t.Setenv("NEO4J_URI", env("NEO4J_URI", "bolt://localhost:7687"))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	st, err := graphrepo.New(ctx, env("NEO4J_URI", "bolt://localhost:7687"), env("NEO4J_USER", "neo4j"), env("NEO4J_PASSWORD", "depgraph123"), env("NEO4J_DATABASE", "neo4j"))
	if err != nil {
		t.Skipf("neo4j not available: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	key := fmt.Sprintf("zz_test_%d_monorepo", time.Now().UnixNano())
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		config.FileName: fmt.Sprintf(`project: %s
components:
  "packages/*":
    layers: {api: ["src/api/**"], ui: ["src/components/**"]}
    forbidden: {api: [ui]}
`, key),
		"go.mod":                      "module example.com/mono\n\ngo 1.22\n",
		"pkg/handler/h.go":            "package handler\n\nimport _ \"example.com/mono/pkg/repository\"\n",
		"pkg/repository/r.go":         "package repository\n",
		"lib/auth/" + config.FileName: "layers: {domain: [\"domain/**\"], adapter: [\"adapter/**\"]}\nforbidden: {domain: [adapter]}\n",
		"lib/auth/domain/d.go":        "package domain\n\nimport _ \"example.com/mono/lib/auth/adapter\"\n",
		"lib/auth/adapter/a.go":       "package adapter\n",
		// a handler and a repository segment, but lib/auth has layers of its own
		"lib/auth/handler/h.go":                 "package handler\n\nimport _ \"example.com/mono/lib/auth/repository\"\n",
		"lib/auth/repository/r.go":              "package repository\n",
		"packages/web/package.json":             `{"name": "@mono/web", "main": "src/index.ts"}`,
		"packages/web/src/index.ts":             "export * from './api/client';\n",
		"packages/web/src/api/client.ts":        "import { Button } from '../components/button';\nexport const client = () => Button;\n",
		"packages/web/src/components/button.ts": "export const Button = 1;\n",
		"packages/kit/package.json":             `{"name": "@mono/kit", "main": "src/index.ts"}`,
		"packages/kit/src/index.ts":             "export * from './components/card';\n",
		"packages/kit/src/components/card.ts":   "export const Card = 1;\n",
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
	for _, want := range []string{
		"layer-violation: pkg/handler -> pkg/repository (handler must not import repository)",
		"layer-violation: lib/auth/domain -> lib/auth/adapter (lib/auth: domain must not import adapter)",
		"layer-violation: packages/web/src/api -> packages/web/src/components (packages/web: api must not import ui)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if n := strings.Count(out, "layer-violation:"); n != 3 {
		t.Errorf("%d layer violations, want 3:\n%s", n, out)
	}
}

// A module manifest (go.mod, package.json, ...) reloads the rules too: the
// modules of module-cycle are read with them.
func TestWatchReloadsModules(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	svc := &fakeService{}
	var out bytes.Buffer
	b := watchBatch{root: root, svc: svc, out: &out}
	writeTree(t, root, map[string]string{"packages/web/package.json": "{}"})
	b.handle(ctx, []string{"packages/web/package.json"})
	if len(svc.cfgs) != 1 || strings.Join(svc.cfgs[0].Modules, ",") != "packages/web" {
		t.Fatalf("modules not reloaded: %+v", svc.cfgs)
	}
	if len(svc.changes) != 1 || strings.Join(svc.changes[0], ",") != "packages/web/package.json" {
		t.Errorf("the manifest must still reach the service: %v", svc.changes)
	}
}
