// Package rules implements rules.Repository with Cypher queries over Neo4j.
package rules

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// Querier runs a read query against the graph store.
type Querier interface {
	Query(ctx context.Context, cypher string, params map[string]any) ([]map[string]any, error)
}

// Repository evaluates rules as Cypher queries over one project's graph.
type Repository struct {
	q       Querier
	project string
}

var _ rules.Repository = (*Repository)(nil)

// New returns a rules repository over q (the graph store) bound to project.
func New(q Querier, project string) *Repository { return &Repository{q: q, project: project} }

// query runs cypher with $project set.
func (r *Repository) query(ctx context.Context, cypher string, p map[string]any) ([]map[string]any, error) {
	params := map[string]any{"project": r.project}
	for k, v := range p {
		params[k] = v
	}
	return r.q.Query(ctx, cypher, params)
}

// Evaluate runs every rule and returns findings sorted by key.
func (r *Repository) Evaluate(ctx context.Context, cfg rules.Config) ([]rules.Finding, error) {
	var out []rules.Finding

	// 1. Import cycles between in-repo packages.
	rows, err := r.query(ctx, `
		MATCH p = (a:Package {project: $project})-[:IMPORTS*2..8]->(a)
		WHERE a.external = false
		WITH [n IN nodes(p) | n.key] AS cyc
		WITH cyc, reduce(m = cyc[0], x IN cyc | CASE WHEN x < m THEN x ELSE m END) AS minKey
		WHERE cyc[0] = minKey
		RETURN DISTINCT cyc LIMIT 50
	`, nil)
	if err != nil {
		return nil, fmt.Errorf("cycles: %w", err)
	}
	for _, row := range rows {
		keys := toStrings(row["cyc"])
		out = append(out, rules.Finding{
			Rule: "import-cycle", Severity: "error",
			Message: strings.Join(keys, " -> "),
			Data:    map[string]any{"packages": keys},
		})
	}

	// 2. Layer violations.
	pairs := make([][]string, 0, len(cfg.ForbiddenLayers))
	for _, p := range cfg.ForbiddenLayers {
		pairs = append(pairs, []string{p[0], p[1]})
	}
	rows, err = r.query(ctx, `
		UNWIND $pairs AS pr
		MATCH (a:Package {project: $project})-[:IMPORTS]->(b:Package)
		WHERE a.external = false AND b.external = false
		  AND a.key CONTAINS pr[0] AND b.key CONTAINS pr[1]
		RETURN DISTINCT a.key AS from, b.key AS to, pr[0] AS fromLayer, pr[1] AS toLayer
	`, map[string]any{"pairs": pairs})
	if err != nil {
		return nil, fmt.Errorf("layers: %w", err)
	}
	for _, row := range rows {
		out = append(out, rules.Finding{
			Rule: "layer-violation", Severity: "error",
			Message: fmt.Sprintf("%s -> %s (%s must not import %s)", row["from"], row["to"], row["fromLayer"], row["toLayer"]),
			Data:    row,
		})
	}

	// 3. God packages (high fan-in).
	rows, err = r.query(ctx, `
		MATCH (p:Package {project: $project})<-[:IMPORTS]-(q:Package)
		WHERE p.external = false
		WITH p, count(DISTINCT q) AS fanIn
		WHERE fanIn >= $threshold
		RETURN p.key AS pkg, fanIn ORDER BY fanIn DESC LIMIT 20
	`, map[string]any{"threshold": cfg.GodPackageFanIn})
	if err != nil {
		return nil, fmt.Errorf("god packages: %w", err)
	}
	for _, row := range rows {
		out = append(out, rules.Finding{
			Rule: "god-package", Severity: "warn",
			ID:      fmt.Sprint(row["pkg"]),
			Message: fmt.Sprintf("%s is imported by %d packages", row["pkg"], row["fanIn"]),
			Data:    row,
		})
	}

	// 4. Duplicate exported functions/classes across files (AI tends to re-implement).
	rows, err = r.query(ctx, `
		MATCH (f:File {project: $project})-[:DECLARES]->(s:Symbol)
		WHERE s.exported AND s.kind IN ['function', 'class', 'struct']
		  AND NOT s.name IN $ignore
		WITH s.name AS name, s.kind AS kind, collect(DISTINCT f.path) AS files
		WHERE size(files) > 1
		RETURN name, kind, files ORDER BY size(files) DESC, name LIMIT 50
	`, map[string]any{"ignore": cfg.IgnoreDuplicateNames})
	if err != nil {
		return nil, fmt.Errorf("duplicates: %w", err)
	}
	for _, row := range rows {
		files := toStrings(row["files"])
		out = append(out, rules.Finding{
			Rule: "duplicate-symbol", Severity: "warn",
			ID:      fmt.Sprintf("%s:%s", row["kind"], row["name"]),
			Message: fmt.Sprintf("%s %q declared in %d files: %s", row["kind"], row["name"], len(files), strings.Join(files, ", ")),
			Data:    row,
		})
	}

	// 5. Imports that resolve to nothing: a path or alias that does not exist,
	// typically written from memory. Kept on the file by UpsertFile.
	rows, err = r.query(ctx, `
		MATCH (f:File {project: $project})
		WHERE size(coalesce(f.unresolvedSpecs, [])) > 0
		UNWIND range(0, size(f.unresolvedSpecs) - 1) AS i
		RETURN f.path AS file, f.unresolvedSpecs[i] AS spec, f.unresolvedReasons[i] AS reason
		ORDER BY file, i LIMIT 100
	`, nil)
	if err != nil {
		return nil, fmt.Errorf("unresolved imports: %w", err)
	}
	for _, row := range rows {
		msg := fmt.Sprintf("%s imports %q, which does not resolve", row["file"], row["spec"])
		if reason, _ := row["reason"].(string); reason != "" {
			msg += " (" + reason + ")"
		}
		out = append(out, rules.Finding{
			Rule: "unresolved-import", Severity: "warn",
			ID:      fmt.Sprintf("%s|%s", row["file"], row["spec"]),
			Message: msg,
			Data:    row,
		})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out, nil
}

func toStrings(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		out = append(out, fmt.Sprint(x))
	}
	return out
}
