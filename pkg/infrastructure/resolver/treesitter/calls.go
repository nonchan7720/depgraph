package treesitter

import (
	"sort"
	"strings"

	gts "github.com/odvcencio/gotreesitter"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver/imports"
)

// Calls (graph.Usage.Calls): per function, method or class, the functions
// it calls, as far as the syntax tells where they are declared:
//
//	Go          f()        a function of the package (not a parameter or
//	                       local variable called f, not a builtin)
//	            q.F()      a function of the imported package q
//	            r.m()      a method of the receiver's own type, in a method
//	TypeScript  f(), new C()   a top-level declaration of the file, or what
//	                           an import binds; ns.f() through a namespace
//	Python      f(), C()   the same; mod.f() through an imported module
//
// A call through a value whose type the syntax does not give (a field, a
// parameter, an interface) is not recorded: depgraph does not guess.

// pendingCall is a call whose target is still a qualifier or an import spec.
type pendingCall struct {
	q    string // Go: the local package name the call is qualified with
	spec string // TypeScript / Python: the import spec the callee comes from
	name string
}

// pendingCalls are the calls of one caller.
type pendingCalls struct {
	name  string
	line  int
	calls []pendingCall
	// forwards: the caller's body is one call with exactly its parameters
	// (forward.go)
	forwards bool
	// ignore: the rules a depgraph:ignore comment names (ignore.go)
	ignore []string
}

// funcCalls resolves the recorded calls.
func (u *usageCollector) funcCalls(bySpec map[string]imports.Result, goBinds map[string]string) []graph.FuncCalls {
	var out []graph.FuncCalls
	for _, fc := range u.calls {
		seen := map[graph.Call]bool{}
		var calls []graph.Call
		for _, p := range fc.calls {
			c, ok := graph.Call{Name: p.name}, true
			switch {
			case p.q != "":
				c.Target, ok = goBinds[p.q]
			case p.spec != "":
				res := bySpec[p.spec]
				c.Target, ok = strings.TrimSuffix(res.File, "/"), res.Kind == imports.InRepo && res.File != ""
			}
			if ok && !seen[c] {
				seen[c] = true
				calls = append(calls, c)
			}
		}
		if len(calls) == 0 {
			continue
		}
		sort.Slice(calls, func(i, j int) bool {
			if calls[i].Target != calls[j].Target {
				return calls[i].Target < calls[j].Target
			}
			return calls[i].Name < calls[j].Name
		})
		out = append(out, graph.FuncCalls{Name: fc.name, Line: fc.line, Calls: calls, Forwards: fc.forwards && len(calls) == 1, Ignore: fc.ignore})
	}
	return out
}

// goPredeclared are the builtin functions and types of Go: a call of one
// is no call of the repository (unless the package declares the name, which
// the rules cannot tell from here either: left out).
var goPredeclared = map[string]bool{}

func init() {
	for name := range strings.FieldsSeq(`append cap clear close complex copy delete imag len make max min new panic print
		println real recover any bool byte comparable complex64 complex128 error float32 float64 int int8 int16 int32
		int64 rune string uint uint8 uint16 uint32 uint64 uintptr`) {
		goPredeclared[name] = true
	}
}

// goFuncCalls records the calls of every function and method of a Go file.
func (u *usageCollector) goFuncCalls(root *gts.Node) {
	x := u.x
	for i := 0; i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		caller, recvType, recvVar := x.fieldText(n, "name"), "", ""
		switch x.typ(n) {
		case "function_declaration":
		case "method_declaration":
			recvType, recvVar = x.goReceiver(n), x.goReceiverName(n)
			if recvType == "" {
				continue
			}
			caller = recvType + "." + caller
		default:
			continue
		}
		local := x.goLocalNames(n)
		fc := pendingCalls{name: caller, line: int(n.StartPoint().Row) + 1, forwards: recvType == "" && x.forwards(n), ignore: x.ignoredRules(n)}
		x.walk(n, func(c *gts.Node) bool { // every call, also inside a closure or another call
			if x.typ(c) == "call_expression" {
				if pc, ok := x.goCallee(x.field(c, "function"), local, recvType, recvVar); ok {
					fc.calls = append(fc.calls, pc)
				}
			}
			return true
		})
		if len(fc.calls) > 0 {
			u.calls = append(u.calls, fc)
		}
	}
}

// goCallee is the function the callee expression fn of a Go call names:
// f, q.F, or recvVar.m (a method of recvType) in a method.
func (x *extractor) goCallee(fn *gts.Node, local map[string]bool, recvType, recvVar string) (pendingCall, bool) {
	if fn == nil {
		return pendingCall{}, false
	}
	switch x.typ(fn) {
	case "identifier":
		name := x.text(fn)
		return pendingCall{name: name}, !local[name] && !goPredeclared[name]
	case "selector_expression":
		op, f := x.field(fn, "operand"), x.field(fn, "field")
		if op == nil || f == nil || x.typ(op) != "identifier" {
			break
		}
		q := x.text(op)
		if q == recvVar && recvVar != "" {
			return pendingCall{name: recvType + "." + x.text(f)}, true
		}
		return pendingCall{q: q, name: x.text(f)}, !local[q]
	}
	return pendingCall{}, false
}

// goLocalNames are the names a Go function declares inside itself: its
// parameters and results, and the variables of its body (in any scope: a
// call of such a name is not taken for a call of the package's function).
func (x *extractor) goLocalNames(fn *gts.Node) map[string]bool {
	out := map[string]bool{}
	add := func(n *gts.Node) {
		x.walk(n, func(c *gts.Node) bool {
			if x.typ(c) == "identifier" {
				out[x.text(c)] = true
				return false
			}
			return true
		})
	}
	x.walk(fn, func(c *gts.Node) bool {
		switch x.typ(c) {
		case "parameter_declaration", "variadic_parameter_declaration", "var_spec":
			for _, nm := range x.fieldChildren(c, "name") {
				out[x.text(nm)] = true
			}
		case "short_var_declaration", "range_clause":
			if l := x.field(c, "left"); l != nil {
				add(l)
			}
		}
		return true
	})
	return out
}

// declCalls records the calls of every top-level function of a TypeScript
// / JavaScript or Python file and of every method of its classes (named
// Class.method; what a class body runs outside its methods has no caller).
func (u *usageCollector) declCalls(root *gts.Node) {
	x := u.x
	kinds := map[string]string{} // the symbols that can call: functions and classes
	for _, s := range x.fg.Symbols {
		if hasShape[s.Kind] {
			kinds[s.Name] = s.Kind
		}
	}
	type decl struct {
		name string
		node *gts.Node
	}
	var decls []decl
	top := map[string]bool{}
	for i := 0; i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		if x.typ(n) == "export_statement" {
			if n = x.field(n, "declaration"); n == nil {
				continue
			}
		}
		var names []string
		for nm := range x.declNames(n) {
			if kinds[nm] != "" {
				names = append(names, nm)
				top[nm] = true
			}
		}
		if len(names) != 1 {
			continue // `const f = ..., g = ...`: which calls are whose is not told apart
		}
		if kinds[names[0]] != "class" {
			decls = append(decls, decl{names[0], n})
			continue
		}
		for _, m := range x.methodsOf(n) {
			if nm := x.fieldText(m, "name"); nm != "" {
				decls = append(decls, decl{names[0] + "." + nm, m})
			}
		}
	}
	for _, d := range decls {
		fc := pendingCalls{name: d.name, line: int(d.node.StartPoint().Row) + 1,
			forwards: !strings.Contains(d.name, ".") && x.forwards(d.node), ignore: x.ignoredRules(d.node)}
		x.walk(d.node, func(c *gts.Node) bool {
			var callee *gts.Node
			switch x.typ(c) {
			case "call_expression", "call":
				callee = x.field(c, "function")
			case "new_expression":
				callee = x.field(c, "constructor")
			}
			if callee != nil {
				if pc, ok := u.calleeOf(callee, top); ok && (pc.spec != "" || pc.name != d.name) { // not its own recursion
					fc.calls = append(fc.calls, pc)
				}
			}
			return true
		})
		if len(fc.calls) > 0 {
			u.calls = append(u.calls, fc)
		}
	}
}

// methodsOf are the method definitions of a class declaration (TypeScript
// / JavaScript method_definition, Python function_definition, also under a
// decorator).
func (x *extractor) methodsOf(class *gts.Node) []*gts.Node {
	if x.typ(class) == "decorated_definition" {
		class = x.field(class, "definition")
	}
	if class == nil {
		return nil
	}
	body := x.field(class, "body")
	if body == nil {
		return nil
	}
	var out []*gts.Node
	for i := 0; i < body.NamedChildCount(); i++ {
		m := body.NamedChild(i)
		if x.typ(m) == "decorated_definition" {
			if d := x.field(m, "definition"); d != nil && x.typ(d) == "function_definition" {
				out = append(out, d)
			}
			continue
		}
		if t := x.typ(m); t == "method_definition" || t == "function_definition" {
			out = append(out, m)
		}
	}
	return out
}

// calleeOf is the function the callee expression of a TypeScript / Python
// call names: a top-level declaration of the file (top), a name an import
// binds, or a member of an imported namespace / module.
func (u *usageCollector) calleeOf(callee *gts.Node, top map[string]bool) (pendingCall, bool) {
	x := u.x
	switch x.typ(callee) {
	case "identifier":
		name := x.text(callee)
		if b, ok := u.tsBind[name]; ok && !b.namespace {
			return pendingCall{spec: b.spec, name: b.name}, true
		}
		if b := u.pyBind[name]; b != nil && b.fromSpec != "" {
			return pendingCall{spec: b.fromSpec, name: b.fromName}, true
		}
		return pendingCall{name: name}, top[name]
	case "member_expression": // ns.f()
		obj, prop := x.field(callee, "object"), x.field(callee, "property")
		if obj != nil && prop != nil && x.typ(obj) == "identifier" {
			if b, ok := u.tsBind[x.text(obj)]; ok && b.namespace {
				return pendingCall{spec: b.spec, name: x.text(prop)}, true
			}
		}
	case "attribute": // mod.f(), pkg.mod.f()
		chain, ok := x.pyDotted(callee)
		if !ok {
			break
		}
		head, rest, _ := strings.Cut(chain, ".")
		if b := u.pyBind[head]; b != nil {
			if spec, name := pyBindingRef(b, head, rest); spec != "" {
				return pendingCall{spec: spec, name: name}, true
			}
		}
	}
	return pendingCall{}, false
}
