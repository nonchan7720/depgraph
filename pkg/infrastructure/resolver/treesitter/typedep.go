package treesitter

import (
	"strings"

	gts "github.com/odvcencio/gotreesitter"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver/imports"
)

// Type dependencies (graph.TypeDep, the input of concrete-dependency): what
// the structs and classes of a file are handed in, by type.
//
//	Go          fields of top-level structs (named and embedded) and the
//	            parameters of New* functions: T, *T and T[X] of an imported
//	            package (q.T); collections and the own package's types are
//	            not dependencies of this kind
//	TypeScript  class properties and constructor parameters (parameter
//	            properties too) typed with an imported name (T, T<X>, ns.T)
//	Python      class-level annotations and __init__ parameters typed with
//	            an imported name (T, mod.T)
//
// A reference is resolved with the file's imports like a use (usage.go); one
// whose type is not declared in the repository is dropped.

// typeRef is one dependency before its qualifier is resolved: q is the
// package / namespace / module the type is written through ("" for a bare
// name), name the type as written.
type typeRef struct {
	holder, member string
	line           int
	q, name        string
}

func (u *usageCollector) addTypeRef(holder, member string, at *gts.Node, q, name string) {
	if holder == "" || member == "" || name == "" {
		return
	}
	u.typeRefs = append(u.typeRefs, typeRef{holder: holder, member: member, line: int(at.StartPoint().Row) + 1, q: q, name: name})
}

// fieldChildren are the children of n in field name (a field_declaration
// has one name per declared field).
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

func (u *usageCollector) goTypeRefs(root *gts.Node) {
	x := u.x
	for i := 0; i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		switch x.typ(n) {
		case "type_declaration":
			for j := 0; j < n.NamedChildCount(); j++ {
				ts := n.NamedChild(j)
				st := x.field(ts, "type")
				if x.typ(ts) != "type_spec" || st == nil || x.typ(st) != "struct_type" {
					continue
				}
				holder := x.fieldText(ts, "name")
				x.forEachDescendant(st, "field_declaration", func(fd *gts.Node) {
					q, name := x.goTypeName(x.field(fd, "type"))
					names := x.fieldChildren(fd, "name")
					if len(names) == 0 { // embedded
						u.addTypeRef(holder, name, fd, q, name)
					}
					for _, nm := range names {
						u.addTypeRef(holder, x.text(nm), fd, q, name)
					}
				})
			}
		case "function_declaration":
			name := x.fieldText(n, "name")
			params := x.field(n, "parameters")
			if !strings.HasPrefix(name, "New") || params == nil {
				continue
			}
			for j := 0; j < params.NamedChildCount(); j++ {
				p := params.NamedChild(j)
				if x.typ(p) != "parameter_declaration" {
					continue
				}
				q, typ := x.goTypeName(x.field(p, "type"))
				for _, nm := range x.fieldChildren(p, "name") {
					u.addTypeRef(name, x.text(nm), p, q, typ)
				}
			}
		}
	}
}

// unwrapType follows a type through what wraps the named type: a pointer
// (*T), a generic instantiation (T[X], T<X>) and a TypeScript annotation.
func (x *extractor) unwrapType(t *gts.Node) *gts.Node {
	for t != nil {
		switch x.typ(t) {
		case "pointer_type", "type_annotation":
			if t.NamedChildCount() == 0 {
				return nil
			}
			t = t.NamedChild(0)
		case "generic_type":
			if g := x.field(t, "type"); g != nil { // Go
				t = g
			} else { // TypeScript
				t = x.field(t, "name")
			}
		default:
			return t
		}
	}
	return nil
}

// goTypeName is the package qualifier and name of a type written q.T, *q.T
// or q.T[X]; "" for any other type.
func (x *extractor) goTypeName(t *gts.Node) (q, name string) {
	if t = x.unwrapType(t); t == nil || x.typ(t) != "qualified_type" || t.NamedChildCount() != 2 {
		return "", ""
	}
	return x.text(t.NamedChild(0)), x.text(t.NamedChild(1))
}

// goReceiver is the type a method is declared on: T of (t T), (t *T),
// (t *T[K]).
func (x *extractor) goReceiver(m *gts.Node) string {
	recv := x.field(m, "receiver")
	if recv == nil {
		return ""
	}
	for i := 0; i < recv.NamedChildCount(); i++ {
		if p := recv.NamedChild(i); x.typ(p) == "parameter_declaration" {
			if t := x.unwrapType(x.field(p, "type")); t != nil && x.typ(t) == "type_identifier" {
				return x.text(t)
			}
		}
	}
	return ""
}

// ---- TypeScript ----------------------------------------------------------

func (u *usageCollector) tsTypeRefs(root *gts.Node) {
	x := u.x
	for i := 0; i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		if x.typ(n) == "export_statement" {
			if n = x.field(n, "declaration"); n == nil {
				continue
			}
		}
		switch x.typ(n) {
		case "class_declaration", "abstract_class_declaration":
		default:
			continue
		}
		holder := x.fieldText(n, "name")
		body := x.field(n, "body")
		if body == nil {
			continue
		}
		for j := 0; j < body.NamedChildCount(); j++ {
			m := body.NamedChild(j)
			switch x.typ(m) {
			case "public_field_definition":
				if x.hasToken(m, "static") {
					continue
				}
				q, name := x.tsTypeName(x.field(m, "type"))
				u.addTypeRef(holder, tsName(x, x.field(m, "name")), m, q, name)
			case "method_definition":
				if x.fieldText(m, "name") != "constructor" {
					continue
				}
				params := x.field(m, "parameters")
				if params == nil {
					continue
				}
				for k := 0; k < params.NamedChildCount(); k++ {
					p := params.NamedChild(k)
					switch x.typ(p) {
					case "required_parameter", "optional_parameter":
					default:
						continue
					}
					pat := x.field(p, "pattern")
					if pat == nil || x.typ(pat) != "identifier" {
						continue
					}
					q, name := x.tsTypeName(x.field(p, "type"))
					u.addTypeRef(holder, x.text(pat), p, q, name)
				}
			}
		}
	}
}

// tsTypeName is the namespace and name of a type annotation written T,
// T<X> or ns.T; "" for any other type (arrays, unions, literals, ...).
func (x *extractor) tsTypeName(t *gts.Node) (q, name string) {
	switch t = x.unwrapType(t); {
	case t == nil:
	case x.typ(t) == "type_identifier":
		return "", x.text(t)
	case x.typ(t) == "nested_type_identifier" && t.NamedChildCount() == 2 && x.typ(t.NamedChild(0)) == "identifier":
		return x.text(t.NamedChild(0)), x.text(t.NamedChild(1))
	}
	return "", ""
}

// tsClassShape marks an abstract class and counts its methods (not the
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
			if x.fieldText(m, "name") == "constructor" || x.hasToken(m, "get") || x.hasToken(m, "set") || x.hasToken(m, "static") {
				continue
			}
			sym.Methods++
		case "abstract_method_signature":
			sym.Methods++
		}
	}
}

// ---- Python --------------------------------------------------------------

func (u *usageCollector) pyTypeRefs(root *gts.Node) {
	x := u.x
	for i := 0; i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		if x.typ(n) == "decorated_definition" {
			if n = x.field(n, "definition"); n == nil {
				continue
			}
		}
		if x.typ(n) != "class_definition" {
			continue
		}
		holder := x.fieldText(n, "name")
		body := x.field(n, "body")
		if body == nil {
			continue
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
			case "assignment": // repo: SqlUserRepo
				left := x.field(m, "left")
				if left == nil || x.typ(left) != "identifier" {
					continue
				}
				q, name := x.pyTypeName(x.field(m, "type"))
				u.addTypeRef(holder, x.text(left), m, q, name)
			case "function_definition":
				if x.fieldText(m, "name") != "__init__" {
					continue
				}
				params := x.field(m, "parameters")
				if params == nil {
					continue
				}
				for k := 0; k < params.NamedChildCount(); k++ {
					p := params.NamedChild(k)
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
					q, name := x.pyTypeName(x.field(p, "type"))
					u.addTypeRef(holder, x.text(id), p, q, name)
				}
			}
		}
	}
}

// pyTypeName is the module path and name of an annotation written T or
// mod.T (a.b.T); "" for any other (strings, subscripts such as list[T]).
func (x *extractor) pyTypeName(t *gts.Node) (q, name string) {
	if t != nil && x.typ(t) == "type" && t.NamedChildCount() == 1 {
		t = t.NamedChild(0)
	}
	if t == nil {
		return "", ""
	}
	switch x.typ(t) {
	case "identifier":
		return "", x.text(t)
	case "attribute":
		d, ok := x.pyDotted(t)
		if !ok {
			return "", ""
		}
		i := strings.LastIndexByte(d, '.')
		return d[:i], d[i+1:]
	}
	return "", ""
}

// pyAbstractBases are the base classes that make a class an abstraction.
var pyAbstractBases = map[string]bool{"Protocol": true, "ABC": true}

// pyClassShape marks an abstract class (a Protocol / ABC base, an ABCMeta
// metaclass or an @abstractmethod) and counts its methods (dunder methods
// left out).
func (x *extractor) pyClassShape(n *gts.Node, sym *graph.Symbol) {
	last := func(c *gts.Node) string {
		if c != nil && x.typ(c) == "subscript" { // Protocol[T]
			c = x.field(c, "value")
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
			if pyAbstractBases[last(b)] {
				sym.Abstract = true
			}
		}
	}
	body := x.field(n, "body")
	if body == nil {
		return
	}
	for i := 0; i < body.NamedChildCount(); i++ {
		m := body.NamedChild(i)
		abstract := false
		if x.typ(m) == "decorated_definition" {
			for j := 0; j < m.NamedChildCount(); j++ {
				if d := m.NamedChild(j); x.typ(d) == "decorator" && strings.HasSuffix(strings.TrimSpace(x.text(d)), "abstractmethod") {
					abstract = true
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
		sym.Methods++
		sym.Abstract = sym.Abstract || abstract
	}
}

// ---- resolution ----------------------------------------------------------

// typeRefSpecs are the specs a file's type references need resolved besides
// its imports: a Python submodule reached through `from pkg import mod`.
func (u *usageCollector) typeRefSpecs(add func(string)) {
	if u.lang != graph.Python {
		return
	}
	for _, r := range u.typeRefs {
		if r.q == "" {
			continue
		}
		head, _, _ := strings.Cut(r.q, ".")
		if b := u.pyBind[head]; b != nil && b.fromSpec != "" {
			add(pyJoin(b.fromSpec, b.fromName) + strings.TrimPrefix(r.q, head))
		}
	}
}

func pyJoin(mod, name string) string {
	if strings.HasSuffix(mod, ".") { // from . import x
		return mod + name
	}
	return mod + "." + name
}

// typeDeps resolves the type references; goBinds maps a Go file's package
// names to their directories.
func (u *usageCollector) typeDeps(bySpec map[string]imports.Result, goBinds map[string]string) []graph.TypeDep {
	var out []graph.TypeDep
	add := func(r typeRef, target, typ string) {
		if target != "" && typ != "" && typ != "default" {
			out = append(out, graph.TypeDep{Holder: r.holder, Member: r.member, Line: r.line, Target: target, Type: typ})
		}
	}
	file := func(spec string) string {
		if res, ok := bySpec[spec]; ok && res.Kind == imports.InRepo {
			return res.File
		}
		return ""
	}
	for _, r := range u.typeRefs {
		switch u.lang {
		case graph.Go:
			if r.q != "" {
				add(r, goBinds[r.q], r.name)
			}
		case graph.TypeScript, graph.JavaScript:
			if r.q == "" {
				if b, ok := u.tsBind[r.name]; ok && !b.namespace {
					add(r, file(b.spec), b.name)
				}
			} else if b, ok := u.tsBind[r.q]; ok && b.namespace {
				add(r, file(b.spec), r.name)
			}
		case graph.Python:
			if r.q == "" {
				if b := u.pyBind[r.name]; b != nil && b.fromSpec != "" {
					add(r, file(b.fromSpec), b.fromName)
				}
				continue
			}
			head, _, _ := strings.Cut(r.q, ".")
			b := u.pyBind[head]
			switch {
			case b == nil:
			case b.fromSpec != "":
				add(r, file(pyJoin(b.fromSpec, b.fromName)+strings.TrimPrefix(r.q, head)), r.name)
			case b.alias != "":
				add(r, file(b.alias+strings.TrimPrefix(r.q, head)), r.name)
			default:
				for _, s := range b.specs {
					if s == r.q {
						add(r, file(s), r.name)
					}
				}
			}
		}
	}
	return out
}
