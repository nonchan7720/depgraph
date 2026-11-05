// Package project implements project.Repository on Neo4j: (:Project) nodes
// next to the graph data they own (nodes whose project property is the key).
package project

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/project"
)

// Store is the part of the shared Neo4j connection this repository uses.
type Store interface {
	Exec(ctx context.Context, cypher string, params map[string]any) error
	Query(ctx context.Context, cypher string, params map[string]any) ([]map[string]any, error)
	Write(ctx context.Context, cypher string, params map[string]any) ([]map[string]any, error)
}

// Projects stores Project nodes (project.Repository).
type Projects struct {
	st Store
}

var _ project.Repository = (*Projects)(nil)

// New returns the project repository over st.
func New(st Store) *Projects { return &Projects{st: st} }

const fields = `p.key AS key, p.name AS name, p.root AS root, p.origin AS origin,
	p.base AS base, p.worktree AS worktree, p.lastScanAt AS lastScanAt,
	p.commit AS commit, p.dirty AS dirty`

// Get returns the project stored under key, or nil.
func (r *Projects) Get(ctx context.Context, key string) (*project.Project, error) {
	rows, err := r.st.Query(ctx, `MATCH (p:Project {key: $key}) RETURN `+fields, map[string]any{"key": key})
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	p := fromRow(rows[0])
	return &p, nil
}

// Upsert creates or replaces the project's properties. A zero LastScanAt,
// an empty Commit and an empty Dirty are stored as null.
func (r *Projects) Upsert(ctx context.Context, p project.Project) error {
	var last any
	if !p.LastScanAt.IsZero() {
		last = p.LastScanAt.UTC()
	}
	var commit, dirty any
	if p.Commit != "" {
		commit = p.Commit
	}
	if len(p.Dirty) > 0 {
		enc := make([]string, 0, len(p.Dirty))
		for _, d := range p.Dirty {
			enc = append(enc, d.Hash+" "+d.Path) // the hash never holds a blank
		}
		dirty = enc
	}
	return r.st.Exec(ctx, `
		MERGE (p:Project {key: $key})
		SET p.name = $name, p.root = $root, p.origin = $origin,
		    p.base = $base, p.worktree = $worktree, p.lastScanAt = $last,
		    p.commit = $commit, p.dirty = $dirty
	`, map[string]any{
		"key": p.Key, "name": p.Name, "root": p.Root, "origin": p.Origin,
		"base": p.Base, "worktree": p.Worktree, "last": last, "commit": commit, "dirty": dirty,
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
		MATCH (n) WHERE (n:Package OR n:File OR n:Symbol OR n:HookSession) AND n.project = $key
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
		Base: s("base"), Worktree: s("worktree"), Commit: s("commit"),
	}
	if l, ok := row["dirty"].([]any); ok {
		for _, v := range l {
			if hash, path, ok := strings.Cut(fmt.Sprint(v), " "); ok {
				p.Dirty = append(p.Dirty, project.DirtyFile{Path: path, Hash: hash})
			}
		}
	}
	if t, ok := row["lastScanAt"].(time.Time); ok {
		p.LastScanAt = t
	}
	return p
}

// ClaimSync takes the sync lease of p until until, unless another holder's
// lease is still running at now; it creates p's node when there is none (with
// p's identity, so the key resolves to p). It reports whether the lease was taken.
func (r *Projects) ClaimSync(ctx context.Context, p project.Project, now, until time.Time) (bool, error) {
	rows, err := r.st.Write(ctx, `
		MERGE (p:Project {key: $key})
		ON CREATE SET p.name = $name, p.root = $root, p.origin = $origin, p.base = $base, p.worktree = $worktree
		WITH p WHERE p.syncUntil IS NULL OR p.syncUntil < $now
		SET p.syncUntil = $until
		RETURN p.key AS key
	`, map[string]any{
		"key": p.Key, "name": p.Name, "root": p.Root, "origin": p.Origin, "base": p.Base, "worktree": p.Worktree,
		"now": now.UTC(), "until": until.UTC(),
	})
	return len(rows) == 1, err
}

// SyncHeld reports whether a sync lease of key runs at now.
func (r *Projects) SyncHeld(ctx context.Context, key string, now time.Time) (bool, error) {
	rows, err := r.st.Query(ctx, `
		MATCH (p:Project {key: $key}) WHERE p.syncUntil IS NOT NULL AND p.syncUntil >= $now
		RETURN p.key AS key
	`, map[string]any{"key": key, "now": now.UTC()})
	return len(rows) == 1, err
}

// ReleaseSync gives the sync lease of key back.
func (r *Projects) ReleaseSync(ctx context.Context, key string) error {
	return r.st.Exec(ctx, `MATCH (p:Project {key: $key}) SET p.syncUntil = null`, map[string]any{"key": key})
}
