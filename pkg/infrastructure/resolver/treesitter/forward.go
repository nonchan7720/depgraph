package treesitter

import (
	"slices"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
)

// Forwarding functions (graph.FuncCalls.Forwards): a function whose whole
// body is one call that gets exactly the function's parameters, in order,
// and whose result it returns:
//
//	func Get(id int, opts ...Opt) (*User, error) { return load(id, opts...) }
//	export const get = (id: number) => load(id);
//	def get(id, **kw): return load(id, **kw)
//
// It adds a name and nothing else. `await` in front of the call, a
// docstring, comments and Python keyword arguments named like the parameters
// (x=x) do not change that. A function whose parameters cannot all be named
// (Go `func(int)`, a destructuring pattern) is not one.

// forwards reports whether the declaration n (a function declaration, or a
// constant holding an arrow function / function expression) only forwards.
func (x *extractor) forwards(n *gts.Node) bool {
	fn := x.functionOf(n)
	if fn == nil {
		return false
	}
	ps := x.field(fn, "parameters")
	if ps == nil {
		ps = x.field(fn, "parameter") // x => f(x)
	}
	params, ok := x.paramNames(ps)
	if !ok {
		return false
	}
	call := x.onlyCall(x.field(fn, "body"))
	if call == nil {
		return false
	}
	args, ok := x.argNames(x.field(call, "arguments"))
	return ok && slices.Equal(params, args)
}

// functionOf is the function node of a declaration: the declaration
// itself, or the function a `const f = ...` declares.
func (x *extractor) functionOf(n *gts.Node) *gts.Node {
	switch x.typ(n) {
	case "function_declaration", "function_definition", "generator_function_declaration":
		return n
	case "decorated_definition": // the decorator wraps it: more than a forward
		return nil
	case "lexical_declaration", "variable_declaration":
		if n.NamedChildCount() != 1 {
			return nil
		}
		if v := x.field(n.NamedChild(0), "value"); v != nil {
			switch x.typ(v) {
			case "arrow_function", "function_expression", "function":
				return v
			}
		}
	}
	return nil
}

// paramNames are the names of a parameter list, in order (none for nil:
// no parameters); ok is false when a parameter has no single name. An arrow
// function with one bare parameter has no list: ps is that identifier.
func (x *extractor) paramNames(ps *gts.Node) (names []string, ok bool) {
	if ps == nil {
		return nil, true
	}
	if x.typ(ps) == "identifier" {
		return []string{x.text(ps)}, true
	}
	for i := 0; i < ps.NamedChildCount(); i++ {
		p := ps.NamedChild(i)
		switch x.typ(p) {
		case "comment":
			continue
		case "parameter_declaration", "variadic_parameter_declaration": // Go: a, b int
			ns := x.fieldChildren(p, "name")
			if len(ns) == 0 {
				return nil, false
			}
			for _, nm := range ns {
				names = append(names, x.text(nm))
			}
			continue
		}
		nm := x.paramName(p)
		if nm == "" {
			return nil, false
		}
		names = append(names, nm)
	}
	return names, true
}

// paramName is the name one TypeScript / JavaScript / Python parameter
// binds ("" for a pattern).
func (x *extractor) paramName(p *gts.Node) string {
	for p != nil {
		switch x.typ(p) {
		case "identifier":
			return x.text(p)
		case "required_parameter", "optional_parameter":
			p = x.field(p, "pattern")
		case "assignment_pattern":
			p = x.field(p, "left")
		case "default_parameter", "typed_default_parameter":
			p = x.field(p, "name")
		case "rest_pattern", "typed_parameter", "list_splat_pattern", "dictionary_splat_pattern":
			if p.NamedChildCount() == 0 {
				return ""
			}
			p = p.NamedChild(0)
		default:
			return ""
		}
	}
	return ""
}

// onlyCall is the call a function body consists of: the expression of an
// arrow function, or the only statement of a block (a return or an
// expression statement; a docstring and comments aside). nil otherwise.
func (x *extractor) onlyCall(body *gts.Node) *gts.Node {
	if body == nil {
		return nil
	}
	e := body
	switch x.typ(body) {
	case "block", "statement_block":
		stmts := x.statements(body)
		if len(stmts) != 1 {
			return nil
		}
		st := stmts[0]
		if t := x.typ(st); t != "return_statement" && t != "expression_statement" {
			return nil
		}
		if st.NamedChildCount() != 1 {
			return nil
		}
		e = st.NamedChild(0)
	}
	for {
		switch x.typ(e) {
		case "call_expression", "call":
			return e
		case "expression_list", "await_expression", "await", "parenthesized_expression": // Go's return list, `await f(x)`
			if e.NamedChildCount() != 1 {
				return nil
			}
			e = e.NamedChild(0)
		default:
			return nil
		}
	}
}

// statements are the statements of a block, without comments and a leading
// docstring; Go wraps them in a statement list.
func (x *extractor) statements(block *gts.Node) []*gts.Node {
	var out []*gts.Node
	for i := 0; i < block.NamedChildCount(); i++ {
		st := block.NamedChild(i)
		switch x.typ(st) {
		case "comment":
			continue
		case "statement_list":
			out = append(out, x.statements(st)...)
			continue
		}
		if len(out) == 0 && x.isDocstring(st) {
			continue
		}
		out = append(out, st)
	}
	return out
}

// isDocstring reports whether st is a statement that is only a string
// (this grammar gives a docstring as the string itself).
func (x *extractor) isDocstring(st *gts.Node) bool {
	if x.typ(st) == "expression_statement" && st.NamedChildCount() == 1 {
		st = st.NamedChild(0)
	}
	t := x.typ(st)
	return t == "string" || t == "concatenated_string"
}

// argNames are the arguments of a call when each is a plain name (also
// spread: xs..., ...xs, *args, **kwargs, or a Python keyword argument
// passing the name to itself); ok is false otherwise.
func (x *extractor) argNames(args *gts.Node) (names []string, ok bool) {
	if args == nil {
		return nil, false
	}
	for i := 0; i < args.NamedChildCount(); i++ {
		a := args.NamedChild(i)
		switch x.typ(a) {
		case "comment":
			continue
		case "keyword_argument":
			v := x.field(a, "value")
			if v == nil || x.typ(v) != "identifier" || x.fieldText(a, "name") != x.text(v) {
				return nil, false
			}
			a = v
		case "variadic_argument", "spread_element", "list_splat", "dictionary_splat":
			if a.NamedChildCount() != 1 {
				return nil, false
			}
			a = a.NamedChild(0)
		}
		if x.typ(a) != "identifier" {
			return nil, false
		}
		names = append(names, strings.TrimSpace(x.text(a)))
	}
	return names, true
}
