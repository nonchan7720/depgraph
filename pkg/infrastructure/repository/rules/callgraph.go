package rules

import (
	"context"
	"fmt"
	"path"
	"slices"
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
	// members: the declared type of a field or parameter, by where it is (a
	// Go package directory, else the file), its owner and its name
	members map[memberKey]graph.TypeRef
	types   *typeIndex
	// impls: the only implementation of an abstraction (nil: none or several)
	impls map[graph.TypeRef]*rules.TypeInfo
}

// memberKey is a field or parameter: scope is a Go package directory, else
// the file.
type memberKey struct{ scope, owner, member string }

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
		RETURN p.key AS pkg, f.path AS file, f.language AS lang, f.calls AS calls, f.exports AS exports,
		       f.typeUses AS typeUses, f.ownMembers AS ownMembers, syms
	`, ignore, nil)
	if err != nil {
		return nil, fmt.Errorf("call graph: %w", err)
	}
	g := &callGraph{goFuncs: map[funcKey][]string{}, fileFuncs: map[funcKey]bool{}, exports: map[funcKey]string{}, files: map[string]bool{},
		classes: map[funcKey]bool{}, members: map[memberKey]graph.TypeRef{}}
	if g.types, err = r.typeIndex(ctx, ignore); err != nil {
		return nil, err
	}
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
		scope := file
		if lang == string(graph.Go) {
			scope = pkg
		}
		for _, enc := range toStrings(row["typeUses"]) {
			if u, ok := graph.DecodeTypeUse(enc); ok && !u.Collection && u.Role != graph.RoleResult {
				g.members[memberKey{scope, u.Owner, u.Member}] = u.TypeRef
			}
		}
		for _, enc := range toStrings(row["ownMembers"]) {
			if m, ok := graph.DecodeOwnMember(enc); ok {
				g.members[memberKey{scope, m.Owner, m.Member}] = graph.TypeRef{Target: pkg, Type: m.Type}
			}
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
	if call.Via != "" {
		_, nodes := g.memberCallees(c, call)
		return nodes
	}
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

// memberCallees resolves a call through a member (graph.Call.Via) by the
// member's declared type: declared is the package that declares that type
// ("" when the type is not known), nodes the methods that run: the method
// of a concrete type, or, for an interface / abstract class, the method of
// its implementation when exactly one type of the project implements it.
// With several implementations which one runs is not known, and no method
// is returned: depgraph does not guess.
func (g *callGraph) memberCallees(c caller, call graph.Call) (declared string, nodes []rules.FuncNode) {
	owner, member, _ := strings.Cut(call.Via, graph.ViaSep)
	scope := c.file
	if c.lang == string(graph.Go) {
		scope = c.pkg
	}
	ref, ok := g.members[memberKey{scope, owner, member}]
	if !ok {
		return "", nil
	}
	info, ok := g.types.Type(ref)
	if !ok {
		return "", nil
	}
	target := &info
	if info.Abstraction() {
		if target = g.implementation(info); target == nil {
			return info.Package, nil
		}
	}
	if !slices.Contains(target.Methods, call.Name) {
		return info.Package, nil
	}
	name := target.Ref.Type + "." + call.Name
	if files, isGo := g.goFuncs[funcKey{target.Package, name}]; isGo {
		for _, file := range files {
			nodes = append(nodes, rules.FuncNode{File: file, Name: name})
		}
	} else if g.files[target.Ref.Target] {
		nodes = append(nodes, rules.FuncNode{File: target.Ref.Target, Name: name})
	}
	return info.Package, nodes
}

// implementation is the only type of the project that implements the
// abstraction i (nil: none, or more than one).
func (g *callGraph) implementation(i rules.TypeInfo) *rules.TypeInfo {
	if g.impls == nil {
		g.impls = map[graph.TypeRef]*rules.TypeInfo{}
	}
	if t, done := g.impls[i.Ref]; done {
		return t
	}
	if i.Embeds { // its method set is not all known: what implements it is not either
		g.impls[i.Ref] = nil
		return nil
	}
	var found *rules.TypeInfo
	seen := map[*rules.TypeInfo]bool{}
	for _, t := range g.types.byRef {
		if seen[t] || t.Ref == i.Ref || t.Abstraction() || !t.ImplementsType(i) {
			continue
		}
		seen[t] = true
		if found != nil {
			found = nil
			break
		}
		found = t
	}
	g.impls[i.Ref] = found
	return found
}
