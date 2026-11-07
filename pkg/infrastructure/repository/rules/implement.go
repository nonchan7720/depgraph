// Package rules implements rules.Repository with Cypher queries over Neo4j.
package rules

import (
	"context"
	"fmt"
	"maps"
	"slices"
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

// moduleCycles finds the cycles between cfg's modules and components; the
// directory cycles already in found (import-cycle) are not repeated.
func (r *Repository) moduleCycles(ctx context.Context, cfg rules.Config, ignore []string, found []rules.Finding) ([]rules.ModuleCycle, error) {
	units := append([]string(nil), cfg.Modules...)
	if len(cfg.Components) > 0 {
		keys, err := r.packageKeys(ctx)
		if err != nil {
			return nil, fmt.Errorf("module cycles: %w", err)
		}
		for _, c := range cfg.ExpandComponents(keys).Components {
			units = append(units, c.Dir)
		}
	}
	if len(units) == 0 {
		return nil, nil
	}
	rows, err := r.query(ctx, `
		MATCH (a:Package {project: $project})-[:IMPORTS]->(b:Package {project: $project})
		WHERE a.external = false AND b.external = false AND a <> b
		  AND none(rx IN $ignore WHERE a.key =~ rx OR b.key =~ rx)
		RETURN a.key AS from, b.key AS to
	`, ignore, nil)
	if err != nil {
		return nil, fmt.Errorf("module cycles: %w", err)
	}
	edges := make([][2]string, 0, len(rows))
	for _, row := range rows {
		from, _ := row["from"].(string)
		to, _ := row["to"].(string)
		edges = append(edges, [2]string{from, to})
	}
	var dirCycles [][]string
	for _, f := range found {
		if f.Rule == rules.RuleImportCycle {
			if keys, ok := f.Data["packages"].([]string); ok {
				dirCycles = append(dirCycles, keys)
			}
		}
	}
	return rules.ModuleCycles(edges, units, dirCycles), nil
}

// packageKeys returns the keys of the project's in-repo packages.
func (r *Repository) packageKeys(ctx context.Context) ([]string, error) {
	rows, err := r.query(ctx, `
		MATCH (p:Package {project: $project}) WHERE p.external = false RETURN p.key AS key
	`, nil, nil)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(rows))
	for _, row := range rows {
		if k, ok := row["key"].(string); ok {
			keys = append(keys, k)
		}
	}
	return keys, nil
}

// expandComponents returns cfg with the components whose directory is a
// pattern expanded over the project's packages.
func (r *Repository) expandComponents(ctx context.Context, cfg rules.Config) (rules.Config, error) {
	if !slices.ContainsFunc(cfg.Components, func(c rules.Component) bool { return strings.ContainsAny(c.Dir, "*?") }) {
		return cfg, nil
	}
	keys, err := r.packageKeys(ctx)
	if err != nil {
		return rules.Config{}, err
	}
	return cfg.ExpandComponents(keys), nil
}

// layerPairs compiles cfg's forbidden pairs for the query; components whose
// directory is a pattern are expanded over the project's packages first.
// toExternal tells the query whether a pair's to layer names external
// packages (only then are imports of external packages matched).
func (r *Repository) layerPairs(ctx context.Context, cfg rules.Config) ([]map[string]any, error) {
	cfg, err := r.expandComponents(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("layers: %w", err)
	}
	lp, err := cfg.LayerPairs()
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(lp))
	for _, p := range lp {
		m := p.Params()
		m["toExternal"] = p.ToSet.HasExternal()
		out = append(out, m)
	}
	return out, nil
}

// inLayer is the Cypher condition "package node n is in the layer set s"
// (a rules.LayerSet.Params map): rules.LayerSet.Contains in the store.
func inLayer(n, s string) string {
	return fmt.Sprintf(`(NOT any(rx IN %[2]s.notRx WHERE %[1]s.key =~ rx) AND (
		(%[1]s.external = false AND any(rx IN %[2]s.rx WHERE %[1]s.key =~ rx)) OR
		(%[1]s.external = true AND (any(rx IN %[2]s.extRx WHERE %[1]s.key =~ rx) OR
			(coalesce(%[1]s.std, false) = false AND any(rx IN %[2]s.libRx WHERE %[1]s.key =~ rx))))))`, n, s)
}

// query runs cypher with $project and $ignore set.
func (r *Repository) query(ctx context.Context, cypher string, ignore []string, p map[string]any) ([]map[string]any, error) {
	params := map[string]any{"project": r.project, "ignore": ignore}
	maps.Copy(params, p)
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

	// 1b. Import cycles between modules (Go modules, npm packages, Python
	// projects, components) that are not directory cycles too.
	if cfg.Enabled(rules.RuleModuleCycle) {
		found, err := r.moduleCycles(ctx, cfg, ignore, out)
		if err != nil {
			return nil, err
		}
		for _, c := range found {
			out = append(out, rules.Finding{
				Rule: rules.RuleModuleCycle, Severity: sev(rules.RuleModuleCycle),
				Message: strings.Join(c.Modules, " -> ") + " (" + strings.Join(c.Imports, ", ") + ")",
				ID:      strings.Join(c.Modules, " -> "),
				Data:    map[string]any{"modules": c.Modules, "imports": c.Imports},
			})
		}
	}

	// 2. Layer violations: a in from, b in to, and the edge crosses the pair
	// (a is not also in to, b is not also in from). The pairs of the root
	// and of every component (components: packages/* expanded over the
	// stored packages) run in one query.
	if cfg.Enabled(rules.RuleLayerViolation) {
		pairs, err := r.layerPairs(ctx, cfg)
		if err != nil {
			return nil, err
		}
		if len(pairs) > 0 {
			// rules.LayerPair.Reports: a in from, b in to, and the import
			// crosses the pair; b may be an external package when the pair's
			// to layer names some (toExternal)
			rows, err := r.query(ctx, `
				UNWIND $pairs AS pr
				MATCH (a:Package {project: $project})-[:IMPORTS]->(b:Package)
				WHERE a.external = false AND (b.external = false OR pr.toExternal)
				  AND NOT any(rx IN pr.exceptRx WHERE a.key =~ rx OR (b.external = false AND b.key =~ rx))
				  AND NOT any(rx IN $ignore WHERE a.key =~ rx OR b.key =~ rx)
				  AND `+inLayer("a", "pr.fromSet")+` AND `+inLayer("b", "pr.toSet")+`
				  AND NOT `+inLayer("a", "pr.toSet")+` AND NOT `+inLayer("b", "pr.fromSet")+`
				RETURN DISTINCT a.key AS from, b.key AS to, pr.from AS fromLayer, pr.to AS toLayer, pr.component AS component
			`, ignore, map[string]any{"pairs": pairs})
			if err != nil {
				return nil, fmt.Errorf("layers: %w", err)
			}
			for _, row := range rows {
				msg := fmt.Sprintf("%s -> %s (%s must not import %s)", row["from"], row["to"], row["fromLayer"], row["toLayer"])
				if c, _ := row["component"].(string); c != "" {
					msg = fmt.Sprintf("%s -> %s (%s: %s must not import %s)", row["from"], row["to"], c, row["fromLayer"], row["toLayer"])
				} else {
					delete(row, "component")
				}
				out = append(out, rules.Finding{
					Rule: rules.RuleLayerViolation, Severity: sev(rules.RuleLayerViolation),
					Message: msg, Data: row,
				})
			}
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
		names, err := cfg.DuplicateNameRegexes()
		if err != nil {
			return nil, err
		}
		rows, err := r.query(ctx, `
			MATCH (p:Package {project: $project})-[:CONTAINS]->(f:File)-[:DECLARES]->(s:Symbol)
			WHERE s.exported AND s.kind IN ['function', 'class', 'struct'] AND f.language <> 'ruby'
			  AND NOT any(rx IN $names WHERE s.name =~ rx)
			  AND NOT any(rx IN $ignore WHERE p.key =~ rx)
			WITH s.name AS name, s.kind AS kind,
			     collect(DISTINCT f.path) AS files,
			     collect(DISTINCT CASE WHEN f.language = 'go' THEN p.key ELSE f.path END) AS places
			WHERE size(places) > 1
			RETURN name, kind, files ORDER BY size(places) DESC, name LIMIT 50
		`, ignore, map[string]any{"names": names})
		// A Go name declared in several files of ONE package compiles only
		// because build constraints separate the files (x_unix.go /
		// x_windows.go): one declaration per build, so a Go package counts
		// as one place. Other languages have no such variants. Ruby is left
		// out: reopening a class in another file is how Ruby extends it
		// (duplicate-code still finds re-implemented methods).
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

	// 5b. Structure inside the layers (arch.go): implementations held
	// across an abstract pair, packages over their size limit.
	arch, err := r.evaluateArch(ctx, cfg, ignore)
	if err != nil {
		return nil, err
	}
	out = append(out, arch...)

	// 6. Unused code at project level (unused.go).
	unused, err := r.evaluateUnused(ctx, cfg)
	if err != nil {
		return nil, err
	}
	out = append(out, unused...)

	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out, nil
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
