package analysis

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// ---- fakes ------------------------------------------------------------------

type fakeRepo struct {
	mu       sync.Mutex
	calls    []string
	files    map[string]*graph.FileGraph
	existing []string // returned by FilePaths
	refs     map[string][]graph.Reference
	deleted  []string
}

func newFakeRepo(existing ...string) *fakeRepo {
	return &fakeRepo{files: map[string]*graph.FileGraph{}, refs: map[string][]graph.Reference{}, existing: existing}
}

func (f *fakeRepo) log(s string) { f.mu.Lock(); f.calls = append(f.calls, s); f.mu.Unlock() }
func (f *fakeRepo) called(s string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == s {
			return true
		}
	}
	return false
}

func (f *fakeRepo) Clear(context.Context) error { f.log("Clear"); return nil }
func (f *fakeRepo) UpsertFile(_ context.Context, fg *graph.FileGraph) error {
	f.mu.Lock()
	f.files[fg.Path] = fg
	f.mu.Unlock()
	f.log("UpsertFile " + fg.Path)
	return nil
}
func (f *fakeRepo) UpsertRefs(_ context.Context, path string, refs []graph.Reference) error {
	f.mu.Lock()
	f.refs[path] = refs
	f.mu.Unlock()
	f.log("UpsertRefs " + path)
	return nil
}
func (f *fakeRepo) DeleteFile(_ context.Context, p string) error {
	f.log("DeleteFile " + p)
	return nil
}
func (f *fakeRepo) DeleteFiles(_ context.Context, ps []string) error {
	f.mu.Lock()
	f.deleted = append(f.deleted, ps...)
	f.mu.Unlock()
	return nil
}
func (f *fakeRepo) FilePaths(context.Context) ([]string, error) { return f.existing, nil }
func (f *fakeRepo) RebuildPackageImports(_ context.Context, k string) error {
	f.log("RebuildPackageImports " + k)
	return nil
}
func (f *fakeRepo) RebuildAllPackageImports(context.Context) error {
	f.log("RebuildAllPackageImports")
	return nil
}
func (f *fakeRepo) PruneEmptyPackages(context.Context) error { f.log("PruneEmptyPackages"); return nil }
func (f *fakeRepo) PruneOrphanExternals(context.Context) error {
	f.log("PruneOrphanExternals")
	return nil
}
func (f *fakeRepo) Stats(context.Context) (graph.Stats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return graph.Stats{Files: int64(len(f.files))}, nil
}
func (f *fakeRepo) Overview(context.Context) (*graph.Overview, error) { return &graph.Overview{}, nil }
func (f *fakeRepo) PackageDetail(context.Context, string) (*graph.PackageDetail, error) {
	return &graph.PackageDetail{}, nil
}

type fakeRules struct {
	mu   sync.Mutex
	next []rules.Finding
	n    int
	cfg  rules.Config // the last configuration evaluated
}

func (r *fakeRules) Evaluate(_ context.Context, cfg rules.Config) ([]rules.Finding, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.n++
	r.cfg = cfg
	return append([]rules.Finding(nil), r.next...), nil
}

type fakeResolver struct{}

func (fakeResolver) Name() string             { return "fake" }
func (fakeResolver) Supports(rel string) bool { return strings.HasSuffix(rel, ".go") }
func (fakeResolver) ResolveFile(_ context.Context, _, rel string) (*graph.FileGraph, error) {
	return &graph.FileGraph{Path: rel, Package: graph.PackageKey(rel), Symbols: []graph.Symbol{{Name: "F", Kind: "function"}}}, nil
}

type fakeNotifier struct {
	mu       sync.Mutex
	findings [][]rules.Finding
}

func (n *fakeNotifier) SetFindings(_ graph.Stats, fs []rules.Finding) {
	n.mu.Lock()
	n.findings = append(n.findings, fs)
	n.mu.Unlock()
}

type fakeReport struct {
	n int
}

func (r *fakeReport) Write(graph.Stats, []rules.Finding) error { r.n++; return nil }

func writeFile(t *testing.T, root, rel string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// scanAll is a ScanFunc that resolves every supported file under root.
func scanAll(ctx context.Context, r graph.Resolver, root string, fn func(*graph.FileGraph) error) error {
	return filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if !r.Supports(rel) {
			return nil
		}
		fg, err := r.ResolveFile(ctx, root, rel)
		if err != nil {
			return err
		}
		return fn(fg)
	})
}

type fixture struct {
	root   string
	repo   *fakeRepo
	rules  *fakeRules
	report *fakeReport
	out    *bytes.Buffer
	svc    Service
}

func newFixture(t *testing.T, opt Options, existing ...string) *fixture {
	t.Helper()
	f := &fixture{
		root: t.TempDir(), repo: newFakeRepo(existing...), rules: &fakeRules{},
		report: &fakeReport{}, out: &bytes.Buffer{},
	}
	opt.Root = f.root
	f.svc = New(Deps{
		Repo: f.repo, Rules: f.rules, Resolver: fakeResolver{}, Scan: scanAll,
		Report: f.report, Out: f.out,
	}, opt)
	return f
}

// ---- tests ------------------------------------------------------------------

func TestFullScanRemovesStaleFiles(t *testing.T) {
	f := newFixture(t, Options{}, "a/a.go", "gone/old.go", "testdata/x.go")
	writeFile(t, f.root, "a/a.go")
	writeFile(t, f.root, "b/b.go")

	if err := f.svc.FullScan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.repo.called("Clear") {
		t.Error("Clear must not run without Reset")
	}
	for _, p := range []string{"a/a.go", "b/b.go"} {
		if f.repo.files[p] == nil {
			t.Errorf("%s not upserted", p)
		}
	}
	if want := []string{"gone/old.go", "testdata/x.go"}; !reflect.DeepEqual(f.repo.deleted, want) {
		t.Errorf("deleted = %v, want %v", f.repo.deleted, want)
	}
	for _, c := range []string{"RebuildAllPackageImports", "PruneEmptyPackages", "PruneOrphanExternals"} {
		if !f.repo.called(c) {
			t.Errorf("%s not called", c)
		}
	}
	if !strings.Contains(f.out.String(), "removed 2 stale") {
		t.Errorf("log should mention removed stale files: %q", f.out.String())
	}
}

func TestFullScanReset(t *testing.T) {
	f := newFixture(t, Options{Reset: true})
	if err := f.svc.FullScan(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !f.repo.called("Clear") {
		t.Error("Reset should clear the store first")
	}
}

func TestHandleChanges(t *testing.T) {
	f := newFixture(t, Options{})
	n := &fakeNotifier{}
	f.svc.SetNotifier(n)
	writeFile(t, f.root, "a/a.go")

	if err := f.svc.HandleChanges(context.Background(), []string{"a/a.go", "b/deleted.go"}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{
		"UpsertFile a/a.go", "DeleteFile b/deleted.go",
		"RebuildPackageImports a", "RebuildPackageImports b",
		"PruneEmptyPackages", "PruneOrphanExternals",
	} {
		if !f.repo.called(c) {
			t.Errorf("%q not called; calls = %v", c, f.repo.calls)
		}
	}
	if f.rules.n != 1 || len(n.findings) != 1 || f.report.n != 1 {
		t.Errorf("evaluate after changes: rules=%d notifier=%d report=%d", f.rules.n, len(n.findings), f.report.n)
	}
}

func TestEvaluateReportsDiff(t *testing.T) {
	f := newFixture(t, Options{})
	ctx := context.Background()
	f.rules.next = []rules.Finding{{Rule: "r", Severity: "warn", Message: "old"}}
	if err := f.svc.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.out.String(), "findings (1)") {
		t.Errorf("first evaluation lists all findings: %q", f.out.String())
	}
	f.out.Reset()
	f.rules.next = []rules.Finding{{Rule: "r", Severity: "error", Message: "new"}}
	if err := f.svc.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	got := f.out.String()
	if !strings.Contains(got, "+ new (1)") || !strings.Contains(got, "- resolved (1)") {
		t.Errorf("second evaluation shows the diff: %q", got)
	}
}

func TestEvaluateWithoutReport(t *testing.T) {
	f := newFixture(t, Options{})
	f.svc = New(Deps{Repo: f.repo, Rules: f.rules, Resolver: fakeResolver{}, Scan: scanAll, Out: f.out}, Options{Root: f.root})
	if err := f.svc.Evaluate(context.Background()); err != nil {
		t.Fatalf("a nil Report disables CLAUDE.md updates: %v", err)
	}
}

// The configuration from Deps is evaluated; SetConfig replaces it (watch
// reloads .depgraph.yml) for the next evaluation.
func TestEvaluateUsesConfig(t *testing.T) {
	f := newFixture(t, Options{})
	ctx := context.Background()
	if err := f.svc.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	if f.rules.cfg.GodPackageFanIn != rules.DefaultConfig().GodPackageFanIn {
		t.Errorf("default config not used: %+v", f.rules.cfg)
	}
	cfg := rules.DefaultConfig()
	cfg.GodPackageFanIn = 42
	f.svc = New(Deps{Repo: f.repo, Rules: f.rules, Resolver: fakeResolver{}, Scan: scanAll, Out: f.out, Config: &cfg}, Options{Root: f.root})
	if err := f.svc.Evaluate(ctx); err != nil || f.rules.cfg.GodPackageFanIn != 42 {
		t.Errorf("Deps.Config not used: %+v, %v", f.rules.cfg, err)
	}
	cfg.GodPackageFanIn = 7
	f.svc.SetConfig(cfg)
	if err := f.svc.Evaluate(ctx); err != nil || f.rules.cfg.GodPackageFanIn != 7 {
		t.Errorf("SetConfig not used: %+v, %v", f.rules.cfg, err)
	}
}

type fakeRefinement struct {
	mu      sync.Mutex
	changed [][]string
}

func (r *fakeRefinement) Enrich(_ context.Context, fg *graph.FileGraph) error {
	fg.Refs = append(fg.Refs, graph.Reference{Kind: "CALLS", FromFile: fg.Path, FromName: "F", ToFile: "x.go", ToName: "G"})
	return nil
}
func (r *fakeRefinement) FilesChanged(files []string) {
	r.mu.Lock()
	r.changed = append(r.changed, files)
	r.mu.Unlock()
}

func TestHandleChangesRefinesInBackground(t *testing.T) {
	f := newFixture(t, Options{})
	writeFile(t, f.root, "a/a.go")
	ctx, cancel := context.WithCancel(context.Background())
	ref := &fakeRefinement{}
	wait := f.svc.StartRefinement(ctx, ref)

	if err := f.svc.HandleChanges(ctx, []string{"a/a.go"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !f.repo.called("UpsertRefs a/a.go") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	wait()

	f.repo.mu.Lock()
	refs := f.repo.refs["a/a.go"]
	f.repo.mu.Unlock()
	if len(refs) != 1 || refs[0].ToName != "G" {
		t.Fatalf("refined refs not stored: %+v", refs)
	}
	ref.mu.Lock()
	defer ref.mu.Unlock()
	if len(ref.changed) == 0 || !reflect.DeepEqual(ref.changed[0], []string{"a/a.go"}) {
		t.Errorf("servers should be told about changed files: %v", ref.changed)
	}
	// stage 1 evaluation + stage 2 evaluation after refinement
	if f.rules.n < 2 {
		t.Errorf("evaluate should run again after refinement, ran %d times", f.rules.n)
	}
}

func TestPrintFindingsSorted(t *testing.T) {
	var b bytes.Buffer
	printFindings(&b, "x", []rules.Finding{{Rule: "b", Message: "2"}, {Rule: "a", Message: "1"}})
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if len(lines) != 3 || !sort.SliceIsSorted(lines[1:], func(i, j int) bool { return lines[1+i] < lines[1+j] }) {
		t.Errorf("findings should be sorted: %q", b.String())
	}
}
