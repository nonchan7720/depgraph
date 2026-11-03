// Package hooksession keeps the hook's per-session state on Neo4j:
// (:HookSession {project, id, findings, updatedAt}) next to the project's
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
	rows, err := s.st.Query(ctx, `
		MATCH (h:HookSession {project: $project, id: $id}) RETURN h.findings AS findings
	`, map[string]any{"project": s.project, "id": session})
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

// Put replaces session's findings (without Data, which the diff does not
// need) and prunes old sessions of the project.
func (s *Sessions) Put(ctx context.Context, session string, findings []rules.Finding, now time.Time) error {
	fs := make([]rules.Finding, 0, len(findings))
	for _, f := range findings {
		f.Data = nil
		fs = append(fs, f)
	}
	b, err := json.Marshal(fs)
	if err != nil {
		return err
	}
	if err := s.st.Exec(ctx, `
		MERGE (h:HookSession {project: $project, id: $id})
		SET h.findings = $findings, h.updatedAt = $now
	`, map[string]any{"project": s.project, "id": session, "findings": string(b), "now": now.UTC()}); err != nil {
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
