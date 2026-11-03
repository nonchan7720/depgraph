package resolver

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
)

// fakeResolver handles *.go files; resolve decides what each call does.
type fakeResolver struct {
	resolve func(ctx context.Context, rel string) (*graph.FileGraph, error)
}

func (f *fakeResolver) Name() string { return "fake" }

func (f *fakeResolver) Supports(rel string) bool { return strings.HasSuffix(rel, ".go") }

func (f *fakeResolver) ResolveFile(ctx context.Context, _ string, rel string) (*graph.FileGraph, error) {
	return f.resolve(ctx, rel)
}

// scanTree creates n Go files spread over nested directories plus a few
// unsupported files, and returns the root and the supported paths in walk order.
func scanTree(t *testing.T, n int) (string, []string) {
	t.Helper()
	root := t.TempDir()
	var rels []string
	for i := range n {
		rel := fmt.Sprintf("d%d/s%d/f%03d.go", i%3, i%5, i)
		rels = append(rels, rel)
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "README.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	sort.Strings(rels) // filepath.WalkDir visits in lexical order
	return root, rels
}

func scanWorkers(t *testing.T, workers int, ctx context.Context, r Resolver, root string, fn func(*graph.FileGraph) error) error {
	t.Helper()
	return ScanRepoWorkers(workers)(ctx, r, root, fn)
}

// fn is called in walk (path) order regardless of how long each parse takes,
// and only from one goroutine at a time.
func TestScanRepoOrderIsDeterministic(t *testing.T) {
	ResetSkipPatterns()
	root, want := scanTree(t, 60)
	r := &fakeResolver{resolve: func(_ context.Context, rel string) (*graph.FileGraph, error) {
		// later files finish first
		var i int
		_, _ = fmt.Sscanf(filepath.Base(rel), "f%03d.go", &i)
		time.Sleep(time.Duration(60-i) * 100 * time.Microsecond)
		return &graph.FileGraph{Path: rel}, nil
	}}
	for _, workers := range []int{1, 4, 16} {
		var got []string
		var inFn atomic.Int32
		err := scanWorkers(t, workers, context.Background(), r, root, func(fg *graph.FileGraph) error {
			if inFn.Add(1) != 1 {
				t.Error("fn called concurrently")
			}
			defer inFn.Add(-1)
			got = append(got, fg.Path)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, want) {
			t.Errorf("workers=%d: order\n got %v\nwant %v", workers, got, want)
		}
	}
	// ScanRepo (default workers) gives the same order
	var got []string
	if err := ScanRepo(context.Background(), r, root, func(fg *graph.FileGraph) error {
		got = append(got, fg.Path)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Errorf("ScanRepo order\n got %v\nwant %v", got, want)
	}
}

// With workers > 1, files are actually parsed concurrently.
func TestScanRepoParsesInParallel(t *testing.T) {
	ResetSkipPatterns()
	root, _ := scanTree(t, 8)
	const workers = 4
	var mu sync.Mutex
	inFlight, peak := 0, 0
	arrived := make(chan struct{})
	var once sync.Once
	r := &fakeResolver{resolve: func(_ context.Context, rel string) (*graph.FileGraph, error) {
		mu.Lock()
		inFlight++
		if inFlight > peak {
			peak = inFlight
		}
		if inFlight == workers {
			once.Do(func() { close(arrived) })
		}
		mu.Unlock()
		select { // wait until all workers are busy (or give up)
		case <-arrived:
		case <-time.After(2 * time.Second):
		}
		mu.Lock()
		inFlight--
		mu.Unlock()
		return &graph.FileGraph{Path: rel}, nil
	}}
	if err := scanWorkers(t, workers, context.Background(), r, root, func(*graph.FileGraph) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if peak != workers {
		t.Errorf("peak concurrent parses = %d, want %d", peak, workers)
	}
}

// Cancelling ctx stops the scan promptly with ctx.Err(), and no parse is
// still running when ScanRepo returns.
func TestScanRepoCancel(t *testing.T) {
	ResetSkipPatterns()
	root, _ := scanTree(t, 200)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var running, calls atomic.Int32
	r := &fakeResolver{resolve: func(ctx context.Context, rel string) (*graph.FileGraph, error) {
		running.Add(1)
		defer running.Add(-1)
		calls.Add(1)
		select { // a slow parse that honours ctx
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
		return &graph.FileGraph{Path: rel}, nil
	}}
	delivered := 0
	start := time.Now()
	err := scanWorkers(t, 4, ctx, r, root, func(*graph.FileGraph) error {
		delivered++
		if delivered == 2 {
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("cancel took %v", d)
	}
	if n := running.Load(); n != 0 {
		t.Errorf("%d parses still running after return", n)
	}
	if delivered != 2 {
		t.Errorf("fn called %d times after cancel, want 2", delivered)
	}
	if n := calls.Load(); n >= 200 {
		t.Errorf("all %d files were parsed despite cancel", n)
	}
}

// A parse error aborts the scan with that error: files before it (in path
// order) are delivered, nothing after it is.
func TestScanRepoResolveError(t *testing.T) {
	ResetSkipPatterns()
	root, rels := scanTree(t, 30)
	bad := rels[10]
	boom := errors.New("boom")
	r := &fakeResolver{resolve: func(_ context.Context, rel string) (*graph.FileGraph, error) {
		if rel == bad {
			return nil, boom
		}
		return &graph.FileGraph{Path: rel}, nil
	}}
	for _, workers := range []int{1, 8} {
		var got []string
		err := scanWorkers(t, workers, context.Background(), r, root, func(fg *graph.FileGraph) error {
			got = append(got, fg.Path)
			return nil
		})
		if !errors.Is(err, boom) {
			t.Fatalf("workers=%d: err = %v, want boom", workers, err)
		}
		if !slices.Equal(got, rels[:10]) {
			t.Errorf("workers=%d: delivered %v, want %v", workers, got, rels[:10])
		}
	}
}

// An error from fn aborts the scan with that error and fn is not called again.
func TestScanRepoCallbackError(t *testing.T) {
	ResetSkipPatterns()
	root, rels := scanTree(t, 30)
	stop := errors.New("stop")
	r := &fakeResolver{resolve: func(_ context.Context, rel string) (*graph.FileGraph, error) {
		return &graph.FileGraph{Path: rel}, nil
	}}
	var got []string
	err := scanWorkers(t, 8, context.Background(), r, root, func(fg *graph.FileGraph) error {
		got = append(got, fg.Path)
		if fg.Path == rels[5] {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) {
		t.Fatalf("err = %v, want stop", err)
	}
	if !slices.Equal(got, rels[:6]) {
		t.Errorf("delivered %v, want %v", got, rels[:6])
	}
}

// ErrUnsupported from the resolver skips the file, as before.
func TestScanRepoSkipsUnsupported(t *testing.T) {
	ResetSkipPatterns()
	root, rels := scanTree(t, 10)
	r := &fakeResolver{resolve: func(_ context.Context, rel string) (*graph.FileGraph, error) {
		if rel == rels[3] {
			return nil, fmt.Errorf("wrapped: %w", ErrUnsupported)
		}
		return &graph.FileGraph{Path: rel}, nil
	}}
	var got []string
	if err := scanWorkers(t, 4, context.Background(), r, root, func(fg *graph.FileGraph) error {
		got = append(got, fg.Path)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := slices.Delete(slices.Clone(rels), 3, 4)
	if !slices.Equal(got, want) {
		t.Errorf("delivered %v, want %v", got, want)
	}
}
