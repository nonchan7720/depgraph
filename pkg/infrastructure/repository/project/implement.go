// Package project implements project.Repository on Neo4j: (:Project) nodes
// next to the graph data they own (nodes whose project property is the key).
package project

import (
	"context"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/project"
)

// Store is the part of the shared Neo4j connection this repository uses.
type Store interface {
	Exec(ctx context.Context, cypher string, params map[string]any) error
	Query(ctx context.Context, cypher string, params map[string]any) ([]map[string]any, error)
}

// Projects stores Project nodes (project.Repository).
type Projects struct {
	st Store
}

var _ project.Repository = (*Projects)(nil)

// New returns the project repository over st.
func New(st Store) *Projects { return &Projects{st: st} }

const fields = `p.key AS key, p.name AS name, p.root AS root, p.origin AS origin,
	p.base AS base, p.worktree AS worktree, p.lastScanAt AS lastScanAt`

// Get returns the project stored under key, or nil.
func (r *Projects) Get(ctx context.Context, key string) (*project.Project, error) {
	rows, err := r.st.Query(ctx, `MATCH (p:Project {key: $key}) RETURN `+fields, map[string]any{"key": key})
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	p := fromRow(rows[0])
	return &p, nil
}

// Upsert creates or replaces the project's properties. A zero LastScanAt is stored as null.
func (r *Projects) Upsert(ctx context.Context, p project.Project) error {
	var last any
	if !p.LastScanAt.IsZero() {
		last = p.LastScanAt.UTC()
	}
	return r.st.Exec(ctx, `
		MERGE (p:Project {key: $key})
		SET p.name = $name, p.root = $root, p.origin = $origin,
		    p.base = $base, p.worktree = $worktree, p.lastScanAt = $last
	`, map[string]any{
		"key": p.Key, "name": p.Name, "root": p.Root, "origin": p.Origin,
		"base": p.Base, "worktree": p.Worktree, "last": last,
	})
}

// List returns every project ordered by key with its file count.
func (r *Projects) List(ctx context.Context) ([]project.Project, error) {
	rows, err := r.st.Query(ctx, `
		MATCH (p:Project)
		RETURN `+fields+`, count { MATCH (f:File) WHERE f.project = p.key } AS files
		ORDER BY key
	`, nil)
	if err != nil {
		return nil, err
	}
	out := make([]project.Project, 0, len(rows))
	for _, row := range rows {
		p := fromRow(row)
		if n, ok := row["files"].(int64); ok {
			p.Files = n
		}
		out = append(out, p)
	}
	return out, nil
}

// Delete removes the project node and every graph node of the project.
func (r *Projects) Delete(ctx context.Context, key string) error {
	if err := r.st.Exec(ctx, `
		MATCH (n) WHERE (n:Package OR n:File OR n:Symbol) AND n.project = $key
		DETACH DELETE n
	`, map[string]any{"key": key}); err != nil {
		return err
	}
	return r.st.Exec(ctx, `MATCH (p:Project {key: $key}) DETACH DELETE p`, map[string]any{"key": key})
}

func fromRow(row map[string]any) project.Project {
	s := func(k string) string {
		v, _ := row[k].(string)
		return v
	}
	p := project.Project{
		Key: s("key"), Name: s("name"), Root: s("root"), Origin: s("origin"),
		Base: s("base"), Worktree: s("worktree"),
	}
	if t, ok := row["lastScanAt"].(time.Time); ok {
		p.LastScanAt = t
	}
	return p
}
