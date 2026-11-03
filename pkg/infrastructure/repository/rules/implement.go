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

// query runs cypher with $project and $ignore set.
func (r *Repository) query(ctx context.Context, cypher string, ignore []string, p map[string]any) ([]map[string]any, error) {
	params := map[string]any{"project": r.project, "ignore": ignore}
	for k, v := range p {
		params[k] = v
	}
	return r.q.Query(ctx, cypher, params)
}

// Evaluate runs every enabled rule and returns findings sorted by key.
// Package patterns (layers, ignore) are matched in the store with the
// regular expressions of rules.Glob, so they mean the same as in Go.
func (r *Repository) Evaluate(ctx context.Context, cfg rules.Config) ([]rules.Finding, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	ignoreGlobs, err := cfg.IgnoreGlobs()
	if err != nil {
		return nil, err
	}
	ignore := rules.Regexes(ignoreGlobs)
	sev := func(rule string) string { return string(cfg.Severity(rule)) }
	var out []rules.Finding

	// 1. Import cycles between in-repo packages (none through an ignored one).
	if cfg.Enabled(rules.RuleImportCycle) {
		rows, err := r.query(ctx, `
			MATCH p = (a:Package {project: $project})-[:IMPORTS*2..8]->(a)
			WHERE a.external = false
			  AND none(n IN nodes(p) WHERE any(rx IN $ignore WHERE n.key =~ rx))
			WITH [n IN nodes(p) | n.key] AS cyc
			WITH cyc, reduce(m = cyc[0], x IN cyc | CASE WHEN x < m THEN x ELSE m END) AS minKey
			WHERE cyc[0] = minKey
			RETURN DISTINCT cyc LIMIT 50
		`, ignore, nil)
		if err != nil {
			return nil, fmt.Errorf("cycles: %w", err)
		}
		for _, row := range rows {
			keys := toStrings(row["cyc"])
			out = append(out, rules.Finding{
				Rule: rules.RuleImportCycle, Severity: sev(rules.RuleImportCycle),
				Message: strings.Join(keys, " -> "),
				Data:    map[string]any{"packages": keys},
			})
		}
	}

	// 2. Layer violations: a in from, b in to, and the edge crosses the pair
	// (a is not also in to, b is not also in from).
	if cfg.Enabled(rules.RuleLayerViolation) && len(cfg.ForbiddenLayers) > 0 {
		layers, err := cfg.LayerGlobs()
		if err != nil {
			return nil, err
		}
		pairs := make([]map[string]any, 0, len(cfg.ForbiddenLayers))
		for _, p := range cfg.ForbiddenLayers {
			pairs = append(pairs, map[string]any{
				"from": p[0], "to": p[1],
				"fromRx": rules.Regexes(layers[p[0]]), "toRx": rules.Regexes(layers[p[1]]),
			})
		}
		rows, err := r.query(ctx, `
			UNWIND $pairs AS pr
			MATCH (a:Package {project: $project})-[:IMPORTS]->(b:Package)
			WHERE a.external = false AND b.external = false
			  AND any(rx IN pr.fromRx WHERE a.key =~ rx) AND any(rx IN pr.toRx WHERE b.key =~ rx)
			  AND NOT any(rx IN pr.toRx WHERE a.key =~ rx) AND NOT any(rx IN pr.fromRx WHERE b.key =~ rx)
			  AND NOT any(rx IN $ignore WHERE a.key =~ rx OR b.key =~ rx)
			RETURN DISTINCT a.key AS from, b.key AS to, pr.from AS fromLayer, pr.to AS toLayer
		`, ignore, map[string]any{"pairs": pairs})
		if err != nil {
			return nil, fmt.Errorf("layers: %w", err)
		}
		for _, row := range rows {
			out = append(out, rules.Finding{
				Rule: rules.RuleLayerViolation, Severity: sev(rules.RuleLayerViolation),
				Message: fmt.Sprintf("%s -> %s (%s must not import %s)", row["from"], row["to"], row["fromLayer"], row["toLayer"]),
				Data:    row,
			})
		}
	}

	// 3. God packages (high fan-in; ignored importers do not count).
	if cfg.Enabled(rules.RuleGodPackage) {
		rows, err := r.query(ctx, `
			MATCH (p:Package {project: $project})<-[:IMPORTS]-(q:Package)
			WHERE p.external = false AND NOT any(rx IN $ignore WHERE p.key =~ rx OR q.key =~ rx)
			WITH p, count(DISTINCT q) AS fanIn
			WHERE fanIn >= $threshold
			RETURN p.key AS pkg, fanIn ORDER BY fanIn DESC LIMIT 20
		`, ignore, map[string]any{"threshold": cfg.GodPackageFanIn})
		if err != nil {
			return nil, fmt.Errorf("god packages: %w", err)
		}
		for _, row := range rows {
			out = append(out, rules.Finding{
				Rule: rules.RuleGodPackage, Severity: sev(rules.RuleGodPackage),
				ID:      fmt.Sprint(row["pkg"]),
				Message: fmt.Sprintf("%s is imported by %d packages", row["pkg"], row["fanIn"]),
				Data:    row,
			})
		}
	}

	// 4. Duplicate exported functions/classes across files (AI tends to re-implement).
	if cfg.Enabled(rules.RuleDuplicateSymbol) {
		rows, err := r.query(ctx, `
			MATCH (p:Package {project: $project})-[:CONTAINS]->(f:File)-[:DECLARES]->(s:Symbol)
			WHERE s.exported AND s.kind IN ['function', 'class', 'struct']
			  AND NOT s.name IN $names
			  AND NOT any(rx IN $ignore WHERE p.key =~ rx)
			WITH s.name AS name, s.kind AS kind, collect(DISTINCT f.path) AS files
			WHERE size(files) > 1
			RETURN name, kind, files ORDER BY size(files) DESC, name LIMIT 50
		`, ignore, map[string]any{"names": nonNil(cfg.IgnoreDuplicateNames)})
		if err != nil {
			return nil, fmt.Errorf("duplicates: %w", err)
		}
		for _, row := range rows {
			files := toStrings(row["files"])
			sort.Strings(files) // collect() has no order: keep the message stable
			out = append(out, rules.Finding{
				Rule: rules.RuleDuplicateSymbol, Severity: sev(rules.RuleDuplicateSymbol),
				ID:      fmt.Sprintf("%s:%s", row["kind"], row["name"]),
				Message: fmt.Sprintf("%s %q declared in %d files: %s", row["kind"], row["name"], len(files), strings.Join(files, ", ")),
				Data:    row,
			})
		}
	}

	// 5. Duplicated code: the same (or nearly the same) logic in several
	// places, whatever the names. Test code is left out (table tests look
	// alike on purpose), and so are ignored packages.
	if cfg.Enabled(rules.RuleDuplicateCode) {
		rows, err := r.query(ctx, `
			MATCH (p:Package {project: $project})-[:CONTAINS]->(f:File)-[:DECLARES]->(s:Symbol)
			WHERE s.tokens >= $min AND none(rx IN $ignore WHERE p.key =~ rx)
			RETURN f.path AS file, s.name AS name, s.kind AS kind, s.line AS line, s.endLine AS endLine,
			       s.tokens AS tokens, s.cloneHash AS hash, s.fingerprints AS fps
		`, ignore, map[string]any{"min": max(cfg.Clones.MinTokens, 1)})
		if err != nil {
			return nil, fmt.Errorf("duplicate code: %w", err)
		}
		var cands []rules.CloneCandidate
		for _, row := range rows {
			file := fmt.Sprint(row["file"])
			if rules.IsTestFile(file) {
				continue
			}
			c := rules.CloneCandidate{File: file, Name: fmt.Sprint(row["name"]), Kind: fmt.Sprint(row["kind"]),
				Line: toInt(row["line"]), EndLine: toInt(row["endLine"]), Tokens: toInt(row["tokens"])}
			c.Hash, _ = row["hash"].(string)
			if fps, ok := row["fps"].([]any); ok {
				for _, f := range fps {
					if v, ok := f.(int64); ok {
						c.Fingerprints = append(c.Fingerprints, v)
					}
				}
			}
			cands = append(cands, c)
		}
		for _, f := range rules.FindClones(cands, cfg.Clones) {
			f.Severity = sev(rules.RuleDuplicateCode)
			out = append(out, f)
		}
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out, nil
}

// nonNil keeps an empty list a list in Cypher (nil would be null, and
// `x IN null` is null, which drops every row).
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func toInt(v any) int {
	n, _ := v.(int64)
	return int(n)
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
