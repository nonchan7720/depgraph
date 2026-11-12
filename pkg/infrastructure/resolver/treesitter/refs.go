package treesitter

import (
	"strings"

	gts "github.com/odvcencio/gotreesitter"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
)

// References (graph.FuncCalls.Refs): the names of the repository a function
// uses without calling them: the types of its signature and body, values,
// constants. With its calls they are everything the function depends on,
// which tells where it belongs (misplaced-function).

// nodeSpan identifies a node of one tree.
func nodeSpan(n *gts.Node) [2]uint32 { return [2]uint32{uint32(n.StartByte()), uint32(n.EndByte())} }

// goRefs are the names the Go function fn uses without calling them:
// q.Name of an imported package q, and the names it uses unqualified that
// are not its own (parameters, locals), builtins, field names or labels;
// which of those the package declares, the rules know.
func (x *extractor) goRefs(fn *gts.Node, local map[string]bool, recvVar string) []pendingCall {
	var out []pendingCall
	name := x.field(fn, "name")
	var walk func(c *gts.Node)
	children := func(c *gts.Node, skip *gts.Node) {
		for i := 0; i < c.NamedChildCount(); i++ {
			if ch := c.NamedChild(i); ch != skip {
				walk(ch)
			}
		}
	}
	walk = func(c *gts.Node) {
		switch x.typ(c) {
		case "call_expression":
			// the callee is a call (calls.go) when goCallee reads it
			f := x.field(c, "function")
			if f != nil && (x.typ(f) == "identifier" || (x.typ(f) == "selector_expression" && x.isIdent(x.field(f, "operand")))) {
				children(c, f)
				return
			}
		case "selector_expression":
			op, f := x.field(c, "operand"), x.field(c, "field")
			if x.isIdent(op) {
				if q := x.text(op); f != nil && q != recvVar && !local[q] {
					out = append(out, pendingCall{q: q, name: x.text(f)})
				}
				return
			}
			if op != nil {
				walk(op)
			}
			return
		case "qualified_type":
			if c.NamedChildCount() == 2 {
				out = append(out, pendingCall{q: x.text(c.NamedChild(0)), name: x.text(c.NamedChild(1))})
			}
			return
		case "keyed_element": // T{Field: v}: the key is a field name
			for i := 1; i < c.NamedChildCount(); i++ {
				walk(c.NamedChild(i))
			}
			return
		case "labeled_statement", "break_statement", "continue_statement", "goto_statement":
			for i := 0; i < c.NamedChildCount(); i++ {
				if ch := c.NamedChild(i); x.typ(ch) != "label_name" {
					walk(ch)
				}
			}
			return
		case "identifier", "type_identifier":
			if n := x.text(c); c != name && n != "_" && !local[n] && !goPredeclared[n] && !goPredeclaredValues[n] {
				out = append(out, pendingCall{name: n})
			}
			return
		}
		children(c, nil)
	}
	walk(fn)
	return out
}

// goPredeclaredValues are Go's predeclared constants and the zero value.
var goPredeclaredValues = map[string]bool{"true": true, "false": true, "nil": true, "iota": true}

func (x *extractor) isIdent(n *gts.Node) bool { return n != nil && x.typ(n) == "identifier" }

// topLevelNames are the names the top-level statements of a TypeScript /
// JavaScript or Python file declare (functions, classes, types, constants,
// Python module variables).
func (x *extractor) topLevelNames(root *gts.Node) map[string]bool {
	top := map[string]bool{}
	for i := 0; i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		switch x.typ(n) {
		case "import_statement", "import_from_statement", "comment":
			continue
		case "export_statement":
			if n = x.field(n, "declaration"); n == nil {
				continue
			}
		case "expression_statement", "assignment": // Python: NAME = value
			a := n
			if x.typ(n) == "expression_statement" && n.NamedChildCount() == 1 {
				a = n.NamedChild(0)
			}
			if x.typ(a) == "assignment" {
				if l := x.field(a, "left"); x.isIdent(l) {
					top[x.text(l)] = true
				}
			}
			continue
		}
		for nm := range x.declNames(n) {
			top[nm] = true
		}
	}
	return top
}

// declRefsOf are the names the TypeScript / Python declaration n (called
// own) uses without calling them: what its imports bind (as a value or a
// type), members of an imported namespace / module, and the other top-level
// names of the file (top). callees are the callee expressions of its calls.
func (u *usageCollector) declRefsOf(n *gts.Node, own string, top map[string]bool, callees map[[2]uint32]bool) []pendingCall {
	x := u.x
	own, _, _ = strings.Cut(own, ".") // a method: its class
	var out []pendingCall
	x.walk(n, func(c *gts.Node) bool {
		if callees[nodeSpan(c)] {
			return false
		}
		switch x.typ(c) {
		case "member_expression", "nested_type_identifier", "nested_identifier", "attribute":
			if pc, ok := u.memberOf(c); ok {
				out = append(out, pc)
				return false
			}
			return true
		case "identifier", "type_identifier", "shorthand_property_identifier":
			name := x.text(c)
			if p := c.Parent(); p != nil && x.typ(p) == "keyword_argument" && x.field(p, "name") == c {
				return false
			}
			if b, ok := u.tsBind[name]; ok && !b.namespace {
				out = append(out, pendingCall{spec: b.spec, name: b.name})
			} else if b := u.pyBind[name]; b != nil && b.fromSpec != "" {
				out = append(out, pendingCall{spec: b.fromSpec, name: b.fromName})
			} else if top[name] && name != own && u.lang != graph.Go {
				out = append(out, pendingCall{name: name})
			}
			return false
		}
		return true
	})
	return out
}

// memberOf is the name a member of an imported namespace (TypeScript:
// ns.x, ns.T) or module (Python: mod.x, pkg.mod.x) names.
func (u *usageCollector) memberOf(c *gts.Node) (pendingCall, bool) {
	x := u.x
	if x.typ(c) == "attribute" {
		chain, ok := x.pyDotted(c)
		if !ok {
			return pendingCall{}, false
		}
		head, rest, _ := strings.Cut(chain, ".")
		if b := u.pyBind[head]; b != nil {
			if spec, name := pyBindingRef(b, head, rest); spec != "" {
				return pendingCall{spec: spec, name: name}, true
			}
		}
		return pendingCall{}, false
	}
	var obj, prop *gts.Node
	if x.typ(c) == "member_expression" {
		obj, prop = x.field(c, "object"), x.field(c, "property")
	} else if c.NamedChildCount() == 2 {
		obj, prop = c.NamedChild(0), c.NamedChild(1)
	}
	if x.isIdent(obj) && prop != nil {
		if b, ok := u.tsBind[x.text(obj)]; ok && b.namespace {
			return pendingCall{spec: b.spec, name: x.text(prop)}, true
		}
	}
	return pendingCall{}, false
}
