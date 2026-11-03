package cmd

import (
	"context"
	"errors"
	"io/fs"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver"
	"github.com/nonchan7720/depgraph/pkg/interfaces/handler/hook"
)

// fileChecker is the hook.FileChecker of post-edit: it parses the edited file
// as it is on disk and reports its unresolved imports and the imports that
// close a cycle with the package graph already in the database. Nothing is
// written; watch / scan keep the database up to date.
type fileChecker struct {
	supports func(rel string) bool
	parse    func(ctx context.Context, root, rel string) (*graph.FileGraph, error)
	// importPath is the shortest import chain between two packages in the
	// database; nil when the database is not available (cycles are skipped).
	importPath func(ctx context.Context, from, to string) ([]string, error)
}

var _ hook.FileChecker = fileChecker{}

func (c fileChecker) CheckFile(ctx context.Context, root, rel string) ([]rules.Finding, error) {
	if resolver.ShouldSkipPath(rel) || !c.supports(rel) {
		return nil, nil
	}
	fg, err := c.parse(ctx, root, rel)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil // deleted
	}
	if err != nil {
		return nil, err
	}
	var out []rules.Finding
	seen := map[string]bool{}
	var targets []string
	for _, im := range fg.Imports {
		switch {
		case im.Unresolved:
			out = append(out, rules.UnresolvedImport(fg.Path, im.Spec, im.Reason))
		case !im.External && im.Target != fg.Package && !seen[im.Target]:
			seen[im.Target] = true
			targets = append(targets, im.Target)
		}
	}
	if c.importPath == nil {
		return out, nil
	}
	for _, t := range targets {
		// fg.Package -> t is the import just written; t -> ... -> fg.Package closes it
		back, err := c.importPath(ctx, t, fg.Package)
		if err != nil {
			return nil, err
		}
		if back != nil {
			out = append(out, rules.ImportCycle(append([]string{fg.Package}, back...)))
		}
	}
	return out, nil
}
