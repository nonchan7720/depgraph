package graph

import (
	"context"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
)

// graph.Overview returns all packages and package-level import edges.
func (r *Graph) Overview(ctx context.Context) (*graph.Overview, error) {
	rows, err := r.query(ctx, `
		MATCH (p:Package {project: $project})
		RETURN p.key AS key, p.name AS name, coalesce(p.external, false) AS external,
		  count { (p)-[:CONTAINS]->(:File) } AS files,
		  count { (p)-[:CONTAINS]->(:File)-[:DECLARES]->(:Symbol) } AS symbols,
		  count { (p)<-[:IMPORTS]-(:Package) } AS fanIn,
		  count { (p)-[:IMPORTS]->(:Package) } AS fanOut
		ORDER BY key
	`, nil)
	if err != nil {
		return nil, err
	}
	ov := &graph.Overview{Packages: []graph.PackageNode{}, Edges: []graph.Edge{}}
	for _, row := range rows {
		ov.Packages = append(ov.Packages, graph.PackageNode{
			Key: str(row["key"]), Name: str(row["name"]), External: row["external"].(bool),
			Files: i64(row["files"]), Symbols: i64(row["symbols"]),
			FanIn: i64(row["fanIn"]), FanOut: i64(row["fanOut"]),
		})
	}
	rows, err = r.query(ctx, `
		MATCH (a:Package {project: $project})-[:IMPORTS]->(b:Package)
		RETURN a.key AS from, b.key AS to
	`, nil)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		ov.Edges = append(ov.Edges, graph.Edge{From: str(row["from"]), To: str(row["to"])})
	}
	return ov, nil
}

// graph.PackageDetail returns files/symbols for one package.
func (r *Graph) PackageDetail(ctx context.Context, key string) (*graph.PackageDetail, error) {
	d := &graph.PackageDetail{Key: key, Files: []graph.FileDetail{}, ImportedBy: []string{}, Imports: []string{}}
	rows, err := r.query(ctx, `
		MATCH (p:Package {project: $project, key: $key})-[:CONTAINS]->(f:File)
		OPTIONAL MATCH (f)-[:DECLARES]->(s:Symbol)
		WITH f, s ORDER BY s.line
		WITH f, collect(CASE WHEN s IS NULL THEN null ELSE {name: s.name, kind: s.kind, exported: s.exported, line: s.line, endLine: s.endLine} END) AS syms
		OPTIONAL MATCH (f)-[:IMPORTS]->(q:Package)
		RETURN f.path AS path, f.language AS language, f.lines AS lines, syms,
		       collect(DISTINCT q.key) AS imports
		ORDER BY path
	`, map[string]any{"key": key})
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		fd := graph.FileDetail{Path: str(row["path"]), Language: str(row["language"]), Lines: i64(row["lines"]), Symbols: []graph.Symbol{}, Imports: []string{}}
		if syms, ok := row["syms"].([]any); ok {
			for _, x := range syms {
				m, ok := x.(map[string]any)
				if !ok {
					continue
				}
				fd.Symbols = append(fd.Symbols, graph.Symbol{
					Name: str(m["name"]), Kind: str(m["kind"]), Exported: m["exported"] == true,
					Line: int(i64(m["line"])), EndLine: int(i64(m["endLine"])),
				})
			}
		}
		if ims, ok := row["imports"].([]any); ok {
			for _, x := range ims {
				fd.Imports = append(fd.Imports, str(x))
			}
		}
		d.Files = append(d.Files, fd)
	}
	rows, err = r.query(ctx, `
		MATCH (p:Package {project: $project, key: $key})
		RETURN [(p)<-[:IMPORTS]-(a:Package) | a.key] AS importedBy, [(p)-[:IMPORTS]->(b:Package) | b.key] AS imports
	`, map[string]any{"key": key})
	if err != nil {
		return nil, err
	}
	if len(rows) > 0 {
		for _, x := range rows[0]["importedBy"].([]any) {
			d.ImportedBy = append(d.ImportedBy, str(x))
		}
		for _, x := range rows[0]["imports"].([]any) {
			d.Imports = append(d.Imports, str(x))
		}
	}
	return d, nil
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func i64(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case float64:
		return int64(x)
	}
	return 0
}
