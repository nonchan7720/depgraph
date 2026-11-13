package rules

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// callGraph is the project's functions and what each calls, joined from
// the files' graph.Usage.Calls (Go, TypeScript / JavaScript, Python). Test
// and generated files and ignored packages are left out.
type callGraph struct {
	callers []caller
	// declared: the files that declare a function, by the name calls use.
	// Go: by package directory ("Run", "T.Run"); other languages: by file.
	goFuncs   map[funcKey][]string
	fileFuncs map[funcKey]bool
	// exports: per TypeScript / JavaScript file, exported name -> local
	// declaration ("default" -> "render").
	exports map[funcKey]string
	files   map[string]bool
	// classes: the classes of the TypeScript / JavaScript and Python files
	classes map[funcKey]bool
}

// funcKey is a name in a place: a Go package directory or a file.
type funcKey struct{ place, name string }

// caller is one function with its calls.
type caller struct {
	pkg, file, lang string
	calls           graph.FuncCalls
}

// callGraph reads the call graph of the project.
func (r *Repository) callGraph(ctx context.Context, ignore []string) (*callGraph, error) {
	rows, err := r.query(ctx, `
		MATCH (p:Package {project: $project})-[:CONTAINS]->(f:File)
		WHERE p.external = false AND none(rx IN $ignore WHERE p.key =~ rx) AND coalesce(f.generated, false) = false
		  AND f.language IN ['go', 'typescript', 'javascript', 'python']
		OPTIONAL MATCH (f)-[:DECLARES]->(s:Symbol) WHERE s.kind IN ['function', 'method', 'class']
		WITH p, f, collect({name: s.name, receiver: s.receiver, kind: s.kind}) AS syms
		RETURN p.key AS pkg, f.path AS file, f.language AS lang, f.calls AS calls, f.exports AS exports, syms
	`, ignore, nil)
	if err != nil {
		return nil, fmt.Errorf("call graph: %w", err)
	}
	g := &callGraph{goFuncs: map[funcKey][]string{}, fileFuncs: map[funcKey]bool{}, exports: map[funcKey]string{}, files: map[string]bool{},
		classes: map[funcKey]bool{}}
	for _, row := range rows {
		pkg, file, lang := fmt.Sprint(row["pkg"]), fmt.Sprint(row["file"]), fmt.Sprint(row["lang"])
		if rules.IsTestFile(file) {
			continue
		}
		g.files[file] = true
		syms, _ := row["syms"].([]any)
		for _, x := range syms {
			s, _ := x.(map[string]any)
			name, _ := s["name"].(string)
			if name == "" {
				continue
			}
			if lang != string(graph.Go) {
				g.fileFuncs[funcKey{file, name}] = true
				if kind, _ := s["kind"].(string); kind == "class" {
					g.classes[funcKey{file, name}] = true
				}
				continue
			}
			if recv, _ := s["receiver"].(string); recv != "" {
				name = recv + "." + name
			}
			g.goFuncs[funcKey{pkg, name}] = append(g.goFuncs[funcKey{pkg, name}], file)
		}
		for _, enc := range toStrings(row["exports"]) {
			if e, ok := graph.DecodeExportName(enc); ok {
				g.exports[funcKey{file, e.Name}] = e.Local
			}
		}
		for _, enc := range toStrings(row["calls"]) {
			if fc, ok := graph.DecodeFuncCalls(enc); ok {
				g.callers = append(g.callers, caller{pkg, file, lang, fc})
			}
		}
	}
	return g, nil
}

// node is the function c is.
func (c caller) node() rules.FuncNode { return rules.FuncNode{File: c.file, Name: c.calls.Name} }

// callees are the functions of the project a call of c names: none when
// the name is no function there (a variable holding one, a type), several
// for a Go function declared per build constraint.
func (g *callGraph) callees(c caller, call graph.Call) []rules.FuncNode {
	if c.lang == string(graph.Go) {
		dir := call.Target
		if dir == "" {
			dir = c.pkg
		}
		var out []rules.FuncNode
		for _, file := range g.goFuncs[funcKey{dir, call.Name}] {
			out = append(out, rules.FuncNode{File: file, Name: call.Name})
		}
		return out
	}
	file, name := call.Target, call.Name
	switch {
	case file == "":
		file = c.file
	case c.lang == string(graph.Python):
		file, name = g.pythonCallee(call)
	}
	if local, ok := g.exports[funcKey{file, name}]; ok {
		name = local
	}
	if g.fileFuncs[funcKey{file, name}] {
		return []rules.FuncNode{{File: file, Name: name}}
	}
	return nil
}

// pythonCallee is the module file and the name in it a Python call names:
// Target is a module file, or a package directory whose first name is a
// submodule ("services.show" of app: show of app/services.py) or else a
// name of its __init__.py.
func (g *callGraph) pythonCallee(call graph.Call) (file, name string) {
	first, rest, _ := strings.Cut(call.Name, ".")
	if g.files[call.Target] {
		return call.Target, first
	}
	for _, mod := range []string{first + ".py", first + "/__init__.py"} {
		if p := path.Join(call.Target, mod); g.files[p] {
			name, _, _ = strings.Cut(rest, ".")
			return p, name
		}
	}
	return path.Join(call.Target, "__init__.py"), first
}

// constructs reports whether calling the function to (from a caller in
// language lang) builds a value rather than doing work: a Go constructor by
// its name (New, NewClient, T.New), a class in TypeScript / JavaScript
// (new C()) and Python (C()).
func (g *callGraph) constructs(lang string, to rules.FuncNode) bool {
	if lang == string(graph.Go) {
		name := to.Name[strings.LastIndexByte(to.Name, '.')+1:]
		return strings.HasPrefix(name, "New")
	}
	return g.classes[funcKey{to.File, to.Name}]
}
