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
		// For every import a -> b, the shortest way back from b to a closes
		// the shortest cycle through that import: every import on a cycle of
		// at most 8 packages is reported, in polynomial time. (Enumerating
		// every elementary cycle, as before, is exponential: a 700-file
		// repository took 17 s, beyond the hook's budget.) shortestPath, not
		// GQL SHORTEST, which the 5.26 planner ran ~500x slower here. Cycles
		// are rotated to start at their smallest key and deduplicated.
		rows, err := r.query(ctx, `
			MATCH (a:Package {project: $project})-[:IMPORTS]->(b:Package {project: $project})
			WHERE a.external = false AND b.external = false AND a <> b
			  AND none(rx IN $ignore WHERE a.key =~ rx OR b.key =~ rx)
			MATCH p = shortestPath((b)-[:IMPORTS*1..7]->(a))
			WHERE all(x IN nodes(p) WHERE x.external = false AND none(rx IN $ignore WHERE x.key =~ rx))
			WITH [a.key] + [n IN nodes(p) | n.key] AS cyc
			WITH cyc[0..size(cyc)-1] AS ring
			WITH ring, reduce(m = 0, i IN range(0, size(ring) - 1) | CASE WHEN ring[i] < ring[m] THEN i ELSE m END) AS s
			WITH ring[s..] + ring[..s] AS r
			WITH DISTINCT r + [r[0]] AS cyc
			RETURN cyc ORDER BY size(cyc), cyc LIMIT 50
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

	// 3. God packages: hubs, imported by many packages and importing many
	// in-repo packages (ignored packages do not count either way). A stable
	// package many depend on and that depends on nothing (a domain model) is
	// healthy, not a god package.
	if cfg.Enabled(rules.RuleGodPackage) {
		rows, err := r.query(ctx, `
			MATCH (p:Package {project: $project})<-[:IMPORTS]-(q:Package)
			WHERE p.external = false AND NOT any(rx IN $ignore WHERE p.key =~ rx OR q.key =~ rx)
			WITH p, count(DISTINCT q) AS fanIn
			WHERE fanIn >= $fanIn
			OPTIONAL MATCH (p)-[:IMPORTS]->(d:Package)
			WHERE d.external = false AND d <> p AND NOT any(rx IN $ignore WHERE d.key =~ rx)
			WITH p, fanIn, count(DISTINCT d) AS fanOut
			WHERE fanOut >= $fanOut
			RETURN p.key AS pkg, fanIn, fanOut ORDER BY fanIn DESC, pkg LIMIT 20
		`, ignore, map[string]any{"fanIn": cfg.GodPackageFanIn, "fanOut": max(cfg.GodPackageFanOut, 1)})
		if err != nil {
			return nil, fmt.Errorf("god packages: %w", err)
		}
		for _, row := range rows {
			out = append(out, rules.Finding{
				Rule: rules.RuleGodPackage, Severity: sev(rules.RuleGodPackage),
				ID:      fmt.Sprint(row["pkg"]),
				Message: fmt.Sprintf("%s is imported by %d packages and imports %d", row["pkg"], row["fanIn"], row["fanOut"]),
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
			WITH s.name AS name, s.kind AS kind,
			     collect(DISTINCT f.path) AS files,
			     collect(DISTINCT CASE WHEN f.language = 'go' THEN p.key ELSE f.path END) AS places
			WHERE size(places) > 1
			RETURN name, kind, files ORDER BY size(places) DESC, name LIMIT 50
		`, ignore, map[string]any{"names": nonNil(cfg.IgnoreDuplicateNames)})
		// A Go name declared in several files of ONE package compiles only
		// because build constraints separate the files (x_unix.go /
		// x_windows.go): one declaration per build, so a Go package counts
		// as one place. Other languages have no such variants.
		if err != nil {
			return nil, fmt.Errorf("duplicates: %w", err)
		}
		for _, row := range rows {
			files := toStrings(row["files"])
			sort.Strings(files) // collect() has no order: keep the message stable
			row["files"] = files
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

	// 6. Unused code at project level (unused.go).
	unused, err := r.evaluateUnused(ctx, cfg)
	if err != nil {
		return nil, err
	}
	out = append(out, unused...)

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
