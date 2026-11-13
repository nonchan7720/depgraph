package rules

import (
	"context"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// The rules over the function-level call graph: Cypher over the linked
// graph (Function, CALLS, FORWARDS, CALLS_INTO; see link.go, callgraph.go).

// functionCycles reports the cycles of calls across the files of the Go
// packages: for every call between two files, the shortest way back closes
// the shortest cycle through it (at most 8 functions). Not a cycle among
// the methods of one type: a type declared over several files is one unit.
func (r *Repository) functionCycles(ctx context.Context, cfg rules.Config) ([]rules.Finding, error) {
	rows, err := r.query(ctx, `
		MATCH (a:Function {project: $project, lang: 'go'})-[:CALLS]->(b:Function {project: $project, lang: 'go'})
		WHERE a.file <> b.file
		MATCH p = shortestPath((b)-[:CALLS*1..7]->(a))
		WHERE all(x IN nodes(p) WHERE x.lang = 'go')
		WITH [a] + nodes(p)[0..-1] AS ring
		WHERE NOT (ring[0].recv IS NOT NULL AND all(n IN ring WHERE coalesce(n.recv, '') = ring[0].recv))
		WITH ring, [n IN ring | n.file + '\u0000' + n.name] AS ids
		WITH ring, ids, reduce(m = 0, i IN range(0, size(ids) - 1) | CASE WHEN ids[i] < ids[m] THEN i ELSE m END) AS s
		WITH ring[s..] + ring[..s] AS ring, ids[s..] + ids[..s] AS ids
		WITH DISTINCT ids, [n IN ring | [n.file, n.name]] AS cycle
		RETURN cycle ORDER BY size(ids), ids LIMIT 50
	`, nil, nil)
	if err != nil {
		return nil, err
	}
	var out []rules.Finding
	for _, row := range rows {
		nodes := funcNodes(row["cycle"])
		f := rules.FunctionCycle(append(nodes, nodes[0]))
		f.Severity = string(cfg.Severity(rules.RuleFunctionCycle))
		out = append(out, f)
	}
	return out, nil
}

// passThrough reports the chains of at least two functions that only pass
// their parameters on: from a function nothing forwards to, to the first
// function that does something else. A ring of forwarders has no such end.
func (r *Repository) passThrough(ctx context.Context, cfg rules.Config) ([]rules.Finding, error) {
	rows, err := r.query(ctx, `
		MATCH (head:Function {project: $project})-[:FORWARDS]->(:Function)
		WHERE NOT (:Function)-[:FORWARDS]->(head)
		MATCH p = (head)-[:FORWARDS*2..]->(last:Function)
		WHERE NOT (last)-[:FORWARDS]->(:Function)
		RETURN [n IN nodes(p) | [n.file, n.name]] AS chain
		ORDER BY head.file, head.name LIMIT 50
	`, nil, nil)
	if err != nil {
		return nil, err
	}
	var out []rules.Finding
	for _, row := range rows {
		f := rules.PassThrough(funcNodes(row["chain"]))
		f.Severity = string(cfg.Severity(rules.RulePassThrough))
		out = append(out, f)
	}
	return out, nil
}

// wideFunctions reports the functions that call functions of more packages
// (directories) of the repository than the limit, their own not counted. A
// call through a member counts the package that declares the member's type
// (CALLS_INTO), whatever implements it. Calls of constructors are not
// counted: building what it needs from many packages is wiring, using their
// behaviour is knowing them. A function with a `depgraph:ignore
// wide-function` comment is left out.
func (r *Repository) wideFunctions(ctx context.Context, cfg rules.Config) ([]rules.Finding, error) {
	limit := cfg.CalledPackagesLimit()
	rows, err := r.query(ctx, `
		MATCH (f:Function {project: $project})
		WHERE f.caller AND NOT $rule IN coalesce(f.ignore, [])
		CALL {
			WITH f
			MATCH (f)-[c:CALLS]->(g:Function)
			WHERE NOT c.via AND NOT c.construct AND g.dir <> f.dir
			RETURN g.dir AS dir
			UNION
			WITH f
			MATCH (f)-[:CALLS_INTO]->(p:Package)
			WHERE p.key <> f.dir
			RETURN p.key AS dir
		}
		WITH f, dir ORDER BY dir
		WITH f, collect(dir) AS packages
		WHERE size(packages) > $limit
		RETURN f.file AS file, f.line AS line, f.name AS name, packages
	`, nil, map[string]any{"limit": limit, "rule": rules.RuleWideFunction})
	if err != nil {
		return nil, err
	}
	var out []rules.Finding
	for _, row := range rows {
		f := rules.WideFunction(str(row["file"]), toInt(row["line"]), str(row["name"]), toStrings(row["packages"]), limit)
		f.Severity = string(cfg.Severity(rules.RuleWideFunction))
		out = append(out, f)
	}
	return out, nil
}

// funcNodes are the functions of a list of [file, name] pairs.
func funcNodes(v any) []rules.FuncNode {
	pairs, _ := v.([]any)
	out := make([]rules.FuncNode, 0, len(pairs))
	for _, p := range pairs {
		if fn := toStrings(p); len(fn) == 2 {
			out = append(out, rules.FuncNode{File: fn[0], Name: fn[1]})
		}
	}
	return out
}
