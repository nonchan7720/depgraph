package treesitter

import (
	"strings"

	gts "github.com/odvcencio/gotreesitter"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
)

// Ruby declarations. Ruby has no exports: every class, module and constant
// is reachable by its name from anywhere, so they are named by their full
// constant path ("Admin::User", from the lexical nesting) and are exported.
// Methods are "Class#name" / "Class.name" (singleton), exported unless
// private / protected; they are compared by duplicate-code but never
// reported as unused (callbacks, routes, views and send call them by name).
// A module that only holds other classes and modules is a namespace and not
// exported: there is nothing in it to use. Declarations wrapped in a
// condition (unless defined?(X) ... end) or begin ... end count.
//
// Definitions in the blocks of an ActiveSupport::Concern (included,
// class_methods, ...) belong to the module.

// rbBlockCalls: calls whose block defines methods of the enclosing class /
// module (true: as singleton methods).
var rbBlockCalls = map[string]bool{
	"included": false, "extended": false, "prepended": false, "class_eval": false,
	"class_methods": true,
}

// rbDecls declares the statements of body; levels is the lexical nesting
// (innermost first, full names), singleton whether a def defines a
// singleton method (class << self).
func (x *extractor) rbDecls(body *gts.Node, levels []string, singleton bool) {
	if body == nil {
		return
	}
	vis := "public"
	for i := 0; i < body.NamedChildCount(); i++ {
		x.rbDecl(body.NamedChild(i), levels, singleton, &vis)
	}
}

func (x *extractor) rbDecl(n *gts.Node, levels []string, singleton bool, vis *string) {
	outer := ""
	if len(levels) > 0 {
		outer = levels[0]
	}
	switch x.typ(n) {
	case "class", "module":
		fq := rbQualify(outer, x.fieldText(n, "name"))
		if fq == "" {
			return
		}
		kind := x.typ(n)
		body := x.field(n, "body")
		// a module, or a class without a superclass, that only nests others
		// reopens a namespace
		// a class can always be instantiated or subclassed
		exported := kind == "class" || !x.rbNamespaceOnly(body)
		x.addSym(n, kind, fq, exported)
		// a class is compared by its methods (duplicate-code), not as a whole
		last := &x.fg.Symbols[len(x.fg.Symbols)-1]
		last.Tokens, last.CloneHash, last.Fingerprints = 0, "", nil
		x.rbDecls(body, append([]string{fq}, levels...), false)
	case "singleton_class":
		x.rbDecls(x.field(n, "body"), levels, true)
	case "if", "unless", "if_modifier", "unless_modifier", "then", "else", "elsif", "begin":
		for i := 0; i < n.NamedChildCount(); i++ {
			x.rbDecl(n.NamedChild(i), levels, singleton, vis)
		}
	case "method":
		name := x.fieldText(n, "name")
		switch {
		case outer == "":
			x.addSym(n, "function", name, true)
		case singleton:
			x.addSym(n, "method", outer+"."+name, *vis == "public")
		default:
			x.addSym(n, "method", outer+"#"+name, *vis == "public")
		}
	case "singleton_method":
		name := x.fieldText(n, "name")
		if outer != "" {
			name = outer + "." + name
		}
		x.addSym(n, "method", name, true)
	case "assignment":
		l := x.field(n, "left")
		if l == nil || x.typ(l) != "constant" {
			return
		}
		kind := "constant"
		if r := x.field(n, "right"); r != nil && x.typ(r) == "call" {
			switch x.fieldText(r, "receiver") + "." + x.fieldText(r, "method") {
			case "Struct.new", "Class.new", "Data.define", "Module.new":
				kind = "class"
			}
		}
		x.fg.Symbols = append(x.fg.Symbols, x.rbSymbol(n, l, kind, rbQualify(outer, x.text(l))))
	case "identifier":
		switch t := x.text(n); t {
		case "private", "protected", "public":
			*vis = t
		}
	case "call":
		if x.field(n, "receiver") != nil {
			return
		}
		m := x.fieldText(n, "method")
		switch m {
		case "private", "protected", "public":
			if args := x.field(n, "arguments"); args != nil {
				v := m
				for i := 0; i < args.NamedChildCount(); i++ {
					x.rbDecl(args.NamedChild(i), levels, singleton, &v)
				}
			}
			return
		}
		if sing, ok := rbBlockCalls[m]; ok && outer != "" {
			if b := x.field(n, "block"); b != nil {
				x.rbDecls(x.rbBlockBody(b), levels, sing || singleton)
			}
		}
	}
}

// rbSymbol is a symbol for the constant assignment n (named by id).
func (x *extractor) rbSymbol(n, id *gts.Node, kind, name string) graph.Symbol {
	p := id.StartPoint()
	return graph.Symbol{
		Name: name, Kind: kind, Exported: true,
		Line: int(n.StartPoint().Row) + 1, EndLine: int(n.EndPoint().Row) + 1,
		NameLine: int(p.Row) + 1, Col: utf16Col(x.src, int(id.StartByte()), int(p.Column)),
	}
}

// rbBlockBody is the statements of a do ... end / { } block.
func (x *extractor) rbBlockBody(b *gts.Node) *gts.Node {
	if body := x.field(b, "body"); body != nil {
		return body
	}
	for i := 0; i < b.NamedChildCount(); i++ {
		switch c := b.NamedChild(i); x.typ(c) {
		case "body_statement", "block_body":
			return c
		}
	}
	return nil
}

// rbNamespaceOnly reports whether a module body holds nothing but classes,
// modules and comments.
func (x *extractor) rbNamespaceOnly(body *gts.Node) bool {
	if body == nil {
		return true
	}
	for i := 0; i < body.NamedChildCount(); i++ {
		switch x.typ(body.NamedChild(i)) {
		case "class", "module", "comment":
		default:
			return false
		}
	}
	return true
}

// rbQualify is the full name of a constant written as name inside outer
// ("::A" is absolute).
func rbQualify(outer, name string) string {
	if a, ok := strings.CutPrefix(name, "::"); ok {
		return a
	}
	if outer == "" || name == "" {
		return name
	}
	return outer + "::" + name
}
