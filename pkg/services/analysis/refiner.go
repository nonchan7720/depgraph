package analysis

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
)

// refiner is the second stage of the live update. The watch handler has already
// upserted the tree-sitter result and refreshed the UI; the refiner then asks
// the language server about the same files in the background, stores CALLS /
// REFERENCES edges and asks for another UI refresh.
//
// Changes arriving while a batch runs are coalesced into the next batch.
type refiner struct {
	root string
	// mu is the same lock the watch handler holds while it upserts files, so
	// edges are never written between a file's delete and re-create.
	mu *sync.Mutex

	resolve func(ctx context.Context, rel string) (*graph.FileGraph, error)
	enrich  func(ctx context.Context, fg *graph.FileGraph) error
	upsert  func(ctx context.Context, path string, refs []graph.Reference) error
	notify  func(files []string) // tell servers about on-disk changes (may be nil)
	done    func(ctx context.Context)
	warn    func(format string, a ...any)

	pmu     sync.Mutex
	pending map[string]struct{}
	wake    chan struct{}
	once    sync.Once
}

func (r *refiner) init() {
	r.once.Do(func() { r.wake = make(chan struct{}, 1) })
}

// Submit queues files (repo-relative) for refinement. Call it while holding
// r.mu, right after upserting them, so Run can tell whether a result is stale.
func (r *refiner) Submit(files []string) {
	r.init()
	r.pmu.Lock()
	if r.pending == nil {
		r.pending = map[string]struct{}{}
	}
	for _, f := range files {
		r.pending[f] = struct{}{}
	}
	r.pmu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *refiner) take() []string {
	r.pmu.Lock()
	defer r.pmu.Unlock()
	files := make([]string, 0, len(r.pending))
	for f := range r.pending {
		files = append(files, f)
	}
	r.pending = nil
	sort.Strings(files)
	return files
}

func (r *refiner) isPending(f string) bool {
	r.pmu.Lock()
	defer r.pmu.Unlock()
	_, ok := r.pending[f]
	return ok
}

// Run processes batches until ctx is cancelled.
func (r *refiner) Run(ctx context.Context) {
	r.init()
	for {
		select {
		case <-ctx.Done():
			return
		case <-r.wake:
		}
		if files := r.take(); len(files) > 0 {
			r.process(ctx, files)
		}
	}
}

func (r *refiner) process(ctx context.Context, files []string) {
	if r.notify != nil {
		r.notify(files)
	}
	type result struct {
		path string
		refs []graph.Reference
	}
	var results []result
	for _, rel := range files {
		if ctx.Err() != nil {
			return
		}
		if _, err := os.Stat(filepath.Join(r.root, filepath.FromSlash(rel))); err != nil {
			continue // deleted: DeleteFile already removed its symbols and edges
		}
		fg, err := r.resolve(ctx, rel)
		if err != nil {
			r.warn("  lsp: skip %s: %v", rel, err)
			continue
		}
		if err := r.enrich(ctx, fg); err != nil && !errors.Is(err, context.Canceled) {
			r.warn("  lsp: %s: %v", rel, err)
		}
		results = append(results, result{rel, fg.Refs})
	}

	r.mu.Lock()
	stored := 0
	for _, res := range results {
		if r.isPending(res.path) {
			continue // changed again meanwhile: these refs describe old content
		}
		if err := r.upsert(ctx, res.path, res.refs); err != nil {
			r.warn("  lsp: store %s: %v", res.path, err)
			continue
		}
		stored++
	}
	r.mu.Unlock()
	if stored > 0 && ctx.Err() == nil {
		r.done(ctx)
	}
}
