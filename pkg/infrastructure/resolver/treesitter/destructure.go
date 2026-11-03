package treesitter

import (
	gts "github.com/odvcencio/gotreesitter"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
)

// addDeclarator adds the symbol(s) a TS/JS variable_declarator declares:
// one per bound name for a destructuring pattern
// (`const { a, b: c, ...rest } = x`, `const [d, , e = 1] = y`).
func (x *extractor) addDeclarator(d *gts.Node, kind string, exported bool) {
	name := x.field(d, "name")
	if name == nil {
		return
	}
	switch x.typ(name) {
	case "object_pattern", "array_pattern":
		for _, id := range x.patternNames(name) {
			x.addSymAt(d, id, "variable", exported)
		}
	default:
		x.addSym(d, kind, x.text(name), exported)
	}
}

// patternNames are the identifiers a destructuring pattern binds, in order.
func (x *extractor) patternNames(n *gts.Node) []*gts.Node {
	var out []*gts.Node
	var walk func(*gts.Node)
	walk = func(c *gts.Node) {
		switch x.typ(c) {
		case "identifier", "shorthand_property_identifier_pattern":
			out = append(out, c)
			return
		case "pair_pattern": // { key: value }: value binds
			walk(x.field(c, "value"))
			return
		case "assignment_pattern", "object_assignment_pattern": // a = default
			walk(x.field(c, "left"))
			return
		}
		for i := 0; i < c.NamedChildCount(); i++ {
			walk(c.NamedChild(i))
		}
	}
	if n != nil {
		walk(n)
	}
	return out
}

// addSymAt is addSym with the name taken from (and located at) id.
func (x *extractor) addSymAt(decl, id *gts.Node, kind string, exported bool) {
	if id == nil {
		return
	}
	p := id.StartPoint()
	x.fg.Symbols = append(x.fg.Symbols, graph.Symbol{
		Name:     x.text(id),
		Kind:     kind,
		Exported: exported,
		Line:     int(decl.StartPoint().Row) + 1,
		EndLine:  int(decl.EndPoint().Row) + 1,
		NameLine: int(p.Row) + 1,
		Col:      utf16Col(x.src, int(id.StartByte()), int(p.Column)),
	})
}
