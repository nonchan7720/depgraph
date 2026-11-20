package rules

import (
	"context"
	"fmt"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// What no entry point reaches, marked on the linked graph of unused.go by
// Cypher statements, and the three rules that read the marks.

// unusedParams are the parameters of the statements and queries: the
// unused-code ignore globs (files or Go package directories) and the
// top-level ignore (packages), as regular expressions.
func unusedParams(cfg rules.Config, ignore []string) (map[string]any, error) {
	_, unusedIgnore, err := cfg.UnusedGlobs()
	if err != nil {
		return nil, err
	}
	return map[string]any{"unusedIgnore": rules.Regexes(unusedIgnore), "ignore": ignore}, nil
}

// ignoredFile is the Cypher condition "file node f of package node p is
// left out of the unused-code rules": the unused-code ignore globs match
// the file or its directory, or the top-level ignore its package.
const ignoredFile = `(any(rx IN $unusedIgnore WHERE f.path =~ rx OR p.key =~ rx) OR any(rx IN $ignore WHERE p.key =~ rx))`

// markUnreachable marks, on the linked graph, the files the entry points
// reach (File.reached), the files and Go packages they do not (dead), and
// for every dead one the file or package it is reported under (deadRoot):
// a dead one nothing imports, with what only it leads to; or, for a cycle
// nothing outside imports, its first one (deadCycle).
func (r *Repository) markUnreachable(ctx context.Context, cfg rules.Config, ignore []string) error {
	p, err := unusedParams(cfg, ignore)
	if err != nil {
		return nil // Validate reports it
	}
	p["project"] = r.project
	steps := []string{
		// start over
		`MATCH (f:File {project: $project})
		 WHERE f.reached IS NOT NULL OR f.dead IS NOT NULL OR f.deadRoot IS NOT NULL
		 REMOVE f.reached, f.dead, f.deadRoot, f.deadCycle`,
		`MATCH (d:Package {project: $project})
		 WHERE d.dead IS NOT NULL OR d.deadRoot IS NOT NULL
		 REMOVE d.dead, d.deadRoot, d.deadCycle`,
		// what the entry points reach: one walk from the root of the entries
		// (a Go package is reached as a whole: GO_PKG)
		`MATCH (root:Root {project: $project, key: 'unused'})
		 WHERE root.usage
		 MATCH (root)-[:ENTRY|USES_FILE|GO_PKG*1..]->(f:File)
		 WITH DISTINCT f
		 SET f.reached = true`,
	}
	if cfg.Enabled(rules.RuleUnreachableFile) {
		steps = append(steps,
			// dead files: TypeScript / JavaScript, Python and Ruby files no
			// entry reaches. A scope (a package.json, a Python project)
			// without any entry point is skipped: its entry points are unknown.
			`MATCH (root:Root {project: $project, key: 'unused'})
			 WHERE root.usage
			 OPTIONAL MATCH (root)-[:ENTRY]->(e:File)
			 WITH collect(DISTINCT CASE e.language WHEN 'javascript' THEN 'typescript' ELSE e.language END + '|' + e.scope) AS scopes
			 MATCH (p:Package {project: $project})-[:CONTAINS]->(f:File)
			 WHERE f.language <> 'go' AND f.reached IS NULL AND NOT coalesce(f.generated, false)
			   AND NOT f.path =~ '(.*/)?__init__\\.py' AND NOT `+ignoredFile+`
			   AND (CASE f.language WHEN 'javascript' THEN 'typescript' ELSE f.language END + '|' + f.scope) IN scopes
			 SET f.dead = true`,
			// under a dead file nothing imports: what only it leads to (of
			// several such files, the smallest)
			`MATCH (top:File {project: $project})
			 WHERE top.dead AND NOT EXISTS { (:File)-[:USES_FILE]->(top) }
			 MATCH way = (top)-[:USES_FILE*0..]->(f:File)
			 WHERE all(x IN nodes(way) WHERE x.dead)
			 WITH DISTINCT top, f
			 WITH f, min(top.path) AS root
			 SET f.deadRoot = root`,
			// the rest are cycles nothing alive imports: under their first file
			`MATCH (top:File {project: $project})
			 WHERE top.dead AND top.deadRoot IS NULL
			 MATCH way = (top)-[:USES_FILE*0..]->(f:File)
			 WHERE all(x IN nodes(way) WHERE x.dead AND x.deadRoot IS NULL)
			 WITH DISTINCT top, f
			 WITH f, min(top.path) AS root
			 SET f.deadRoot = root, f.deadCycle = true`,
		)
	}
	if cfg.Enabled(rules.RuleUnreachablePackage) {
		steps = append(steps,
			// dead Go packages: no file (tests apart) is reached, an entry,
			// generated or ignored, and something is declared (a package of
			// doc.go only is documentation)
			`MATCH (root:Root {project: $project, key: 'unused'})
			 WHERE root.usage
			 MATCH (p:Package {project: $project})-[:CONTAINS]->(f:File {language: 'go'})
			 WHERE NOT f.path ENDS WITH '_test.go'
			 WITH p, EXISTS { (f)-[:DECLARES]->(:Symbol) } AS declares,
			      (f.reached IS NOT NULL OR EXISTS { (:Root)-[:ENTRY]->(f) } OR coalesce(f.generated, false) OR `+ignoredFile+`) AS alive
			 WITH p, collect(declares) AS declares, collect(alive) AS alive
			 WHERE any(x IN declares WHERE x) AND none(x IN alive WHERE x)
			 SET p.dead = true`,
			`MATCH (top:Package {project: $project})
			 WHERE top.dead AND NOT EXISTS {
			   MATCH (top)-[:CONTAINS]->(f:File {language: 'go'})<-[:USES_FILE]-(:File)<-[:CONTAINS]-(other:Package)
			   WHERE NOT f.path ENDS WITH '_test.go' AND other <> top }
			 MATCH way = (top)-[:USES_GO_PKG*0..]->(d:Package)
			 WHERE all(x IN nodes(way) WHERE x.dead)
			 WITH DISTINCT top, d
			 WITH d, min(top.key) AS root
			 SET d.deadRoot = root`,
			`MATCH (top:Package {project: $project})
			 WHERE top.dead AND top.deadRoot IS NULL
			 MATCH way = (top)-[:USES_GO_PKG*0..]->(d:Package)
			 WHERE all(x IN nodes(way) WHERE x.dead AND x.deadRoot IS NULL)
			 WITH DISTINCT top, d
			 WITH d, min(top.key) AS root
			 SET d.deadRoot = root, d.deadCycle = true`,
		)
	}
	for _, step := range steps {
		if err := r.write(ctx, step, p); err != nil {
			return fmt.Errorf("unreachable: %w", err)
		}
	}
	return nil
}

// unreachable reports the dead files (label File, by path) or Go packages
// (label Package, by key): one finding per one nothing imports, with the
// ones only it leads to.
func (r *Repository) unreachable(ctx context.Context, cfg rules.Config, rule, label, key string) ([]rules.Finding, error) {
	rows, err := r.query(ctx, `
		MATCH (n:`+label+` {project: $project})
		WHERE n.deadRoot IS NOT NULL
		WITH n.deadRoot AS root, n ORDER BY n.`+key+`
		WITH root, collect(n) AS dead
		RETURN root, [n IN dead WHERE n.`+key+` <> root | n.`+key+`] AS only,
		       any(n IN dead WHERE n.`+key+` = root AND n.deadCycle) AS cycle
	`, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rule, err)
	}
	out := make([]rules.Finding, 0, len(rows))
	for _, row := range rows {
		cycle, _ := row["cycle"].(bool)
		only := toStrings(row["only"])
		if len(only) == 0 {
			only = nil
		}
		f := rules.UnreachableFile(str(row["root"]), only, cycle)
		if rule == rules.RuleUnreachablePackage {
			f = rules.UnreachablePackage(str(row["root"]), only, cycle)
		}
		f.Severity = string(cfg.Severity(rule))
		out = append(out, f)
	}
	return out, nil
}

// unusedExports reports the exported declarations nothing in the project
// uses, not even their own file (an export only its own file uses is
// unneeded, but that is style: Go handlers registered in the same file).
// Not in an entry point (its exports are used from outside, unless it is
// the API of a package only the repository uses), a generated or ignored
// file, a file whose exports are all used (a namespace used as a value,
// export *), or a file already reported as unreachable.
func (r *Repository) unusedExports(ctx context.Context, cfg rules.Config, ignore []string) ([]rules.Finding, error) {
	p, err := unusedParams(cfg, ignore)
	if err != nil {
		return nil, nil // Validate reports it
	}
	p["kinds"] = reportedKinds
	rows, err := r.query(ctx, `
		MATCH (root:Root {project: $project, key: 'unused'})
		WHERE root.usage
		MATCH (p:Package {project: $project})-[:CONTAINS]->(f:File)-[:DECLARES]->(s:Symbol)
		WHERE s.kind IN $kinds[f.language]
		  AND (s.exported OR any(e IN coalesce(f.exports, []) WHERE e ENDS WITH '=' + s.name))
		  AND NOT s.name IN coalesce(f.keep, [])
		  AND NOT (f.language = 'python' AND s.name STARTS WITH '__')
		  AND NOT coalesce(f.generated, false) AND NOT `+ignoredFile+`
		  AND NOT (EXISTS { (root)-[:ENTRY]->(f) } AND NOT coalesce(f.internal, false))
		  AND NOT EXISTS { (root)-[:USES_ALL]->(f) }
		  AND f.dead IS NULL AND NOT (f.language = 'go' AND p.dead IS NOT NULL AND NOT f.path ENDS WITH '_test.go')
		  AND CASE f.language
		        WHEN 'go' THEN NOT EXISTS { (root)-[:USES_ALL_GO]->(p) }
		                   AND NOT EXISTS { MATCH (:File)-[u:USES_GO]->(p) WHERE s.name IN u.names }
		        ELSE NOT EXISTS { MATCH (:File)-[u:USES_NAMES]->(f) WHERE s.name IN u.names }
		      END
		RETURN f.path AS file, s.line AS line, s.kind AS kind, s.name AS name
	`, nil, p)
	if err != nil {
		return nil, fmt.Errorf("unused exports: %w", err)
	}
	out := make([]rules.Finding, 0, len(rows))
	for _, row := range rows {
		f := rules.UnusedExport(str(row["file"]), toInt(row["line"]), str(row["kind"]), str(row["name"]))
		f.Severity = string(cfg.Severity(rules.RuleUnusedExport))
		out = append(out, f)
	}
	return out, nil
}
