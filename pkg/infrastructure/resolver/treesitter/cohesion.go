package treesitter

import (
	"sort"

	gts "github.com/odvcencio/gotreesitter"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
)

// How the parts of a package or module hold together (low-cohesion):
//
//	Go          the package-level names a file declares (Usage.Decls) and the
//	            names it uses (Usage.Idents): files of a package are linked
//	            when one uses a name another declares
//	TypeScript  each top-level declaration with the other top-level names of
//	Python      the file it refers to (Usage.DeclRefs)

func (u *usageCollector) goCohesion(root *gts.Node) {
	x := u.x
	decls := map[string]bool{}
	idents := map[string]bool{}
	for i := 0; i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		switch x.typ(n) {
		case "package_clause", "import_declaration", "comment":
			continue
		case "function_declaration":
			decls[x.fieldText(n, "name")] = true
		case "type_declaration", "var_declaration", "const_declaration":
			x.walk(n, func(c *gts.Node) bool {
				switch x.typ(c) {
				case "type_spec", "type_alias", "var_spec", "const_spec":
					for _, nm := range x.fieldChildren(c, "name") {
						decls[x.text(nm)] = true
					}
					return false
				}
				return true // the declaration and its spec lists
			})
		}
		x.walk(n, func(c *gts.Node) bool {
			switch x.typ(c) {
			case "identifier", "type_identifier":
				idents[x.text(c)] = true
				return false
			case "selector_expression": // x.Sel: only the operand may be a package-level name
				if op := x.field(c, "operand"); op != nil {
					x.walk(op, func(o *gts.Node) bool {
						if t := x.typ(o); t == "identifier" || t == "type_identifier" {
							idents[x.text(o)] = true
							return false
						}
						return true
					})
				}
				return false
			case "qualified_type": // pkg.T: another package's
				return false
			}
			return true
		})
	}
	delete(decls, "")
	delete(decls, "_")
	u.goDecls, u.goIdents = sortedSet(decls), sortedSet(idents)
}

func sortedSet(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// declRefs records, for each top-level declaration of a TypeScript /
// Python file, the other top-level names of the file it refers to.
func (u *usageCollector) declRefs(root *gts.Node) {
	x := u.x
	type decl struct {
		names []string
		node  *gts.Node
	}
	var decls []decl
	top := map[string]bool{}
	for i := 0; i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		switch x.typ(n) {
		case "import_statement", "import_from_statement", "comment":
			continue
		case "export_statement":
			if d := x.field(n, "declaration"); d != nil {
				n = d
			} else {
				continue
			}
		}
		var names []string
		if u.lang == graph.Python && (x.typ(n) == "expression_statement" || x.typ(n) == "assignment") {
			a := n
			if x.typ(n) == "expression_statement" && n.NamedChildCount() == 1 {
				a = n.NamedChild(0)
			}
			if x.typ(a) == "assignment" {
				if l := x.field(a, "left"); l != nil && x.typ(l) == "identifier" {
					names = []string{x.text(l)}
				}
			}
		} else {
			for nm := range x.declNames(n) {
				names = append(names, nm)
			}
		}
		if len(names) == 0 {
			continue
		}
		sort.Strings(names)
		for _, nm := range names {
			top[nm] = true
		}
		decls = append(decls, decl{names, n})
	}
	for _, d := range decls {
		own := map[string]bool{}
		for _, nm := range d.names {
			own[nm] = true
		}
		refs := map[string]bool{}
		x.walk(d.node, func(c *gts.Node) bool {
			switch x.typ(c) {
			case "identifier", "type_identifier":
				if t := x.text(c); top[t] && !own[t] {
					refs[t] = true
				}
				return false
			}
			return true
		})
		for _, nm := range d.names {
			u.declRefList = append(u.declRefList, graph.DeclRef{Name: nm, Refs: sortedSet(refs)})
		}
	}
}
