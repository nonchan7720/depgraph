// Package resolver defines the pluggable analysis backend interface.
//
// Backends:
//   - treesitter: zero-config structural analysis (packages, files, imports, declarations)
//   - lsp:        incremental, precise references/call edges via a running language server
//   - scip:       batch precise analysis from a SCIP index (planned)
package resolver

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
)

// ErrUnsupported is graph.ErrUnsupported (kept for callers of this package).
var ErrUnsupported = graph.ErrUnsupported

// Resolver is graph.Resolver.
type Resolver = graph.Resolver

// SkipDirs are directory names never descended into, wherever they appear.
var SkipDirs = map[string]bool{
	".git": true, "node_modules": true, "vendor": true, "dist": true, "build": true,
	".venv": true, "venv": true, "__pycache__": true, "target": true, ".next": true,
	".idea": true, ".vscode": true, "coverage": true, ".cache": true,
	".worktrees": true, ".claude": true,
}

// extraPatterns are user-supplied exclusions (see AddSkipPatterns).
var extraPatterns []string

// AddSkipPatterns registers additional exclusions. A pattern without a slash
// matches a directory or file name anywhere (glob, e.g. "*.gen.go", "tmp*");
// a pattern with a slash matches a repo-relative path prefix or glob
// (e.g. "worktrees", "examples/legacy", "internal/**/testdata").
// A leading slash anchors a single-segment pattern at the repo root ("/worktrees").
// A trailing slash is ignored.
func AddSkipPatterns(patterns []string) {
	for _, p := range patterns {
		p = strings.TrimSpace(strings.TrimSuffix(filepath.ToSlash(p), "/"))
		if p == "" || strings.HasPrefix(p, "#") {
			continue
		}
		extraPatterns = append(extraPatterns, p)
	}
}

// ResetSkipPatterns clears user-supplied exclusions (tests).
func ResetSkipPatterns() { extraPatterns = nil }

// IgnoreFilePath resolves the ignore file location: a relative file is taken
// relative to root (the --root directory), an absolute one is used as is.
// An empty file disables the ignore file and yields "". Patterns inside the
// file are always matched against paths relative to root.
func IgnoreFilePath(root, file string) string {
	if file == "" {
		return ""
	}
	if filepath.IsAbs(file) {
		return filepath.Clean(file)
	}
	return filepath.Join(root, file)
}

// LoadIgnoreFile reads patterns from an ignore file (one per line, # comments).
// A missing file is not an error.
func LoadIgnoreFile(path string) error {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	AddSkipPatterns(strings.Split(string(b), "\n"))
	return nil
}

func matchPattern(pat, rel, name string) bool {
	if after, ok := strings.CutPrefix(pat, "/"); ok { // leading slash: anchored at repo root
		return matchPath(after, rel)
	}
	if strings.Contains(pat, "/") {
		return matchPath(pat, rel)
	}
	ok, _ := path.Match(pat, name)
	return ok
}

func matchPath(pat, rel string) bool {
	{
		rel = strings.TrimPrefix(rel, "./")
		if rel == pat || strings.HasPrefix(rel, pat+"/") {
			return true
		}
		if ok, _ := path.Match(pat, rel); ok {
			return true
		}
		// "a/**/b" style: match any depth
		if strings.Contains(pat, "**") {
			re := globToRegexp(pat)
			return re.MatchString(rel)
		}
		return false
	}
}

func globToRegexp(pat string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pat); i++ {
		switch {
		case strings.HasPrefix(pat[i:], "**/"):
			b.WriteString("(?:.*/)?")
			i += 2
		case strings.HasPrefix(pat[i:], "**"):
			b.WriteString(".*")
			i++
		case pat[i] == '*':
			b.WriteString("[^/]*")
		case pat[i] == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(pat[i])))
		}
	}
	b.WriteString("(?:/.*)?$")
	return regexp.MustCompile(b.String())
}

// ShouldSkipDir reports whether a directory (name, repo-relative path) is excluded.
func ShouldSkipDir(rel, name string) bool {
	if SkipDirs[name] {
		return true
	}
	rel = filepath.ToSlash(rel)
	for _, p := range extraPatterns {
		if matchPattern(p, rel, name) {
			return true
		}
	}
	return false
}

// ShouldSkipPath reports whether a repo-relative file path falls under an
// excluded directory or matches a file pattern.
func ShouldSkipPath(rel string) bool {
	rel = filepath.ToSlash(rel)
	parts := strings.Split(rel, "/")
	cur := ""
	for i, part := range parts {
		if cur == "" {
			cur = part
		} else {
			cur = cur + "/" + part
		}
		if i < len(parts)-1 {
			if ShouldSkipDir(cur, part) {
				return true
			}
			continue
		}
		// last component is the file itself: name/path patterns still apply
		for _, p := range extraPatterns {
			if matchPattern(p, cur, part) {
				return true
			}
		}
	}
	return false
}

// ScanRepo walks root and calls fn for every supported file, parsing with
// GOMAXPROCS workers. See ScanRepoWorkers.
func ScanRepo(ctx context.Context, r Resolver, root string, fn func(fg *graph.FileGraph) error) error {
	return scanRepo(ctx, r, root, 0, fn)
}

// ScanRepoWorkers returns ScanRepo with n parse workers (n <= 0: GOMAXPROCS).
//
// Files are parsed concurrently, but fn is called from the calling goroutine
// only, one file at a time, in walk (lexical path) order, so the output does
// not depend on the number of workers. The first error in that order (from
// the resolver or from fn) aborts the scan and is returned; a cancelled ctx
// stops it with ctx.Err(). No parse is running when it returns.
func ScanRepoWorkers(n int) func(ctx context.Context, r Resolver, root string, fn func(fg *graph.FileGraph) error) error {
	return func(ctx context.Context, r Resolver, root string, fn func(fg *graph.FileGraph) error) error {
		return scanRepo(ctx, r, root, n, fn)
	}
}

type scanResult struct {
	fg  *graph.FileGraph
	err error
}

func scanRepo(ctx context.Context, r Resolver, root string, workers int, fn func(fg *graph.FileGraph) error) error {
	if workers <= 0 {
		workers = runtime.GOMAXPROCS(0)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type job struct {
		rel string
		out chan scanResult // buffered (1): a worker never blocks on it
	}
	jobs := make(chan job)
	// one result channel per file, in walk order; the buffer bounds how far
	// parsing may run ahead of fn
	order := make(chan chan scanResult, 2*workers)

	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for j := range jobs {
				fg, err := r.ResolveFile(ctx, root, j.rel)
				j.out <- scanResult{fg, err}
			}
		})
	}

	walkErr := make(chan error, 1)
	go func() {
		defer close(order)
		defer close(jobs)
		walkErr <- walkRepo(ctx, r, root, func(rel string) error {
			out := make(chan scanResult, 1)
			select {
			case order <- out:
			case <-ctx.Done():
				return ctx.Err()
			}
			select {
			case jobs <- job{rel, out}:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()

	err := deliver(ctx, order, fn)
	cancel()
	for range order { // let the walker finish
	}
	wg.Wait()
	if err == nil {
		err = <-walkErr
	}
	return err
}

// deliver calls fn for each result in order until the results run out, an
// error occurs or ctx is cancelled.
func deliver(ctx context.Context, order <-chan chan scanResult, fn func(*graph.FileGraph) error) error {
	for out := range order {
		var res scanResult
		select {
		case res = <-out:
		case <-ctx.Done():
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if res.err != nil {
			if errors.Is(res.err, ErrUnsupported) {
				continue
			}
			return res.err
		}
		if err := fn(res.fg); err != nil {
			return err
		}
	}
	return nil
}

// walkRepo calls emit with the repo-relative path of every supported,
// non-ignored file under root, in lexical order.
func walkRepo(ctx context.Context, r Resolver, root string, emit func(rel string) error) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped, not fatal
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if p != root && ShouldSkipDir(rel, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if ShouldSkipPath(rel) || !r.Supports(rel) {
			return nil
		}
		return emit(rel)
	})
}
