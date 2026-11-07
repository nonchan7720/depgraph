package treesitter

import (
	"slices"
	"strconv"
	"strings"

	gts "github.com/odvcencio/gotreesitter"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver/imports"
)

// Members (graph.TypeUse, graph.Construct, Symbol.Implements): which types
// of the repository the file's members hold, take, return and construct.
//
//	Go          struct fields (named and embedded), interface method
//	            signatures, the parameters (New*: constructor parameters) and
//	            results of functions and methods; composite literals (q.T{})
//	TypeScript  class properties, constructor / method parameters and return
//	            types, interface method signatures, functions; new T();
//	            implements / extends
//	Python      class annotations, __init__ / method parameters and return
//	            annotations, functions; calls of an imported class (T());
//	            base classes
//
// A type is named through an import (q.T, an imported name, ns.T); the
// file's own types and anything that does not resolve into the repository
// are dropped. Collections (slices, maps, arrays, list[T], T[]) and the
// arguments of generic types are marked Collection.

// typeName is one type a type expression names: q the package / namespace /
// module it is written through ("" for a bare name).
type typeName struct {
	q, name string
	coll    bool
}

// memberRef is a type a member names, before it is resolved.
type memberRef struct {
	use       graph.TypeUse // without TypeRef
	construct bool          // a Construct (use.Owner, use.Line)
	implement int           // >= 0: Symbols index of the class that implements it
	t         typeName
}

func (u *usageCollector) addRefs(use graph.TypeUse, at *gts.Node, names []typeName) {
	use.Line = int(at.StartPoint().Row) + 1
	for _, n := range names {
		if n.name == "" {
			continue
		}
		ru := use
		ru.Collection = use.Collection || n.coll
		u.members = append(u.members, memberRef{use: ru, implement: -1, t: n})
	}
}

func (u *usageCollector) addConstruct(owner string, at *gts.Node, n typeName) {
	if n.name == "" {
		return
	}
	u.members = append(u.members, memberRef{use: graph.TypeUse{Owner: owner, Line: int(at.StartPoint().Row) + 1}, construct: true, implement: -1, t: n})
}

func (u *usageCollector) addImplements(sym int, n typeName) {
	if sym >= 0 && n.name != "" {
		u.members = append(u.members, memberRef{implement: sym, t: n})
	}
}

// symIndex is the index of the symbol name declared at line (-1).
func (x *extractor) symIndex(name string, line int) int {
	for i, s := range slices.Backward(x.fg.Symbols) {
		if s.Name == name && s.Line == line {
			return i
		}
	}
	return -1
}

// fieldChildren are the named children of n in field name.
func (x *extractor) fieldChildren(n *gts.Node, name string) []*gts.Node {
	var out []*gts.Node
	for i := 0; i < n.ChildCount(); i++ {
		if n.FieldNameForChild(i, x.lang) == name {
			if c := n.Child(i); c != nil && c.IsNamed() {
				out = append(out, c)
			}
		}
	}
	return out
}

// ---- Go ------------------------------------------------------------------

func (u *usageCollector) goMembers(root *gts.Node) {
	x := u.x
	for i := 0; i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		switch x.typ(n) {
		case "type_declaration":
			for j := 0; j < n.NamedChildCount(); j++ {
				ts := n.NamedChild(j)
				t := x.field(ts, "type")
				if x.typ(ts) != "type_spec" || t == nil {
					continue
				}
				holder := x.fieldText(ts, "name")
				switch x.typ(t) {
				case "struct_type":
					x.forEachDescendant(t, "field_declaration", func(fd *gts.Node) {
						names := x.goTypeNames(x.field(fd, "type"))
						fields := x.fieldChildren(fd, "name")
						if len(fields) == 0 && len(names) == 1 { // embedded
							u.addRefs(graph.TypeUse{Owner: holder, Member: names[0].name, Role: graph.RoleField,
								Exported: goExported(holder) && goExported(names[0].name)}, fd, names)
						}
						for _, f := range fields {
							u.addRefs(graph.TypeUse{Owner: holder, Member: x.text(f), Role: graph.RoleField,
								Exported: goExported(holder) && goExported(x.text(f))}, fd, names)
						}
					})
				case "interface_type":
					for k := 0; k < t.NamedChildCount(); k++ {
						m := t.NamedChild(k)
						if x.typ(m) != "method_elem" {
							continue
						}
						name := x.fieldText(m, "name")
						u.goSignature(holder+"."+name, goExported(holder) && goExported(name), false, m)
					}
				}
			}
		case "function_declaration":
			name := x.fieldText(n, "name")
			u.goSignature(name, goExported(name), strings.HasPrefix(name, "New"), n)
			u.goConstructs(name, x.field(n, "body"))
		case "method_declaration":
			recv, name := x.goReceiver(n), x.fieldText(n, "name")
			owner := recv + "." + name
			u.goSignature(owner, goExported(recv) && goExported(name), false, n)
			u.goConstructs(owner, x.field(n, "body"))
		}
	}
}

// goSignature records the parameters and results of a function, method or
// interface method n.
func (u *usageCollector) goSignature(owner string, exported, ctor bool, n *gts.Node) {
	x := u.x
	role := graph.RoleParam
	if ctor {
		role = graph.RoleCtor
	}
	if params := x.field(n, "parameters"); params != nil {
		for i := 0; i < params.NamedChildCount(); i++ {
			p := params.NamedChild(i)
			if x.typ(p) != "parameter_declaration" {
				continue
			}
			names := x.goTypeNames(x.field(p, "type"))
			for _, nm := range x.fieldChildren(p, "name") {
				u.addRefs(graph.TypeUse{Owner: owner, Member: x.text(nm), Role: role, Exported: exported}, p, names)
			}
		}
	}
	res := x.field(n, "result")
	if res == nil {
		return
	}
	if x.typ(res) != "parameter_list" {
		u.addRefs(graph.TypeUse{Owner: owner, Member: "0", Role: graph.RoleResult, Exported: exported}, res, x.goTypeNames(res))
		return
	}
	idx := 0
	for i := 0; i < res.NamedChildCount(); i++ {
		p := res.NamedChild(i)
		if x.typ(p) != "parameter_declaration" {
			continue
		}
		names := x.goTypeNames(x.field(p, "type"))
		count := max(len(x.fieldChildren(p, "name")), 1)
		for range count {
			u.addRefs(graph.TypeUse{Owner: owner, Member: strconv.Itoa(idx), Role: graph.RoleResult, Exported: exported}, p, names)
			idx++
		}
	}
}

// goConstructs records the composite literals of a type of another package
// (q.T{}, &q.T{}) in body.
func (u *usageCollector) goConstructs(owner string, body *gts.Node) {
	x := u.x
	x.walk(body, func(c *gts.Node) bool {
		if x.typ(c) == "composite_literal" {
			if t := x.field(c, "type"); t != nil {
				for _, n := range x.goTypeNames(t) {
					if !n.coll {
						u.addConstruct(owner, c, n)
					}
				}
			}
		}
		return true
	})
}

// goTypeNames are the package-qualified types a Go type expression names:
// q.T, *q.T, q.T[X] and, as collections, []q.T, [n]q.T, map[k]q.T.
func (x *extractor) goTypeNames(t *gts.Node) []typeName {
	coll := false
	for t != nil {
		switch x.typ(t) {
		case "pointer_type":
			if t.NamedChildCount() == 0 {
				return nil
			}
			t = t.NamedChild(0)
		case "slice_type", "array_type":
			coll, t = true, x.field(t, "element")
		case "map_type":
			coll, t = true, x.field(t, "value")
		case "generic_type":
			t = x.field(t, "type")
		case "qualified_type":
			if t.NamedChildCount() != 2 {
				return nil
			}
			return []typeName{{q: x.text(t.NamedChild(0)), name: x.text(t.NamedChild(1)), coll: coll}}
		default:
			return nil
		}
	}
	return nil
}

// goReceiver is the type a method is declared on: T of (t T), (t *T), (t *T[K]).
func (x *extractor) goReceiver(m *gts.Node) string {
	recv := x.field(m, "receiver")
	if recv == nil {
		return ""
	}
	for i := 0; i < recv.NamedChildCount(); i++ {
		p := recv.NamedChild(i)
		if x.typ(p) != "parameter_declaration" {
			continue
		}
		t := x.field(p, "type")
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
				return x.text(t)
			default:
				return ""
			}
		}
	}
	return ""
}

// goInterfaceMethods are the methods an interface type declares.
func (x *extractor) goInterfaceMethods(t *gts.Node) []string {
	var out []string
	for i := 0; i < t.NamedChildCount(); i++ {
		if m := t.NamedChild(i); x.typ(m) == "method_elem" {
			out = append(out, x.fieldText(m, "name"))
		}
	}
	return out
}

// ---- TypeScript ----------------------------------------------------------

func (u *usageCollector) tsMembers(root *gts.Node) {
	x := u.x
	for i := 0; i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		exported := false
		if x.typ(n) == "export_statement" {
			exported = true
			if n = x.field(n, "declaration"); n == nil {
				continue
			}
		}
		switch x.typ(n) {
		case "class_declaration", "abstract_class_declaration":
			u.tsClass(n, exported)
		case "interface_declaration":
			name := x.fieldText(n, "name")
			body := x.field(n, "body")
			if body == nil {
				continue
			}
			for j := 0; j < body.NamedChildCount(); j++ {
				if m := body.NamedChild(j); x.typ(m) == "method_signature" {
					u.tsSignature(name+"."+tsName(x, x.field(m, "name")), exported, graph.RoleParam, m)
				}
			}
		case "function_declaration":
			name := x.fieldText(n, "name")
			u.tsSignature(name, exported, graph.RoleParam, n)
			u.tsConstructs(name, x.field(n, "body"))
		}
	}
}

func (u *usageCollector) tsClass(n *gts.Node, exported bool) {
	x := u.x
	holder := x.fieldText(n, "name")
	sym := x.symIndex(holder, int(n.StartPoint().Row)+1)
	for i := 0; i < n.NamedChildCount(); i++ {
		h := n.NamedChild(i)
		if x.typ(h) != "class_heritage" {
			continue
		}
		for j := 0; j < h.NamedChildCount(); j++ {
			clause := h.NamedChild(j)
			for k := 0; k < clause.NamedChildCount(); k++ {
				for _, tn := range x.tsTypeNames(clause.NamedChild(k)) {
					u.addImplements(sym, tn)
				}
			}
		}
	}
	body := x.field(n, "body")
	if body == nil {
		return
	}
	for j := 0; j < body.NamedChildCount(); j++ {
		m := body.NamedChild(j)
		public := !x.tsPrivate(m)
		switch x.typ(m) {
		case "public_field_definition":
			if x.hasToken(m, "static") {
				continue
			}
			u.addRefs(graph.TypeUse{Owner: holder, Member: tsName(x, x.field(m, "name")), Role: graph.RoleField,
				Exported: exported && public}, m, x.tsTypeNames(x.field(m, "type")))
		case "method_definition":
			name := tsName(x, x.field(m, "name"))
			if x.hasToken(m, "get") || x.hasToken(m, "set") {
				continue
			}
			if name == "constructor" {
				u.tsSignature(holder, exported, graph.RoleCtor, m)
				continue
			}
			u.tsSignature(holder+"."+name, exported && public, graph.RoleParam, m)
			u.tsConstructs(holder+"."+name, x.field(m, "body"))
		}
	}
}

// tsPrivate: a private / protected / #private class member.
func (x *extractor) tsPrivate(m *gts.Node) bool {
	for i := 0; i < m.NamedChildCount(); i++ {
		c := m.NamedChild(i)
		switch x.typ(c) {
		case "accessibility_modifier":
			if t := x.text(c); t == "private" || t == "protected" {
				return true
			}
		case "private_property_identifier":
			return true
		}
	}
	return false
}

func (u *usageCollector) tsSignature(owner string, exported bool, role string, n *gts.Node) {
	x := u.x
	if params := x.field(n, "parameters"); params != nil {
		for i := 0; i < params.NamedChildCount(); i++ {
			p := params.NamedChild(i)
			switch x.typ(p) {
			case "required_parameter", "optional_parameter":
			default:
				continue
			}
			pat := x.field(p, "pattern")
			if pat == nil || x.typ(pat) != "identifier" {
				continue
			}
			u.addRefs(graph.TypeUse{Owner: owner, Member: x.text(pat), Role: role, Exported: exported}, p, x.tsTypeNames(x.field(p, "type")))
		}
	}
	if role != graph.RoleCtor {
		if rt := x.field(n, "return_type"); rt != nil {
			u.addRefs(graph.TypeUse{Owner: owner, Member: "0", Role: graph.RoleResult, Exported: exported}, n, x.tsTypeNames(rt))
		}
	}
}

func (u *usageCollector) tsConstructs(owner string, body *gts.Node) {
	x := u.x
	x.walk(body, func(c *gts.Node) bool {
		if x.typ(c) == "new_expression" {
			if ctor := x.field(c, "constructor"); ctor != nil {
				switch x.typ(ctor) {
				case "identifier":
					u.addConstruct(owner, c, typeName{name: x.text(ctor)})
				case "member_expression":
					if o, p := x.field(ctor, "object"), x.field(ctor, "property"); o != nil && p != nil && x.typ(o) == "identifier" {
						u.addConstruct(owner, c, typeName{q: x.text(o), name: x.text(p)})
					}
				}
			}
		}
		return true
	})
}

// tsTypeNames are the types a TypeScript type (annotation) names: T,
// ns.T, T | null; the arguments of a generic type (Promise<T>, Map<K, V>)
// and array elements as collections.
func (x *extractor) tsTypeNames(t *gts.Node) []typeName {
	if t == nil {
		return nil
	}
	switch x.typ(t) {
	case "type_annotation":
		if t.NamedChildCount() == 0 {
			return nil
		}
		return x.tsTypeNames(t.NamedChild(0))
	case "type_identifier", "identifier":
		return []typeName{{name: x.text(t)}}
	case "nested_type_identifier", "member_expression":
		if t.NamedChildCount() == 2 && x.typ(t.NamedChild(0)) == "identifier" {
			return []typeName{{q: x.text(t.NamedChild(0)), name: x.text(t.NamedChild(1))}}
		}
	case "generic_type":
		out := x.tsTypeNames(x.field(t, "name"))
		if args := x.field(t, "type_arguments"); args != nil {
			for i := 0; i < args.NamedChildCount(); i++ {
				for _, n := range x.tsTypeNames(args.NamedChild(i)) {
					n.coll = true
					out = append(out, n)
				}
			}
		}
		return out
	case "array_type":
		var out []typeName
		for i := 0; i < t.NamedChildCount(); i++ {
			for _, n := range x.tsTypeNames(t.NamedChild(i)) {
				n.coll = true
				out = append(out, n)
			}
		}
		return out
	case "union_type", "parenthesized_type":
		var out []typeName
		for i := 0; i < t.NamedChildCount(); i++ {
			out = append(out, x.tsTypeNames(t.NamedChild(i))...)
		}
		return out
	}
	return nil
}

// tsClassShape marks an abstract class and lists its methods (not the
// constructor, accessors or static methods).
func (x *extractor) tsClassShape(n *gts.Node, sym *graph.Symbol) {
	sym.Abstract = x.typ(n) == "abstract_class_declaration"
	body := x.field(n, "body")
	if body == nil {
		return
	}
	for i := 0; i < body.NamedChildCount(); i++ {
		m := body.NamedChild(i)
		switch x.typ(m) {
		case "method_definition":
			name := tsName(x, x.field(m, "name"))
			if name == "constructor" || x.hasToken(m, "get") || x.hasToken(m, "set") || x.hasToken(m, "static") {
				continue
			}
			sym.Methods = append(sym.Methods, name)
		case "abstract_method_signature":
			sym.Methods = append(sym.Methods, tsName(x, x.field(m, "name")))
		}
	}
}

// tsInterfaceMethods are the methods an interface body declares.
func (x *extractor) tsInterfaceMethods(n *gts.Node) []string {
	body := x.field(n, "body")
	if body == nil {
		return nil
	}
	var out []string
	for i := 0; i < body.NamedChildCount(); i++ {
		if m := body.NamedChild(i); x.typ(m) == "method_signature" {
			out = append(out, tsName(x, x.field(m, "name")))
		}
	}
	return out
}

// ---- Python --------------------------------------------------------------

func (u *usageCollector) pyMembers(root *gts.Node) {
	x := u.x
	for i := 0; i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		if x.typ(n) == "decorated_definition" {
			if n = x.field(n, "definition"); n == nil {
				continue
			}
		}
		switch x.typ(n) {
		case "class_definition":
			u.pyClass(n)
		case "function_definition":
			name := x.fieldText(n, "name")
			u.pySignature(name, pyExported(name), graph.RoleParam, n)
			u.pyConstructs(name, x.field(n, "body"))
		}
	}
}

func (u *usageCollector) pyClass(n *gts.Node) {
	x := u.x
	holder := x.fieldText(n, "name")
	public := pyExported(holder)
	if bases := x.field(n, "superclasses"); bases != nil {
		sym := x.symIndex(holder, int(n.StartPoint().Row)+1)
		for i := 0; i < bases.NamedChildCount(); i++ {
			if b := bases.NamedChild(i); x.typ(b) != "keyword_argument" {
				for _, tn := range x.pyTypeNames(b) {
					u.addImplements(sym, tn)
				}
			}
		}
	}
	body := x.field(n, "body")
	if body == nil {
		return
	}
	for j := 0; j < body.NamedChildCount(); j++ {
		m := body.NamedChild(j)
		if x.typ(m) == "expression_statement" && m.NamedChildCount() == 1 {
			m = m.NamedChild(0)
		}
		if x.typ(m) == "decorated_definition" {
			if m = x.field(m, "definition"); m == nil {
				continue
			}
		}
		switch x.typ(m) {
		case "assignment": // repo: SqlRepo
			left := x.field(m, "left")
			if left == nil || x.typ(left) != "identifier" {
				continue
			}
			name := x.text(left)
			u.addRefs(graph.TypeUse{Owner: holder, Member: name, Role: graph.RoleField, Exported: public && pyExported(name)},
				m, x.pyTypeNames(x.field(m, "type")))
		case "function_definition":
			name := x.fieldText(m, "name")
			switch {
			case name == "__init__":
				u.pySignature(holder, public, graph.RoleCtor, m)
			case strings.HasPrefix(name, "__") && strings.HasSuffix(name, "__"):
				continue
			default:
				u.pySignature(holder+"."+name, public && pyExported(name), graph.RoleParam, m)
			}
			u.pyConstructs(holder+"."+name, x.field(m, "body"))
		}
	}
}

func (u *usageCollector) pySignature(owner string, exported bool, role string, n *gts.Node) {
	x := u.x
	if params := x.field(n, "parameters"); params != nil {
		for i := 0; i < params.NamedChildCount(); i++ {
			p := params.NamedChild(i)
			switch x.typ(p) {
			case "typed_parameter", "typed_default_parameter":
			default:
				continue
			}
			id := x.field(p, "name")
			if id == nil && p.NamedChildCount() > 0 {
				id = p.NamedChild(0)
			}
			if id == nil || x.typ(id) != "identifier" {
				continue
			}
			u.addRefs(graph.TypeUse{Owner: owner, Member: x.text(id), Role: role, Exported: exported}, p, x.pyTypeNames(x.field(p, "type")))
		}
	}
	if role != graph.RoleCtor {
		if rt := x.field(n, "return_type"); rt != nil {
			u.addRefs(graph.TypeUse{Owner: owner, Member: "0", Role: graph.RoleResult, Exported: exported}, n, x.pyTypeNames(rt))
		}
	}
}

// pyConstructs records calls of a capitalized name (a class, by Python's
// naming convention; the rule checks that it is one) in body.
func (u *usageCollector) pyConstructs(owner string, body *gts.Node) {
	x := u.x
	x.walk(body, func(c *gts.Node) bool {
		if x.typ(c) != "call" {
			return true
		}
		for _, tn := range x.pyTypeNames(x.field(c, "function")) {
			if tn.name != "" && tn.name[0] >= 'A' && tn.name[0] <= 'Z' {
				u.addConstruct(owner, c, tn)
			}
		}
		return true
	})
}

// pyTypeNames are the types a Python annotation (or expression) names: T,
// mod.T, Optional[T], T | None; the arguments of a generic (list[T],
// dict[K, V]) as collections.
func (x *extractor) pyTypeNames(t *gts.Node) []typeName {
	if t == nil {
		return nil
	}
	switch x.typ(t) {
	case "type":
		if t.NamedChildCount() == 1 {
			return x.pyTypeNames(t.NamedChild(0))
		}
	case "identifier":
		return []typeName{{name: x.text(t)}}
	case "attribute":
		d, ok := x.pyDotted(t)
		if !ok {
			return nil
		}
		i := strings.LastIndexByte(d, '.')
		return []typeName{{q: d[:i], name: d[i+1:]}}
	case "binary_operator": // T | None
		var out []typeName
		for _, side := range []string{"left", "right"} {
			if c := x.field(t, side); c != nil && x.typ(c) != "none" {
				out = append(out, x.pyTypeNames(c)...)
			}
		}
		return out
	case "generic_type", "subscript":
		if t.NamedChildCount() == 0 {
			return nil
		}
		head := t.NamedChild(0)
		optional := strings.HasSuffix(x.text(head), "Optional")
		out := x.pyTypeNames(head)
		for i := 1; i < t.NamedChildCount(); i++ {
			arg := t.NamedChild(i)
			var args []*gts.Node
			if x.typ(arg) == "type_parameter" {
				for k := 0; k < arg.NamedChildCount(); k++ {
					args = append(args, arg.NamedChild(k))
				}
			} else {
				args = append(args, arg)
			}
			for _, a := range args {
				for _, n := range x.pyTypeNames(a) {
					n.coll = !optional
					out = append(out, n)
				}
			}
		}
		return out
	}
	return nil
}

// pyAbstractBases are the base classes that make a class an abstraction.
var pyAbstractBases = map[string]bool{"Protocol": true, "ABC": true}

// pyDataBases are the base classes of data: values, records, models.
var pyDataBases = map[string]bool{"Enum": true, "IntEnum": true, "StrEnum": true, "Flag": true, "IntFlag": true,
	"NamedTuple": true, "TypedDict": true, "BaseModel": true, "Struct": true}

// pyDataDecorators make a class a record (dataclasses, attrs).
var pyDataDecorators = map[string]bool{"dataclass": true, "define": true, "frozen": true, "mutable": true, "s": true, "attrs": true}

// pyAccessors are the decorators of a property: an attribute, not behaviour.
var pyAccessors = map[string]bool{"property": true, "cached_property": true, "setter": true, "getter": true, "deleter": true}

// pyDecoratorName is the last name of a decorator (dataclass of
// @dataclasses.dataclass(frozen=True), setter of @dsn.setter).
func (x *extractor) pyDecoratorName(d *gts.Node) string {
	t := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(x.text(d)), "@"))
	if i := strings.IndexByte(t, '('); i >= 0 {
		t = t[:i]
	}
	return t[strings.LastIndexByte(t, '.')+1:]
}

// pyMarkData: a class decorated as a record holds data.
func (x *extractor) pyMarkData(decorated *gts.Node, sym *graph.Symbol) {
	for i := 0; i < decorated.NamedChildCount(); i++ {
		if d := decorated.NamedChild(i); x.typ(d) == "decorator" && pyDataDecorators[x.pyDecoratorName(d)] {
			sym.Data = true
		}
	}
}

// pyClassShape marks an abstract class (a Protocol / ABC base, an ABCMeta
// metaclass or an @abstractmethod) or a data class (a data base) and lists
// its methods (dunder methods and properties left out).
func (x *extractor) pyClassShape(n *gts.Node, sym *graph.Symbol) {
	last := func(c *gts.Node) string {
		if c != nil && (x.typ(c) == "subscript" || x.typ(c) == "generic_type") && c.NamedChildCount() > 0 {
			c = c.NamedChild(0)
		}
		if c == nil {
			return ""
		}
		t := x.text(c)
		return t[strings.LastIndexByte(t, '.')+1:]
	}
	if bases := x.field(n, "superclasses"); bases != nil {
		for i := 0; i < bases.NamedChildCount(); i++ {
			b := bases.NamedChild(i)
			if x.typ(b) == "keyword_argument" {
				if x.fieldText(b, "name") == "metaclass" && last(x.field(b, "value")) == "ABCMeta" {
					sym.Abstract = true
				}
				continue
			}
			sym.Abstract = sym.Abstract || pyAbstractBases[last(b)]
			sym.Data = sym.Data || pyDataBases[last(b)]
		}
	}
	body := x.field(n, "body")
	if body == nil {
		return
	}
	for i := 0; i < body.NamedChildCount(); i++ {
		m := body.NamedChild(i)
		abstract, accessor := false, false
		if x.typ(m) == "decorated_definition" {
			for j := 0; j < m.NamedChildCount(); j++ {
				if d := m.NamedChild(j); x.typ(d) == "decorator" {
					name := x.pyDecoratorName(d)
					abstract = abstract || name == "abstractmethod"
					accessor = accessor || pyAccessors[name]
				}
			}
			m = x.field(m, "definition")
		}
		if m == nil || x.typ(m) != "function_definition" {
			continue
		}
		name := x.fieldText(m, "name")
		if strings.HasPrefix(name, "__") && strings.HasSuffix(name, "__") {
			continue
		}
		sym.Abstract = sym.Abstract || abstract
		if !accessor {
			sym.Methods = append(sym.Methods, name)
		}
	}
}

// ---- resolution ----------------------------------------------------------

// memberSpecs are the specs the members need resolved besides the file's
// imports: a Python submodule reached through `from pkg import mod`.
func (u *usageCollector) memberSpecs(add func(string)) {
	if u.lang != graph.Python {
		return
	}
	for _, m := range u.members {
		if m.t.q == "" {
			continue
		}
		head, _, _ := strings.Cut(m.t.q, ".")
		if b := u.pyBind[head]; b != nil && b.fromSpec != "" {
			add(pyJoin(b.fromSpec, b.fromName) + strings.TrimPrefix(m.t.q, head))
		}
	}
}

func pyJoin(mod, name string) string {
	if strings.HasSuffix(mod, ".") { // from . import x
		return mod + name
	}
	return mod + "." + name
}

// resolveMembers resolves the members' types into fg; goBinds maps a Go
// file's package names to their directories.
func (u *usageCollector) resolveMembers(fg *graph.FileGraph, bySpec map[string]imports.Result, goBinds map[string]string) {
	file := func(spec string) string {
		if res, ok := bySpec[spec]; ok && res.Kind == imports.InRepo {
			return res.File
		}
		return ""
	}
	for _, m := range u.members {
		ref := u.resolveType(m.t, file, goBinds)
		if ref.Target == "" || ref.Type == "" {
			continue
		}
		switch {
		case m.implement >= 0:
			fg.Symbols[m.implement].Implements = append(fg.Symbols[m.implement].Implements, ref.String())
		case m.construct:
			fg.Constructs = append(fg.Constructs, graph.Construct{Owner: m.use.Owner, Line: m.use.Line, TypeRef: ref})
		default:
			use := m.use
			use.TypeRef = ref
			fg.TypeUses = append(fg.TypeUses, use)
		}
	}
}

func (u *usageCollector) resolveType(t typeName, file func(string) string, goBinds map[string]string) graph.TypeRef {
	switch u.lang {
	case graph.Go:
		if t.q != "" {
			return graph.TypeRef{Target: goBinds[t.q], Type: t.name}
		}
	case graph.TypeScript, graph.JavaScript:
		if t.q == "" {
			if b, ok := u.tsBind[t.name]; ok && !b.namespace {
				return graph.TypeRef{Target: file(b.spec), Type: b.name}
			}
		} else if b, ok := u.tsBind[t.q]; ok && b.namespace {
			return graph.TypeRef{Target: file(b.spec), Type: t.name}
		}
	case graph.Python:
		if t.q == "" {
			if b := u.pyBind[t.name]; b != nil && b.fromSpec != "" {
				return graph.TypeRef{Target: file(b.fromSpec), Type: b.fromName}
			}
			return graph.TypeRef{}
		}
		head, _, _ := strings.Cut(t.q, ".")
		b := u.pyBind[head]
		switch {
		case b == nil:
		case b.fromSpec != "":
			return graph.TypeRef{Target: file(pyJoin(b.fromSpec, b.fromName) + strings.TrimPrefix(t.q, head)), Type: t.name}
		case b.alias != "":
			return graph.TypeRef{Target: file(b.alias + strings.TrimPrefix(t.q, head)), Type: t.name}
		default:
			for _, s := range b.specs {
				if s == t.q {
					return graph.TypeRef{Target: file(s), Type: t.name}
				}
			}
		}
	}
	return graph.TypeRef{}
}
