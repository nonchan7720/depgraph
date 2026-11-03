package lsp

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"sync"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
)

// Server is the subset of Client the Enricher needs. *Client implements it;
// tests substitute a fake.
type Server interface {
	DidOpen(rel string, lang graph.Language) error
	DidClose(rel string) error
	PrepareCallHierarchy(ctx context.Context, rel string, line, col int) ([]CallHierarchyItem, error)
	OutgoingCalls(ctx context.Context, item CallHierarchyItem) ([]OutgoingCall, error)
	IncomingCalls(ctx context.Context, item CallHierarchyItem) ([]IncomingCall, error)
	References(ctx context.Context, rel string, line, col int) ([]Location, error)
}

// SymbolsFunc returns the top-level symbols of a repo-relative file (normally
// by running tree-sitter). It returns nil for files that are not part of the
// graph (ignored, unsupported); such targets are dropped.
type SymbolsFunc func(ctx context.Context, rel string) ([]graph.Symbol, error)

// Enricher fills FileGraph.Refs for one file using a language server.
//
// For every function/method it asks for outgoing and incoming calls (CALLS).
// For every type-like symbol it asks for references and records the enclosing
// symbol of each use (REFERENCES). LSP locations (uri, line) are mapped back to
// (repo-relative file, symbol name) with Symbols; anything outside the
// repository (standard library, dependencies) is discarded.
type Enricher struct {
	Server  Server
	Root    string // absolute repo root
	Symbols SymbolsFunc

	rootsOnce sync.Once
	roots     []string // Root and its symlink-resolved form
}

func isCallable(kind string) bool { return kind == "function" || kind == "method" }

func isTypeLike(kind string) bool {
	switch kind {
	case "struct", "interface", "type", "class", "enum":
		return true
	}
	return false
}

// enrichRun is the per-call state (symbol cache and edge de-duplication).
type enrichRun struct {
	e     *Enricher
	fg    *graph.FileGraph
	cache map[string][]graph.Symbol
	seen  map[graph.Reference]struct{}
}

func (r *enrichRun) add(kind, fromFile, fromName, toFile, toName string) {
	ref := graph.Reference{Kind: kind, FromFile: fromFile, FromName: fromName, ToFile: toFile, ToName: toName}
	if _, ok := r.seen[ref]; ok {
		return
	}
	r.seen[ref] = struct{}{}
	r.fg.Refs = append(r.fg.Refs, ref)
}

// locate maps an LSP uri and 0-based line to (rel, enclosing symbol name).
func (r *enrichRun) locate(ctx context.Context, uri string, line0 int) (rel, name string, ok bool) {
	rel, ok = r.e.uriToRel(uri)
	if !ok {
		return "", "", false
	}
	syms, cached := r.cache[rel]
	if !cached {
		if rel == r.fg.Path {
			syms = r.fg.Symbols
		} else if s, err := r.e.Symbols(ctx, rel); err == nil {
			syms = s
		}
		r.cache[rel] = syms
	}
	line := line0 + 1
	best := -1
	for i, s := range syms {
		if line < s.Line || line > s.EndLine {
			continue
		}
		if best < 0 || s.EndLine-s.Line < syms[best].EndLine-syms[best].Line {
			best = i
		}
	}
	if best < 0 {
		return "", "", false
	}
	return rel, syms[best].Name, true
}

// Enrich replaces fg.Refs with LSP-derived references. Failures for individual
// symbols are collected and returned together; refs found for the others are
// still kept.
func (e *Enricher) Enrich(ctx context.Context, fg *graph.FileGraph) error {
	var targets []graph.Symbol
	for _, s := range fg.Symbols {
		if s.NameLine > 0 && (isCallable(s.Kind) || isTypeLike(s.Kind)) {
			targets = append(targets, s)
		}
	}
	fg.Refs = nil
	if len(targets) == 0 {
		return nil
	}
	if err := e.Server.DidOpen(fg.Path, fg.Language); err != nil {
		return fmt.Errorf("lsp didOpen %s: %w", fg.Path, err)
	}
	defer func() { _ = e.Server.DidClose(fg.Path) }()

	run := &enrichRun{e: e, fg: fg, cache: map[string][]graph.Symbol{}, seen: map[graph.Reference]struct{}{}}
	var errs []error
	for _, s := range targets {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		var err error
		if isCallable(s.Kind) {
			err = run.calls(ctx, s)
		} else {
			err = run.typeRefs(ctx, s)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("%s %s: %w", fg.Path, s.Name, err))
		}
	}
	return errors.Join(errs...)
}

func (r *enrichRun) calls(ctx context.Context, s graph.Symbol) error {
	items, err := r.e.Server.PrepareCallHierarchy(ctx, r.fg.Path, s.NameLine-1, s.Col)
	if err != nil {
		return err
	}
	var errs []error
	for _, it := range items {
		out, err := r.e.Server.OutgoingCalls(ctx, it)
		if err != nil {
			errs = append(errs, err)
		}
		for _, c := range out {
			if toFile, toName, ok := r.locate(ctx, c.To.URI, c.To.SelectionRange.Start.Line); ok {
				r.add("CALLS", r.fg.Path, s.Name, toFile, toName)
			}
		}
		in, err := r.e.Server.IncomingCalls(ctx, it)
		if err != nil {
			errs = append(errs, err)
		}
		for _, c := range in {
			if fromFile, fromName, ok := r.locate(ctx, c.From.URI, c.From.SelectionRange.Start.Line); ok {
				r.add("CALLS", fromFile, fromName, r.fg.Path, s.Name)
			}
		}
	}
	return errors.Join(errs...)
}

func (r *enrichRun) typeRefs(ctx context.Context, s graph.Symbol) error {
	locs, err := r.e.Server.References(ctx, r.fg.Path, s.NameLine-1, s.Col)
	if err != nil {
		return err
	}
	for _, l := range locs {
		file, name, ok := r.locate(ctx, l.URI, l.Range.Start.Line)
		if !ok || (file == r.fg.Path && name == s.Name) {
			continue // outside the repo, unknown, or inside its own declaration
		}
		r.add("REFERENCES", file, name, r.fg.Path, s.Name)
	}
	return nil
}

// uriToRel converts a file:// URI into a repo-relative slash path. URIs outside
// the repository (or not file URIs) report ok=false.
func (e *Enricher) uriToRel(uri string) (string, bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return "", false
	}
	p := filepath.Clean(filepath.FromSlash(u.Path))
	e.rootsOnce.Do(func() {
		e.roots = []string{filepath.Clean(e.Root)}
		if real, err := filepath.EvalSymlinks(e.Root); err == nil && real != e.roots[0] {
			e.roots = append(e.roots, real)
		}
	})
	for _, root := range e.roots {
		rel, err := filepath.Rel(root, p)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		return filepath.ToSlash(rel), true
	}
	return "", false
}
