package rules

import (
	"context"
	"fmt"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// goFunc is a function or method of a Go package: the package directory
// and the name calls use ("Run", "T.Run").
type goFunc struct{ dir, name string }

// functionCycles joins the calls of the Go files (graph.Usage.Calls) into
// the call graph of the project's functions and reports its cycles across
// files. A called name that is no function or method of the package (a
// variable holding a function, a conversion to a type) has no node. Test
// and generated files are left out.
func (r *Repository) functionCycles(ctx context.Context, cfg rules.Config, ignore []string) ([]rules.Finding, error) {
	rows, err := r.query(ctx, `
		MATCH (p:Package {project: $project})-[:CONTAINS]->(f:File {language: 'go'})
		WHERE p.external = false AND none(rx IN $ignore WHERE p.key =~ rx) AND coalesce(f.generated, false) = false
		OPTIONAL MATCH (f)-[:DECLARES]->(s:Symbol) WHERE s.kind IN ['function', 'method']
		WITH p, f, collect({name: s.name, receiver: s.receiver}) AS syms
		RETURN p.key AS pkg, f.path AS file, f.calls AS calls, syms
	`, ignore, nil)
	if err != nil {
		return nil, fmt.Errorf("function cycles: %w", err)
	}
	declared := map[goFunc][]string{} // the files that declare a function
	type caller struct {
		pkg, file string
		calls     graph.FuncCalls
	}
	var callers []caller
	for _, row := range rows {
		pkg, file := fmt.Sprint(row["pkg"]), fmt.Sprint(row["file"])
		if rules.IsTestFile(file) {
			continue
		}
		syms, _ := row["syms"].([]any)
		for _, x := range syms {
			s, _ := x.(map[string]any)
			name, _ := s["name"].(string)
			if name == "" {
				continue
			}
			if recv, _ := s["receiver"].(string); recv != "" {
				name = recv + "." + name
			}
			declared[goFunc{pkg, name}] = append(declared[goFunc{pkg, name}], file)
		}
		for _, enc := range toStrings(row["calls"]) {
			if fc, ok := graph.DecodeFuncCalls(enc); ok {
				callers = append(callers, caller{pkg, file, fc})
			}
		}
	}
	var edges [][2]rules.FuncNode
	for _, c := range callers {
		from := rules.FuncNode{File: c.file, Name: c.calls.Name}
		for _, call := range c.calls.Calls {
			dir := call.Target
			if dir == "" {
				dir = c.pkg
			}
			for _, file := range declared[goFunc{dir, call.Name}] {
				edges = append(edges, [2]rules.FuncNode{from, {File: file, Name: call.Name}})
			}
		}
	}
	var out []rules.Finding
	for _, cyc := range rules.FunctionCycles(edges) {
		f := rules.FunctionCycle(cyc)
		f.Severity = string(cfg.Severity(rules.RuleFunctionCycle))
		out = append(out, f)
	}
	return out, nil
}
