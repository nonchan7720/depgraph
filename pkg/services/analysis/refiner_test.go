package analysis

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
)

type refinerHarness struct {
	r *refiner

	mu       sync.Mutex
	upserted map[string][]graph.Reference
	notified [][]string
	doneN    int
	doneCh   chan struct{}
	enriched []string
	onEnrich func(rel string) // enrich 中に呼ばれるフック
}

func newHarness(t *testing.T, root string) *refinerHarness {
	t.Helper()
	h := &refinerHarness{upserted: map[string][]graph.Reference{}, doneCh: make(chan struct{}, 10)}
	var gmu sync.Mutex
	h.r = &refiner{
		root: root,
		mu:   &gmu,
		resolve: func(_ context.Context, rel string) (*graph.FileGraph, error) {
			return &graph.FileGraph{Path: rel, Language: graph.Go}, nil
		},
		enrich: func(_ context.Context, fg *graph.FileGraph) error {
			h.mu.Lock()
			h.enriched = append(h.enriched, fg.Path)
			hook := h.onEnrich
			h.mu.Unlock()
			if hook != nil {
				hook(fg.Path)
			}
			fg.Refs = []graph.Reference{{Kind: "CALLS", FromFile: fg.Path, FromName: "A", ToFile: fg.Path, ToName: "B"}}
			return nil
		},
		upsert: func(_ context.Context, path string, refs []graph.Reference) error {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.upserted[path] = refs
			return nil
		},
		notify: func(files []string) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.notified = append(h.notified, files)
		},
		done: func(context.Context) {
			h.mu.Lock()
			h.doneN++
			h.mu.Unlock()
			h.doneCh <- struct{}{}
		},
		warn: func(string, ...any) {},
	}
	return h
}

func (h *refinerHarness) start(t *testing.T) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	fin := make(chan struct{})
	go func() { h.r.Run(ctx); close(fin) }()
	return func() { cancel(); <-fin }
}

func (h *refinerHarness) waitDone(t *testing.T) {
	t.Helper()
	select {
	case <-h.doneCh:
	case <-time.After(3 * time.Second):
		t.Fatal("refiner did not finish")
	}
}

func touch(t *testing.T, root, rel string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.WriteFile(p, []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRefinerEnrichesUpsertsAndNotifies(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "a.go")
	h := newHarness(t, root)
	stop := h.start(t)
	defer stop()

	h.r.Submit([]string{"a.go", "gone.go"}) // gone.go は存在しない
	h.waitDone(t)

	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.upserted["a.go"]) != 1 {
		t.Errorf("upserted = %v", h.upserted)
	}
	if _, ok := h.upserted["gone.go"]; ok {
		t.Error("deleted file must not be upserted")
	}
	if len(h.notified) != 1 {
		t.Fatalf("notified = %v", h.notified)
	}
	got := append([]string(nil), h.notified[0]...)
	sort.Strings(got)
	if len(got) != 2 {
		t.Errorf("notified files = %v", got)
	}
}

// enrich 中に同じファイルが再 Submit されたら、古い内容の結果は捨てて再処理する。
func TestRefinerDropsStaleResults(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "a.go")
	h := newHarness(t, root)
	var once sync.Once
	h.onEnrich = func(string) { once.Do(func() { h.r.Submit([]string{"a.go"}) }) }
	stop := h.start(t)
	defer stop()

	h.r.Submit([]string{"a.go"})
	h.waitDone(t) // 1 回目: 結果は破棄されるので done は呼ばれない想定 -> 2 回目の完了を待つ
	h.mu.Lock()
	n := len(h.enriched)
	h.mu.Unlock()
	if n != 2 {
		t.Errorf("enrich should run twice (stale + retry), got %d", n)
	}
}

func TestRefinerEnrichErrorIsWarnedAndSkipped(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "a.go")
	h := newHarness(t, root)
	warned := 0
	h.r.warn = func(string, ...any) { warned++ }
	h.r.enrich = func(_ context.Context, fg *graph.FileGraph) error {
		fg.Refs = []graph.Reference{{Kind: "CALLS"}}
		return errors.New("partial")
	}
	stop := h.start(t)
	defer stop()
	h.r.Submit([]string{"a.go"})
	h.waitDone(t)
	h.mu.Lock()
	defer h.mu.Unlock()
	if warned != 1 {
		t.Errorf("warned = %d", warned)
	}
	// 部分失敗でも取れた refs は保存する
	if len(h.upserted["a.go"]) != 1 {
		t.Errorf("partial refs should still be stored: %v", h.upserted)
	}
}

func TestRefinerStopsOnCancel(t *testing.T) {
	h := newHarness(t, t.TempDir())
	stop := h.start(t)
	stop() // ハングしなければ OK
}
