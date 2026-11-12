package treesitter

import (
	"slices"
	"strings"

	gts "github.com/odvcencio/gotreesitter"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
)

// Calls through members (graph.Call.Via): a method called on a field of
// the receiver / this / self, or on a parameter. The syntax does not say
// which function runs, but the member's declared type does: the rules look
// it up in the members model (graph.TypeUse, graph.OwnMember) and, for an
// interface, follow it to the types that implement it.
//
//	Go          recv.field.M(), param.M()
//	TypeScript  this.field.m(), param.m()
//	Python      self.field.m(), param.m()
//
// A call on a local variable, on the result of a call or deeper in a chain
// (a.b.c.M()) is not recorded: its type is not declared in one place.

func via(owner, member string) string { return owner + graph.ViaSep + member }

// goMemberCall is the call fn makes through a field of the receiver or a
// parameter of the function caller.
func (x *extractor) goMemberCall(fn *gts.Node, caller string, params []string, recvType, recvVar string) (pendingCall, bool) {
	if fn == nil || x.typ(fn) != "selector_expression" {
		return pendingCall{}, false
	}
	op, f := x.field(fn, "operand"), x.field(fn, "field")
	if op == nil || f == nil {
		return pendingCall{}, false
	}
	switch x.typ(op) {
	case "identifier":
		if p := x.text(op); p != recvVar && slices.Contains(params, p) {
			return pendingCall{name: x.text(f), via: via(caller, p)}, true
		}
	case "selector_expression":
		o, field := x.field(op, "operand"), x.field(op, "field")
		if x.isIdent(o) && field != nil && recvVar != "" && x.text(o) == recvVar {
			return pendingCall{name: x.text(f), via: via(recvType, x.text(field))}, true
		}
	}
	return pendingCall{}, false
}

func (x *extractor) isIdent(n *gts.Node) bool { return n != nil && x.typ(n) == "identifier" }

// goOwnType is the type of the file's own package a Go type expression
// names: T or *T, not a builtin, a collection or another package's.
func (x *extractor) goOwnType(t *gts.Node) string {
	for t != nil {
		switch x.typ(t) {
		case "pointer_type":
			if t.NamedChildCount() == 0 {
				return ""
			}
			t = t.NamedChild(0)
		case "generic_type":
			t = x.field(t, "type")
		case "type_identifier":
			if name := x.text(t); !goPredeclared[name] {
				return name
			}
			return ""
		default:
			return ""
		}
	}
	return ""
}

// goOwnMembers records the parameters of the function or method n (called
// owner) whose type the package declares.
func (u *usageCollector) goOwnMembers(n *gts.Node, owner string) {
	x := u.x
	params := x.field(n, "parameters")
	if params == nil {
		return
	}
	for i := 0; i < params.NamedChildCount(); i++ {
		p := params.NamedChild(i)
		if x.typ(p) != "parameter_declaration" {
			continue
		}
		if t := x.goOwnType(x.field(p, "type")); t != "" {
			for _, nm := range x.fieldChildren(p, "name") {
				u.ownMembers = append(u.ownMembers, graph.OwnMember{Owner: owner, Member: x.text(nm), Type: t})
			}
		}
	}
}

// goOwnFields records the fields of the struct types of a Go file whose
// type the package declares.
func (u *usageCollector) goOwnFields(root *gts.Node) {
	x := u.x
	x.forEachDescendant(root, "type_spec", func(ts *gts.Node) {
		t := x.field(ts, "type")
		if t == nil || x.typ(t) != "struct_type" {
			return
		}
		holder := x.fieldText(ts, "name")
		x.forEachDescendant(t, "field_declaration", func(fd *gts.Node) {
			if own := x.goOwnType(x.field(fd, "type")); own != "" {
				for _, f := range x.fieldChildren(fd, "name") {
					u.ownMembers = append(u.ownMembers, graph.OwnMember{Owner: holder, Member: x.text(f), Type: own})
				}
			}
		})
	})
}

// declParams are the parameter names of the TypeScript / Python function
// or method n (without self / cls).
func (x *extractor) declParams(n *gts.Node) []string {
	fn := n
	if f := x.functionOf(n); f != nil {
		fn = f
	}
	ps := x.field(fn, "parameters")
	if ps == nil {
		if p := x.field(fn, "parameter"); x.isIdent(p) {
			return []string{x.text(p)}
		}
		return nil
	}
	var out []string
	for i := 0; i < ps.NamedChildCount(); i++ {
		if nm := x.paramName(ps.NamedChild(i)); nm != "" && nm != "self" && nm != "cls" {
			out = append(out, nm)
		}
	}
	return out
}

// memberCall is the call the callee expression of a TypeScript / Python
// call makes through a field of this / self or a parameter of the
// declaration decl ("fn", "Class.method"; the parameters of a constructor
// belong to the class, like TypeUse's).
func (u *usageCollector) memberCall(callee *gts.Node, decl string, params []string) (pendingCall, bool) {
	x := u.x
	var obj, prop *gts.Node
	switch x.typ(callee) {
	case "member_expression":
		obj, prop = x.field(callee, "object"), x.field(callee, "property")
	case "attribute":
		obj, prop = x.field(callee, "object"), x.field(callee, "attribute")
	}
	if obj == nil || prop == nil {
		return pendingCall{}, false
	}
	class, method, isMethod := strings.Cut(decl, ".")
	switch x.typ(obj) {
	case "identifier":
		p := x.text(obj)
		if !slices.Contains(params, p) {
			break
		}
		owner := decl
		if isMethod && (method == "constructor" || method == "__init__") {
			owner = class
		}
		return pendingCall{name: x.text(prop), via: via(owner, p)}, true
	case "member_expression", "attribute":
		o := x.field(obj, "object")
		f := x.field(obj, "property")
		if f == nil {
			f = x.field(obj, "attribute")
		}
		if isMethod && o != nil && f != nil && (x.typ(o) == "this" || (x.isIdent(o) && x.text(o) == "self")) {
			return pendingCall{name: x.text(prop), via: via(class, x.text(f))}, true
		}
	}
	return pendingCall{}, false
}
