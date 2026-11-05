// Package hooksession keeps the hook's per-session state on Neo4j:
// (:HookSession {project, id, findings, baseline, updatedAt}) next to the project's
// graph, removed with it (projects rm, Clear) and pruned on every write, so
// nothing is left in the repository and nothing piles up.
package hooksession

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

const (
	// MaxSessions is how many sessions are kept per project (the newest).
	MaxSessions = 20
	// MaxAge: sessions not updated for longer are removed.
	MaxAge = 7 * 24 * time.Hour
)

// Store is the part of the Neo4j connection this repository uses.
type Store interface {
	Exec(ctx context.Context, cypher string, params map[string]any) error
	Query(ctx context.Context, cypher string, params map[string]any) ([]map[string]any, error)
}

// Sessions is the session state of one project (hook.SessionStore).
type Sessions struct {
	st      Store
	project string
}

// New returns the session state of project on st.
func New(st Store, project string) *Sessions { return &Sessions{st: st, project: project} }

// Get returns the findings last stored for session.
func (s *Sessions) Get(ctx context.Context, session string) ([]rules.Finding, bool, error) {
	return s.read(ctx, session, "findings")
}

// Put replaces session's findings (without Data, which the diff does not
// need) and prunes old sessions of the project.
func (s *Sessions) Put(ctx context.Context, session string, findings []rules.Finding, now time.Time) error {
	return s.write(ctx, `SET h.findings = $findings, h.updatedAt = $now`, session, findings, now)
}

// Baseline returns the findings the session started with.
func (s *Sessions) Baseline(ctx context.Context, session string) ([]rules.Finding, bool, error) {
	return s.read(ctx, session, "baseline")
}

// PutBaseline records session's baseline unless it has one, and prunes old
// sessions of the project.
func (s *Sessions) PutBaseline(ctx context.Context, session string, findings []rules.Finding, now time.Time) error {
	return s.write(ctx, `SET h.baseline = coalesce(h.baseline, $findings), h.updatedAt = $now`, session, findings, now)
}

// Absorb adds findings to the baseline of every session of the project that
// has one (debt that reached the checkout through git, e.g. a rebase or a
// merge, is not the sessions' own); a finding already there is not repeated.
func (s *Sessions) Absorb(ctx context.Context, findings []rules.Finding, now time.Time) error {
	if len(findings) == 0 {
		return nil
	}
	rows, err := s.st.Query(ctx, `
		MATCH (h:HookSession {project: $project}) WHERE h.baseline IS NOT NULL
		RETURN h.id AS id
	`, map[string]any{"project": s.project})
	if err != nil {
		return err
	}
	for _, row := range rows {
		id, _ := row["id"].(string)
		base, ok, err := s.Baseline(ctx, id)
		if err != nil || !ok {
			continue
		}
		seen := map[string]bool{}
		for _, f := range base {
			seen[f.Key()] = true
		}
		for _, f := range findings {
			if !seen[f.Key()] {
				seen[f.Key()] = true
				base = append(base, f)
			}
		}
		if err := s.write(ctx, `SET h.baseline = $findings, h.updatedAt = $now`, id, base, now); err != nil {
			return err
		}
	}
	return nil
}

// read returns the findings in property prop of the session; ok is false
// when the session or the property does not exist.
func (s *Sessions) read(ctx context.Context, session, prop string) ([]rules.Finding, bool, error) {
	rows, err := s.st.Query(ctx, `
		MATCH (h:HookSession {project: $project, id: $id}) WHERE h[$prop] IS NOT NULL
		RETURN h[$prop] AS findings
	`, map[string]any{"project": s.project, "id": session, "prop": prop})
	if err != nil || len(rows) == 0 {
		return nil, false, err
	}
	var fs []rules.Finding
	if b, _ := rows[0]["findings"].(string); b != "" {
		if err := json.Unmarshal([]byte(b), &fs); err != nil {
			return nil, false, nil // unreadable: treat as unknown
		}
	}
	return fs, true, nil
}

// write merges the session node, applies set ($findings without Data, $now)
// and prunes old sessions of the project.
func (s *Sessions) write(ctx context.Context, set, session string, findings []rules.Finding, now time.Time) error {
	fs := make([]rules.Finding, 0, len(findings))
	for _, f := range findings {
		f.Data = nil
		fs = append(fs, f)
	}
	b, err := json.Marshal(fs)
	if err != nil {
		return err
	}
	if err := s.st.Exec(ctx, `MERGE (h:HookSession {project: $project, id: $id}) `+set,
		map[string]any{"project": s.project, "id": session, "findings": string(b), "now": now.UTC()}); err != nil {
		return err
	}
	return s.st.Exec(ctx, `
		MATCH (h:HookSession {project: $project})
		WITH h ORDER BY h.updatedAt DESC
		WITH collect(h) AS hs
		UNWIND range(0, size(hs) - 1) AS i
		WITH hs[i] AS h, i
		WHERE i >= $max OR h.updatedAt < $cutoff
		DELETE h
	`, map[string]any{"project": s.project, "max": MaxSessions, "cutoff": now.Add(-MaxAge).UTC()})
}
