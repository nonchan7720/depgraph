// Package graph persists FileGraphs into Neo4j and runs read queries.
// Store is the shared connection; Graph is one project's graph.
package graph

import (
	"context"
	"fmt"
	"path"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
)

// Store wraps a Neo4j driver.
type Store struct {
	driver neo4j.DriverWithContext
	db     string
}

// New connects and verifies connectivity.
func New(ctx context.Context, uri, user, pass, database string) (*Store, error) {
	d, err := neo4j.NewDriverWithContext(uri, neo4j.BasicAuth(user, pass, ""))
	if err != nil {
		return nil, err
	}
	if err := d.VerifyConnectivity(ctx); err != nil {
		return nil, fmt.Errorf("neo4j connect %s: %w", uri, err)
	}
	return &Store{driver: d, db: database}, nil
}

// Close releases the driver.
func (s *Store) Close(ctx context.Context) error { return s.driver.Close(ctx) }

// Exec runs a write statement.
func (s *Store) Exec(ctx context.Context, cypher string, params map[string]any) error {
	_, err := s.write(ctx, cypher, params)
	return err
}

func (s *Store) write(ctx context.Context, cypher string, params map[string]any) ([]map[string]any, error) {
	res, err := neo4j.ExecuteQuery(ctx, s.driver, cypher, params, neo4j.EagerResultTransformer,
		neo4j.ExecuteQueryWithDatabase(s.db))
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(res.Records))
	for _, rec := range res.Records {
		out = append(out, rec.AsMap())
	}
	return out, nil
}

// Query runs a read query and returns rows as maps.
func (s *Store) Query(ctx context.Context, cypher string, params map[string]any) ([]map[string]any, error) {
	res, err := neo4j.ExecuteQuery(ctx, s.driver, cypher, params, neo4j.EagerResultTransformer,
		neo4j.ExecuteQueryWithDatabase(s.db), neo4j.ExecuteQueryWithReadersRouting())
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(res.Records))
	for _, rec := range res.Records {
		out = append(out, rec.AsMap())
	}
	return out, nil
}

// EnsureSchema creates constraints/indexes (idempotent). Graph nodes are
// unique per project; the single-property constraints of the one-project
// layout are dropped so two projects can hold the same package key / path.
func (s *Store) EnsureSchema(ctx context.Context) error {
	stmts := []string{
		`DROP CONSTRAINT package_key IF EXISTS`,
		`DROP CONSTRAINT file_path IF EXISTS`,
		`DROP INDEX symbol_file_name IF EXISTS`,
		`CREATE CONSTRAINT project_key IF NOT EXISTS FOR (p:Project) REQUIRE p.key IS UNIQUE`,
		`CREATE CONSTRAINT package_project_key IF NOT EXISTS FOR (p:Package) REQUIRE (p.project, p.key) IS UNIQUE`,
		`CREATE CONSTRAINT file_project_path IF NOT EXISTS FOR (f:File) REQUIRE (f.project, f.path) IS UNIQUE`,
		`CREATE INDEX package_project IF NOT EXISTS FOR (p:Package) ON (p.project)`,
		`CREATE INDEX file_project IF NOT EXISTS FOR (f:File) ON (f.project)`,
		`CREATE INDEX symbol_project IF NOT EXISTS FOR (s:Symbol) ON (s.project)`,
		`CREATE INDEX symbol_project_file_name IF NOT EXISTS FOR (s:Symbol) ON (s.project, s.file, s.name)`,
		`CREATE INDEX symbol_name IF NOT EXISTS FOR (s:Symbol) ON (s.name)`,
		`CREATE INDEX symbol_kind IF NOT EXISTS FOR (s:Symbol) ON (s.kind)`,
	}
	for _, st := range stmts {
		// several depgraph processes (or test packages) may start on an empty
		// database at once; losing that race is not an error.
		if err := s.Exec(ctx, st, nil); err != nil && !isSchemaRace(st, err) {
			return err
		}
	}
	return nil
}

// Graph is the graph of one project (graph.Repository). Every query is
// scoped by the project key; the Store connection is shared.
type Graph struct {
	st      *Store
	project string
}

var _ graph.Repository = (*Graph)(nil)

// ForProject binds the store to the project key.
func ForProject(st *Store, project string) *Graph {
	return &Graph{st: st, project: project}
}

// Project returns the key the repository is bound to.
func (r *Graph) Project() string { return r.project }

// params adds $project to p.
func (r *Graph) params(p map[string]any) map[string]any {
	out := map[string]any{"project": r.project}
	for k, v := range p {
		out[k] = v
	}
	return out
}

func (r *Graph) run(ctx context.Context, cypher string, p map[string]any) error {
	return r.st.Exec(ctx, cypher, r.params(p))
}

func (r *Graph) query(ctx context.Context, cypher string, p map[string]any) ([]map[string]any, error) {
	return r.st.Query(ctx, cypher, r.params(p))
}

// Clear deletes the project's graph data (the Project node is kept).
func (r *Graph) Clear(ctx context.Context) error {
	return r.run(ctx, `
		MATCH (n) WHERE (n:Package OR n:File OR n:Symbol) AND n.project = $project
		DETACH DELETE n
	`, nil)
}

// UpsertFile replaces a file's subgraph (file node, its symbols, its imports)
// in one transaction. Package-level IMPORTS edges are rebuilt separately via
// RebuildPackageImports. External packages (ext:...) belong to the project too.
func (r *Graph) UpsertFile(ctx context.Context, fg *graph.FileGraph) error {
	symbols := make([]map[string]any, 0, len(fg.Symbols))
	for _, sym := range fg.Symbols {
		symbols = append(symbols, map[string]any{
			"name": sym.Name, "kind": sym.Kind, "exported": sym.Exported,
			"line": sym.Line, "endLine": sym.EndLine,
		})
	}
	imports := make([]map[string]any, 0, len(fg.Imports))
	for _, im := range fg.Imports {
		imports = append(imports, map[string]any{
			"spec": im.Spec, "target": im.Target, "external": im.External,
		})
	}

	session := r.st.driver.NewSession(ctx, neo4j.SessionConfig{DatabaseName: r.st.db})
	defer func() { _ = session.Close(ctx) }()

	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		// 1. package + file, and clear old symbols / file-level imports
		if _, err := tx.Run(ctx, `
			MERGE (p:Package {project: $project, key: $pkg})
			  ON CREATE SET p.name = $pkgName, p.external = false
			  SET p.external = false
			MERGE (f:File {project: $project, path: $path})
			  SET f.language = $lang, f.lines = $lines, f.updatedAt = datetime()
			MERGE (p)-[:CONTAINS]->(f)
			WITH f
			OPTIONAL MATCH (f)-[:DECLARES]->(old:Symbol)
			DETACH DELETE old
			WITH DISTINCT f
			OPTIONAL MATCH (f)-[r:IMPORTS]->()
			DELETE r
		`, r.params(map[string]any{
			"pkg": fg.Package, "pkgName": path.Base(fg.Package),
			"path": fg.Path, "lang": string(fg.Language), "lines": fg.Lines,
		})); err != nil {
			return nil, err
		}

		// 2. symbols
		if len(symbols) > 0 {
			if _, err := tx.Run(ctx, `
				MATCH (f:File {project: $project, path: $path})
				UNWIND $symbols AS s
				CREATE (sym:Symbol {
					project: $project, name: s.name, kind: s.kind, exported: s.exported,
					line: s.line, endLine: s.endLine, language: $lang, file: $path
				})
				CREATE (f)-[:DECLARES]->(sym)
			`, r.params(map[string]any{"path": fg.Path, "symbols": symbols, "lang": string(fg.Language)})); err != nil {
				return nil, err
			}
		}

		// 3. imports
		if len(imports) > 0 {
			if _, err := tx.Run(ctx, `
				MATCH (f:File {project: $project, path: $path})
				UNWIND $imports AS i
				MERGE (q:Package {project: $project, key: i.target})
				  ON CREATE SET q.name = i.target, q.external = i.external
				MERGE (f)-[r:IMPORTS]->(q)
				  SET r.spec = i.spec
			`, r.params(map[string]any{"path": fg.Path, "imports": imports})); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	return err
}

// UpsertRefs stores symbol-level edges (CALLS / REFERENCES) found by a precise
// resolver for the file at path, in one transaction.
//
// Edges leaving path's symbols are replaced: they are deleted first, then the
// refs whose source is in path are merged. Edges entering path's symbols from
// other files (FromFile != path) are only merged, never deleted; the other
// file's own enrichment owns them. Symbols are matched by (project, file,
// name); refs whose endpoints are not in the graph, and unknown kinds, are
// ignored. UpsertFile deletes a file's symbols with DETACH DELETE, so run
// UpsertRefs after it to restore edges.
func (r *Graph) UpsertRefs(ctx context.Context, path string, refs []graph.Reference) error {
	byKind := map[string][]map[string]any{"CALLS": nil, "REFERENCES": nil}
	for _, ref := range refs {
		if _, ok := byKind[ref.Kind]; !ok {
			continue
		}
		byKind[ref.Kind] = append(byKind[ref.Kind], map[string]any{
			"fromFile": ref.FromFile, "fromName": ref.FromName, "toFile": ref.ToFile, "toName": ref.ToName,
		})
	}

	session := r.st.driver.NewSession(ctx, neo4j.SessionConfig{DatabaseName: r.st.db})
	defer func() { _ = session.Close(ctx) }()
	_, err := session.ExecuteWrite(ctx, func(tx neo4j.ManagedTransaction) (any, error) {
		if _, err := tx.Run(ctx, `
			MATCH (:Symbol {project: $project, file: $path})-[r:CALLS|REFERENCES]->()
			DELETE r
		`, r.params(map[string]any{"path": path})); err != nil {
			return nil, err
		}
		// relationship types cannot be parameters; the kinds are a fixed whitelist
		for kind, rows := range byKind {
			if len(rows) == 0 {
				continue
			}
			if _, err := tx.Run(ctx, `
				UNWIND $refs AS r
				MATCH (a:Symbol {project: $project, file: r.fromFile, name: r.fromName})
				MATCH (b:Symbol {project: $project, file: r.toFile, name: r.toName})
				MERGE (a)-[:`+kind+`]->(b)
			`, r.params(map[string]any{"refs": rows})); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	return err
}

// DeleteFile removes a file and its symbols.
func (r *Graph) DeleteFile(ctx context.Context, relPath string) error {
	return r.run(ctx, `
		MATCH (f:File {project: $project, path: $path})
		OPTIONAL MATCH (f)-[:DECLARES]->(sym:Symbol)
		DETACH DELETE sym, f
	`, map[string]any{"path": relPath})
}

// RebuildPackageImports recomputes (Package)-[:IMPORTS]->(Package) for one package
// from its files' imports.
func (r *Graph) RebuildPackageImports(ctx context.Context, pkgKey string) error {
	if err := r.run(ctx, `
		MATCH (p:Package {project: $project, key: $pkg})-[r:IMPORTS]->()
		DELETE r
	`, map[string]any{"pkg": pkgKey}); err != nil {
		return err
	}
	return r.run(ctx, `
		MATCH (p:Package {project: $project, key: $pkg})-[:CONTAINS]->(:File)-[:IMPORTS]->(q:Package)
		WHERE q <> p
		MERGE (p)-[:IMPORTS]->(q)
	`, map[string]any{"pkg": pkgKey})
}

// RebuildAllPackageImports recomputes package-level edges for the whole project.
func (r *Graph) RebuildAllPackageImports(ctx context.Context) error {
	if err := r.run(ctx, `MATCH (:Package {project: $project})-[r:IMPORTS]->() DELETE r`, nil); err != nil {
		return err
	}
	return r.run(ctx, `
		MATCH (p:Package {project: $project})-[:CONTAINS]->(:File)-[:IMPORTS]->(q:Package)
		WHERE q <> p
		MERGE (p)-[:IMPORTS]->(q)
	`, nil)
}

// PruneEmptyPackages removes in-repo packages with no files left.
func (r *Graph) PruneEmptyPackages(ctx context.Context) error {
	return r.run(ctx, `
		MATCH (p:Package {project: $project})
		WHERE p.external = false AND NOT (p)-[:CONTAINS]->()
		DETACH DELETE p
	`, nil)
}

// Stats returns the project's node/edge counts.
func (r *Graph) Stats(ctx context.Context) (graph.Stats, error) {
	rows, err := r.query(ctx, `
		RETURN
		  count { MATCH (p:Package {project: $project}) WHERE p.external = false } AS packages,
		  count { MATCH (:File {project: $project}) } AS files,
		  count { MATCH (:Symbol {project: $project}) } AS symbols,
		  count { MATCH (:Package {project: $project})-[:IMPORTS]->(:Package) } AS imports,
		  count { MATCH (:Symbol {project: $project})-[:CALLS]->(:Symbol) } AS calls
	`, nil)
	if err != nil || len(rows) == 0 {
		return graph.Stats{}, err
	}
	row := rows[0]
	return graph.Stats{
		Packages: i64(row["packages"]),
		Files:    i64(row["files"]),
		Symbols:  i64(row["symbols"]),
		Imports:  i64(row["imports"]),
		Calls:    i64(row["calls"]),
	}, nil
}
