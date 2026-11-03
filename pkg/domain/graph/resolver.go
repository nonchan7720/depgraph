package graph

import (
	"context"
	"errors"
)

// ErrUnsupported is returned for files a resolver does not handle.
var ErrUnsupported = errors.New("resolver: unsupported file")

// Resolver turns a single source file into a FileGraph (tree-sitter today;
// LSP / SCIP backends refine it).
type Resolver interface {
	Name() string
	// Supports reports whether rel (repo-relative path) is handled by this resolver.
	Supports(rel string) bool
	// ResolveFile parses one file. root is the absolute repo root.
	ResolveFile(ctx context.Context, root, rel string) (*FileGraph, error)
}
