package graph

import "context"

// Repository persists FileGraphs. Updates are per file: a file's subgraph is
// replaced as a whole, package-level IMPORTS edges are derived from file
// imports. The read side is view.Reader.
type Repository interface {
	Clear(ctx context.Context) error
	UpsertFile(ctx context.Context, fg *FileGraph) error
	UpsertRefs(ctx context.Context, path string, refs []Reference) error
	DeleteFile(ctx context.Context, relPath string) error
	DeleteFiles(ctx context.Context, paths []string) error
	FilePaths(ctx context.Context) ([]string, error)
	RebuildPackageImports(ctx context.Context, pkgKey string) error
	RebuildAllPackageImports(ctx context.Context) error
	PruneEmptyPackages(ctx context.Context) error
	PruneOrphanExternals(ctx context.Context) error
}
