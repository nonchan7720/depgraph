package rules

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// loadFile is a file with what it loads and the names it declares.
type loadFile struct {
	loads    []graph.Use
	declares map[string]bool
}

// linkLoads adds, for the files of one directory, which file loads which
// (from what each file loads, graph.Usage.Loads): LOADS, eager when the
// file is loaded with the importing one, not only when a function of it
// runs. A cycle through several directories is a cycle of packages, which
// import-cycle reports, so only loads inside a directory are linked.
func (r *Repository) linkLoads(ctx context.Context, ignore []string, want *linked) error {
	rows, err := r.query(ctx, `
		MATCH (p:Package {project: $project})-[:CONTAINS]->(f:File)
		WHERE p.external = false AND none(rx IN $ignore WHERE p.key =~ rx)
		OPTIONAL MATCH (f)-[:DECLARES]->(s:Symbol)
		RETURN f.path AS file, f.loads AS loads, collect(s.name) AS names
	`, ignore, nil)
	if err != nil {
		return fmt.Errorf("file loads: %w", err)
	}
	files := make(loadFiles, len(rows))
	for _, row := range rows {
		f := &loadFile{declares: map[string]bool{}}
		for _, s := range toStrings(row["loads"]) {
			if u, ok := graph.DecodeUse(s); ok {
				f.loads = append(f.loads, u)
			}
		}
		for _, n := range toStrings(row["names"]) {
			f.declares[n] = true
		}
		files[fmt.Sprint(row["file"])] = f
	}
	for from, f := range files {
		for _, u := range f.loads {
			to, module := files.loaded(u, 0)
			if to == "" || to == from || path.Dir(to) != path.Dir(from) {
				continue
			}
			if module && path.Base(to) == "__init__.py" {
				// its own package, imported as a module: the import system
				// loaded the package before this module, with or without it
				continue
			}
			id := edgeID{"LOADS", nodeID{"File", from}, nodeID{"File", to}}
			eager := u.As != graph.LoadDeferred
			if old, ok := want.edges[id]; ok {
				eager = eager || old["eager"] == true
			}
			want.edges[id] = map[string]any{"eager": eager}
		}
	}
	return nil
}

// fileCycles reports the import cycles between the files of one directory:
// for every load on a cycle of at most 8 files, the shortest cycle through
// it. First the cycles that exist when the files are loaded (eager loads
// only); then, with deferred, those that only close through an import
// inside a function, which name the files that hide them.
func (r *Repository) fileCycles(ctx context.Context, cfg rules.Config) ([]rules.Finding, error) {
	const limit = 50
	rows, err := r.query(ctx, `
		MATCH (a:File {project: $project})-[e:LOADS]->(b:File {project: $project})
		// a <> b is known before the search: on a plan that pairs the files
		// first, shortestPath fails on a pair of the same file
		WHERE e.eager AND a <> b
		MATCH p = allShortestPaths((b)-[:LOADS*1..7]->(a))
		WHERE all(l IN relationships(p) WHERE l.eager)
		// of several shortest ways back, always the same one: the smallest
		WITH a, b, [n IN nodes(p) | n.path] AS back ORDER BY back
		WITH a, b, head(collect(back)) AS back
		WITH [a.path] + back[0..-1] AS ring
		WITH ring, reduce(m = 0, i IN range(0, size(ring) - 1) | CASE WHEN ring[i] < ring[m] THEN i ELSE m END) AS s
		WITH DISTINCT ring[s..] + ring[..s] AS ring
		RETURN ring ORDER BY size(ring), ring LIMIT $limit
	`, nil, map[string]any{"limit": limit})
	if err != nil {
		return nil, fmt.Errorf("file cycles: %w", err)
	}
	var out []rules.Finding
	for _, row := range rows {
		ring := toStrings(row["ring"])
		out = append(out, rules.FileCycle(append(ring, ring[0])))
	}
	if cfg.FileCycleDeferred && len(out) < limit {
		rows, err := r.query(ctx, `
			MATCH (a:File {project: $project})-[e:LOADS]->(b:File {project: $project})
			WHERE NOT e.eager AND a <> b
			MATCH p = allShortestPaths((b)-[:LOADS*1..7]->(a))
			WITH a, b, e, p, [n IN nodes(p) | n.path] AS back ORDER BY back
			WITH a, b, e, head(collect(p)) AS p
			WITH [a.path] + [n IN nodes(p) | n.path][0..-1] AS ring,
			     [l IN [e] + relationships(p) WHERE NOT l.eager | startNode(l).path] AS hidden
			WITH ring, hidden, reduce(m = 0, i IN range(0, size(ring) - 1) | CASE WHEN ring[i] < ring[m] THEN i ELSE m END) AS s
			WITH ring[s..] + ring[..s] AS ring, hidden ORDER BY hidden
			WITH ring, head(collect(hidden)) AS hidden
			RETURN ring, COLLECT { UNWIND hidden AS h WITH DISTINCT h RETURN h ORDER BY h } AS by
			ORDER BY size(ring), ring LIMIT $limit
		`, nil, map[string]any{"limit": limit - len(out)})
		if err != nil {
			return nil, fmt.Errorf("file cycles: %w", err)
		}
		for _, row := range rows {
			ring, by := toStrings(row["ring"]), toStrings(row["by"])
			f := rules.FileCycle(append(ring, ring[0]))
			f.Message += " (hidden by an import inside a function of " + strings.Join(by, ", ") + ")"
			f.Data["deferred"] = by
			out = append(out, f)
		}
	}
	for i := range out {
		out[i].Severity = string(cfg.Severity(rules.RuleFileCycle))
	}
	return out, nil
}

// loadFiles are the project's files by path.
type loadFiles map[string]*loadFile

// maxReExports bounds how many packages a name is followed through.
const maxReExports = 5

// loaded is the file of the project a load names: the file itself, or for
// a Python package directory the submodule the imported name is, else the
// module the name comes from: the package's __init__.py when that declares
// it, or the module the __init__.py imports it from (a module that takes a
// name its package only passes on depends on the module that declares it,
// not on the package). "" when it is none, or cannot be told (the
// __init__.py does not declare the name but has a wildcard import).
// module: the load is of the file as a whole module (import pkg, from pkg
// import submodule), not of a name in it.
func (fs loadFiles) loaded(u graph.Use, depth int) (file string, module bool) {
	if fs[u.Target] != nil {
		return u.Target, u.Name == ""
	}
	first, _, _ := strings.Cut(u.Name, ".")
	if first != "" {
		for _, mod := range []string{first + ".py", first + ".pyi", first + "/__init__.py"} {
			if p := path.Join(u.Target, mod); fs[p] != nil {
				return p, true
			}
		}
	}
	init := path.Join(u.Target, "__init__.py")
	pkg := fs[init]
	switch {
	case pkg == nil:
		return "", false
	case first == "":
		return init, true
	case pkg.declares[first]:
		return init, false
	case depth >= maxReExports:
		return "", false
	}
	for _, l := range pkg.loads {
		if name, _, _ := strings.Cut(l.Name, "."); name == first {
			return fs.loaded(l, depth+1)
		}
	}
	return "", false
}
