package graph

import (
	"context"
)

// FilePaths returns every stored file path.
func (r *Graph) FilePaths(ctx context.Context) ([]string, error) {
	rows, err := r.query(ctx, `MATCH (f:File {project: $project}) RETURN f.path AS path`, nil)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, str(row["path"]))
	}
	return out, nil
}

// DeleteFiles removes files and their symbols in one statement.
func (r *Graph) DeleteFiles(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	return r.run(ctx, `
		UNWIND $paths AS path
		MATCH (f:File {project: $project, path: path})
		OPTIONAL MATCH (f)-[:DECLARES]->(sym:Symbol)
		DETACH DELETE sym, f
	`, map[string]any{"paths": paths})
}

// PruneOrphanExternals removes external packages nothing imports any more.
func (r *Graph) PruneOrphanExternals(ctx context.Context) error {
	return r.run(ctx, `
		MATCH (p:Package {project: $project})
		WHERE p.external = true AND NOT ()-[:IMPORTS]->(p)
		DETACH DELETE p
	`, nil)
}
