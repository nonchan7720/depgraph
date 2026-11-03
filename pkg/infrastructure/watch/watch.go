// Package watch watches a repository for file changes and delivers debounced
// batches of changed paths. AI agents often rewrite dozens of files at once,
// so changes are coalesced before analysis.
package watch

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver"
)

// Run blocks until ctx is done. fn receives repo-relative, slash-separated
// paths that changed (created, written, removed, renamed) since the last call.
func Run(ctx context.Context, root string, supports func(rel string) bool, debounce time.Duration, fn func(changed []string)) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer func() { _ = w.Close() }()

	addDir := func(dir string) {
		_ = w.Add(dir)
	}
	// Register every directory recursively (fsnotify is not recursive).
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != root {
				rel, rerr := filepath.Rel(root, p)
				if rerr == nil && resolver.ShouldSkipDir(filepath.ToSlash(rel), d.Name()) {
					return filepath.SkipDir
				}
			}
			addDir(p)
		}
		return nil
	})

	pending := map[string]struct{}{}
	var timer *time.Timer
	var timerC <-chan time.Time

	flush := func() {
		if len(pending) == 0 {
			return
		}
		batch := make([]string, 0, len(pending))
		for p := range pending {
			batch = append(batch, p)
		}
		pending = map[string]struct{}{}
		fn(batch)
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			rel, err := filepath.Rel(root, ev.Name)
			if err != nil {
				continue
			}
			rel = filepath.ToSlash(rel)
			if resolver.ShouldSkipPath(rel) {
				continue
			}
			// New directory: start watching it.
			if ev.Has(fsnotify.Create) {
				if st, err := os.Stat(ev.Name); err == nil && st.IsDir() {
					if !resolver.ShouldSkipDir(rel, filepath.Base(ev.Name)) {
						addDir(ev.Name)
					}
					continue
				}
			}
			if !supports(rel) {
				continue
			}
			pending[rel] = struct{}{}
			if timer == nil {
				timer = time.NewTimer(debounce)
			} else {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(debounce)
			}
			timerC = timer.C
		case <-timerC:
			timerC = nil
			flush()
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			_ = err // non-fatal; keep watching
		}
	}
}
