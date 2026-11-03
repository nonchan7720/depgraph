package analysis

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

type service struct {
	d   Deps
	opt Options
	cfg rules.Config
	out io.Writer

	// mu serializes graph writes between HandleChanges (tree-sitter stage) and
	// the refiner. evalMu guards prev/ready/notifier, which both stages touch.
	mu       sync.Mutex
	evalMu   sync.Mutex
	prev     []rules.Finding
	ready    bool
	notifier Notifier
	ref      *refiner
}

// New returns the analysis service.
func New(d Deps, opt Options) Service {
	s := &service{d: d, opt: opt, cfg: rules.DefaultConfig(), out: d.Out}
	if d.Config != nil {
		s.cfg = *d.Config
	}
	if s.out == nil {
		s.out = io.Discard
	}
	return s
}

func (s *service) logf(format string, a ...any) { _, _ = fmt.Fprintf(s.out, format, a...) }

func (s *service) SetNotifier(n Notifier) {
	s.evalMu.Lock()
	s.notifier = n
	s.evalMu.Unlock()
}

func (s *service) FullScan(ctx context.Context) error {
	repo := s.d.Repo
	if s.opt.Reset {
		if err := repo.Clear(ctx); err != nil {
			return err
		}
	}
	start := time.Now()
	seen := map[string]struct{}{}
	err := s.d.Scan(ctx, s.d.Resolver, s.opt.Root, func(fg *graph.FileGraph) error {
		seen[fg.Path] = struct{}{}
		return repo.UpsertFile(ctx, fg)
	})
	if err != nil {
		return err
	}
	// Drop files left from earlier runs: deleted while depgraph was stopped,
	// newly ignored, or scanned under another root. Without this the graph
	// accumulates garbage across restarts.
	existing, err := repo.FilePaths(ctx)
	if err != nil {
		return err
	}
	stale := graph.StalePaths(existing, seen)
	if err := repo.DeleteFiles(ctx, stale); err != nil {
		return err
	}
	for _, step := range []func(context.Context) error{
		repo.RebuildAllPackageImports, repo.PruneEmptyPackages, repo.PruneOrphanExternals,
	} {
		if err := step(ctx); err != nil {
			return err
		}
	}
	s.logf("scanned %d files in %s", len(seen), time.Since(start).Round(time.Millisecond))
	if len(stale) > 0 {
		s.logf(" (removed %d stale)", len(stale))
	}
	s.logf("\n")
	return nil
}

func (s *service) HandleChanges(ctx context.Context, changed []string) error {
	s.mu.Lock()
	err := s.applyChanges(ctx, changed)
	if err == nil && s.ref != nil {
		s.ref.Submit(changed)
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
	return s.Evaluate(ctx)
}

// applyChanges must be called with s.mu held.
func (s *service) applyChanges(ctx context.Context, changed []string) error {
	repo := s.d.Repo
	touched := map[string]struct{}{}
	for _, rel := range changed {
		touched[graph.PackageKey(rel)] = struct{}{}
		if _, err := os.Stat(filepath.Join(s.opt.Root, filepath.FromSlash(rel))); err != nil {
			if err := repo.DeleteFile(ctx, rel); err != nil {
				return err
			}
			continue
		}
		fg, err := s.d.Resolver.ResolveFile(ctx, s.opt.Root, rel)
		if err != nil {
			s.logf("  skip %s: %v\n", rel, err)
			continue
		}
		if err := repo.UpsertFile(ctx, fg); err != nil {
			return err
		}
	}
	pkgs := make([]string, 0, len(touched))
	for p := range touched {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)
	for _, p := range pkgs {
		if err := repo.RebuildPackageImports(ctx, p); err != nil {
			return err
		}
	}
	if err := repo.PruneEmptyPackages(ctx); err != nil {
		return err
	}
	return repo.PruneOrphanExternals(ctx)
}

func (s *service) Evaluate(ctx context.Context) error {
	s.evalMu.Lock()
	defer s.evalMu.Unlock()
	findings, err := s.d.Rules.Evaluate(ctx, s.cfg)
	if err != nil {
		return err
	}
	stats, err := s.d.Repo.Stats(ctx)
	if err != nil {
		return err
	}
	if !s.ready {
		printFindings(s.out, "findings", findings)
		s.ready = true
	} else {
		added, removed := rules.Diff(s.prev, findings)
		if len(added) == 0 && len(removed) == 0 {
			s.logf("  no change (%d findings)\n", len(findings))
		}
		printFindings(s.out, "+ new", added)
		printFindings(s.out, "- resolved", removed)
	}
	s.prev = findings
	if s.notifier != nil {
		s.notifier.SetFindings(stats, findings)
	}
	if s.d.Report != nil {
		if err := s.d.Report.Write(stats, findings); err != nil {
			return err
		}
	}
	return nil
}

func (s *service) StartRefinement(ctx context.Context, r Refinement) func() {
	ref := &refiner{
		root: s.opt.Root,
		mu:   &s.mu,
		resolve: func(ctx context.Context, rel string) (*graph.FileGraph, error) {
			return s.d.Resolver.ResolveFile(ctx, s.opt.Root, rel)
		},
		enrich: r.Enrich,
		upsert: s.d.Repo.UpsertRefs,
		notify: r.FilesChanged,
		done: func(ctx context.Context) {
			s.logf("%s lsp: refined\n", time.Now().Format("15:04:05"))
			if err := s.Evaluate(ctx); err != nil {
				s.logf("  error: %v\n", err)
			}
		},
		warn: func(format string, a ...any) { s.logf(format+"\n", a...) },
	}
	s.mu.Lock()
	s.ref = ref
	s.mu.Unlock()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); ref.Run(ctx) }()
	return wg.Wait
}

func printFindings(w io.Writer, label string, fs []rules.Finding) {
	if len(fs) == 0 {
		return
	}
	sort.Slice(fs, func(i, j int) bool { return fs[i].Key() < fs[j].Key() })
	_, _ = fmt.Fprintf(w, "%s (%d):\n", label, len(fs))
	for _, f := range fs {
		_, _ = fmt.Fprintf(w, "  [%s] %s: %s\n", f.Severity, f.Rule, f.Message)
	}
}
