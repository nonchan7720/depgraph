// Package rules implements rules.Repository with Cypher queries over Neo4j.
package rules

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
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
	// root is the directory the project's files are read from (version
	// files, type checker data), "" when they are not available.
	root string
}

var _ rules.Repository = (*Repository)(nil)

// New returns a rules repository over q (the graph store) bound to project.
func New(q Querier, project string) *Repository { return &Repository{q: q, project: project} }

// WithRoot returns r reading the project's files from root (the tree that
// was scanned: the gate's worktree, not necessarily the configuration's).
func (r *Repository) WithRoot(root string) *Repository {
	c := *r
	c.root = root
	return &c
}

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
// It also returns the compiled pairs.
func (r *Repository) layerPairs(ctx context.Context, cfg rules.Config) ([]map[string]any, []rules.LayerPair, error) {
	cfg, err := r.expandComponents(ctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("layers: %w", err)
	}
	lp, err := cfg.LayerPairs()
	if err != nil {
		return nil, nil, err
	}
	out := make([]map[string]any, 0, len(lp))
	for _, p := range lp {
		m := p.Params()
		m["toExternal"] = p.ToSet.HasExternal()
		out = append(out, m)
	}
	return out, lp, nil
}

// inRepoLayer is the Cypher condition "in-repo package node n is in the
// layer set s" (a rules.LayerSet.Params map): rules.LayerSet.Contains.
func inRepoLayer(n, s string) string {
	return fmt.Sprintf(`(NOT any(rx IN %[2]s.notRx WHERE %[1]s.key =~ rx) AND any(rx IN %[2]s.rx WHERE %[1]s.key =~ rx))`, n, s)
}

// inExtLayer is the Cypher condition "external package node n, imported by
// a Python file when py, known to be third-party when tp, is in the layer
// set s": rules.LayerSet.Contains.
func inExtLayer(n, s, py, tp string) string {
	return fmt.Sprintf(`(NOT any(rx IN CASE WHEN %[3]s THEN %[2]s.pyExtNotRx ELSE %[2]s.extNotRx END WHERE %[1]s.key =~ rx) AND (
		any(rx IN CASE WHEN %[3]s THEN %[2]s.pyExtRx ELSE %[2]s.extRx END WHERE %[1]s.key =~ rx) OR
		(%[4]s AND any(rx IN %[2]s.libRx WHERE %[1]s.key =~ rx))))`, n, s, py, tp)
}

// externalUnclassified lists, per language, the external imports of the
// from layer of a forbidden pair whose to layer has a wildcard that would
// match them if they were known to be third-party, and is not decided by an
// exact pattern or an exclusion.
func (r *Repository) externalUnclassified(ctx context.Context, cfg rules.Config, ignore []string,
	pythonFor func([]rules.LayerPair) (*pythonImports, error)) ([]rules.Finding, error) {
	pairs, lp, err := r.layerPairs(ctx, cfg)
	if err != nil {
		return nil, err
	}
	var wild []map[string]any
	for _, p := range pairs {
		if set, _ := p["toSet"].(map[string]any); set != nil {
			if libs, _ := set["libRx"].([]string); len(libs) > 0 {
				wild = append(wild, p)
			}
		}
	}
	if len(wild) == 0 {
		return nil, nil
	}
	rows, err := r.query(ctx, `
		UNWIND $pairs AS pr
		MATCH (a:Package {project: $project})-[:CONTAINS]->(f:File)-[e:IMPORTS]->(b:Package)
		WHERE a.external = false AND b.external = true AND e.class IS NULL AND f.language <> 'python'
		  AND NOT any(rx IN pr.exceptRx WHERE a.key =~ rx)
		  AND NOT any(rx IN $ignore WHERE a.key =~ rx OR b.key =~ rx)
		  AND `+inRepoLayer("a", "pr.fromSet")+` AND NOT `+inRepoLayer("a", "pr.toSet")+`
		WITH a, b, f, e, pr, false AS py
		WHERE `+inExtLayer("b", "pr.toSet", "py", "true")+` AND NOT `+inExtLayer("b", "pr.toSet", "py", "false")+`
		  AND NOT `+inExtLayer("b", "pr.fromSet", "py", "true")+`
		RETURN DISTINCT f.language AS lang, b.key AS key, e.basis AS basis
	`, ignore, map[string]any{"pairs": wild})
	if err != nil {
		return nil, fmt.Errorf("external unclassified: %w", err)
	}
	byLang := map[string][]string{}
	for _, row := range rows {
		lang, _ := row["lang"].(string)
		item := fmt.Sprint(row["key"])
		if basis, _ := row["basis"].(string); basis != "" {
			item += " (" + basis + ")"
		}
		byLang[lang] = append(byLang[lang], item)
	}
	py, err := pythonFor(lp)
	if err != nil {
		return nil, err
	}
	if items := py.unclassified(lp); len(items) > 0 {
		byLang[string(graph.Python)] = append(byLang[string(graph.Python)], items...)
	}
	langs := slices.Sorted(maps.Keys(byLang))
	out := make([]rules.Finding, 0, len(langs))
	for _, lang := range langs {
		items := byLang[lang]
		sort.Strings(items)
		items = slices.Compact(items)
		shown := items
		if len(shown) > 10 {
			shown = shown[:10]
		}
		msg := fmt.Sprintf("%d imports of %s files may be third-party but could not be classified, so ext: wildcards skip them: %s",
			len(items), lang, strings.Join(shown, ", "))
		if n := len(items) - len(shown); n > 0 {
			msg += fmt.Sprintf(", and %d more", n)
		}
		out = append(out, rules.Finding{
			Rule: rules.RuleExternalUnclassified, Severity: string(cfg.Severity(rules.RuleExternalUnclassified)),
			ID: lang, Message: msg, Data: map[string]any{"language": lang, "imports": items},
		})
	}
	return out, nil
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
	// Python imports are classified now, from the project's files
	// (python.go), and their pairs evaluated with rules.LayerPair.Reports.
	var py *pythonImports
	pythonFor := func(lp []rules.LayerPair) (*pythonImports, error) {
		if py == nil && slices.ContainsFunc(lp, func(p rules.LayerPair) bool { return p.ToSet.HasExternal() }) {
			var err error
			if py, err = r.pythonImports(ctx, cfg, ignore); err != nil {
				return nil, err
			}
		}
		return py, nil
	}
	if cfg.Enabled(rules.RuleLayerViolation) {
		pairs, lp, err := r.layerPairs(ctx, cfg)
		if err != nil {
			return nil, err
		}
		if len(pairs) > 0 {
			// rules.LayerPair.Reports: a in from, b in to, and the import
			// crosses the pair. Imports of in-repo packages are package
			// edges; imports of external packages are read per file, whose
			// language decides what is the standard library (and whether
			// submodules are dotted), for the pairs whose to layer names
			// external packages (toExternal). A wildcard matches only an
			// import the resolver classified as third-party (the edge's
			// class; an edge of an older graph has none).
			params := map[string]any{"pairs": pairs}
			rows, err := r.query(ctx, `
				UNWIND $pairs AS pr
				MATCH (a:Package {project: $project})-[:IMPORTS]->(b:Package)
				WHERE a.external = false AND b.external = false
				  AND NOT any(rx IN pr.exceptRx WHERE a.key =~ rx OR b.key =~ rx)
				  AND NOT any(rx IN $ignore WHERE a.key =~ rx OR b.key =~ rx)
				  AND `+inRepoLayer("a", "pr.fromSet")+` AND `+inRepoLayer("b", "pr.toSet")+`
				  AND NOT `+inRepoLayer("a", "pr.toSet")+` AND NOT `+inRepoLayer("b", "pr.fromSet")+`
				RETURN DISTINCT a.key AS from, b.key AS to, pr.from AS fromLayer, pr.to AS toLayer, pr.component AS component
				UNION
				UNWIND $pairs AS pr
				WITH pr WHERE pr.toExternal
				MATCH (a:Package {project: $project})-[:CONTAINS]->(f:File)-[e:IMPORTS]->(b:Package)
				WHERE a.external = false AND b.external = true AND f.language <> 'python'
				  AND NOT any(rx IN pr.exceptRx WHERE a.key =~ rx)
				  AND NOT any(rx IN $ignore WHERE a.key =~ rx OR b.key =~ rx)
				  AND `+inRepoLayer("a", "pr.fromSet")+` AND NOT `+inRepoLayer("a", "pr.toSet")+`
				WITH pr, a, b, false AS py, coalesce(e.class = '`+graph.ClassThirdParty+`', false) AS tp
				WHERE `+inExtLayer("b", "pr.toSet", "py", "tp")+` AND NOT `+inExtLayer("b", "pr.fromSet", "py", "tp")+`
				RETURN DISTINCT a.key AS from, b.key AS to, pr.from AS fromLayer, pr.to AS toLayer, pr.component AS component
			`, ignore, params)
			if err != nil {
				return nil, fmt.Errorf("layers: %w", err)
			}
			py, err := pythonFor(lp)
			if err != nil {
				return nil, err
			}
			rows = append(rows, py.violations(lp)...)
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

	// 2b. Imports a wildcard external layer could not classify.
	if cfg.Enabled(rules.RuleExternalUnclassified) {
		fs, err := r.externalUnclassified(ctx, cfg, ignore, pythonFor)
		if err != nil {
			return nil, err
		}
		out = append(out, fs...)
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

	// 5b. The code inside the layers (members.go).
	members, err := r.evaluateMembers(ctx, cfg, ignore)
	if err != nil {
		return nil, err
	}
	out = append(out, members...)

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
