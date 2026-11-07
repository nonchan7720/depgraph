package treesitter

import (
	"path"
	"strings"

	gts "github.com/odvcencio/gotreesitter"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
)

// tsBinding is a local name bound by an import.
type tsBinding struct {
	spec      string
	name      string // imported name ("default", "a"); "" for a namespace
	namespace bool
}

// TypeScript / JavaScript usage:
//
//   - import D, { a, b as c } from './x'   -> x#default, x#a, x#b
//   - import * as ns from './y'; ns.f      -> y#f (y#* when ns is used as a value)
//   - import './side'                      -> side# (the file only)
//   - export { a as b } from './w'         -> re-export w#a as b
//   - export * from './z'                  -> re-export z#* as *
//   - import('./d'), require('./r')        -> d#*, r#*
//   - import(`./dir/${x}`), require.context('./dir'), import.meta.glob('./dir/*')
//     -> every file under dir
//   - './path' in any other string          -> the file only
func (u *usageCollector) tsFile(root *gts.Node) {
	x := u.x
	bind := map[string]tsBinding{}
	// 1. imports and re-exports (top level only)
	for i := 0; i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		switch x.typ(n) {
		case "import_statement":
			u.tsImport(n, bind)
		}
	}
	for i := 0; i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		if x.typ(n) == "export_statement" {
			u.tsExport(n, bind)
		}
	}
	// 2. symbols of this file, for uses inside it
	own := map[string]bool{}
	for _, s := range x.fg.Symbols {
		own[s.Name] = true
	}
	// 3. every other node
	for i := 0; i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		if x.typ(n) == "import_statement" {
			if c := tsChildOfType(x, n, "import_require_clause"); c != nil {
				u.tsRequireClause(c)
			}
			continue
		}
		if x.typ(n) == "export_statement" {
			// `export { a }` / `export default a` exports a, it does not use
			// it: only a declaration or an expression is walked
			if d := x.field(n, "declaration"); d != nil {
				n = d
			} else if v := x.field(n, "value"); v != nil && x.typ(v) != "identifier" {
				n = v
			} else {
				continue
			}
		}
		declared := x.declNames(n)
		x.walk(n, func(c *gts.Node) bool { return u.tsNode(c, bind, own, declared) })
	}
}

func tsChildOfType(x *extractor, n *gts.Node, typ string) *gts.Node {
	for i := 0; i < n.NamedChildCount(); i++ {
		if c := n.NamedChild(i); x.typ(c) == typ {
			return c
		}
	}
	return nil
}

// `import x = require('./y')`
func (u *usageCollector) tsRequireClause(c *gts.Node) {
	x := u.x
	for i := 0; i < c.NamedChildCount(); i++ {
		if s, ok := x.stringContent(c.NamedChild(i)); ok {
			u.use(s, "*")
		}
	}
}

func (u *usageCollector) tsImport(n *gts.Node, bind map[string]tsBinding) {
	x := u.x
	src := x.field(n, "source")
	if src == nil {
		if c := tsChildOfType(x, n, "import_require_clause"); c != nil {
			u.tsRequireClause(c)
		}
		return
	}
	spec, ok := x.stringContent(src)
	if !ok {
		return
	}
	clause := tsChildOfType(x, n, "import_clause")
	if clause == nil {
		u.use(spec, "") // side-effect import
		return
	}
	u.use(spec, "")
	for i := 0; i < clause.NamedChildCount(); i++ {
		c := clause.NamedChild(i)
		switch x.typ(c) {
		case "identifier": // default import
			bind[x.text(c)] = tsBinding{spec: spec, name: "default"}
			u.use(spec, "default")
		case "namespace_import":
			if id := tsChildOfType(x, c, "identifier"); id != nil {
				bind[x.text(id)] = tsBinding{spec: spec, namespace: true}
			}
		case "named_imports":
			for j := 0; j < c.NamedChildCount(); j++ {
				sp := c.NamedChild(j)
				if x.typ(sp) != "import_specifier" {
					continue
				}
				name := tsName(x, x.field(sp, "name"))
				local := name
				if a := x.field(sp, "alias"); a != nil {
					local = x.text(a)
				}
				if name == "" {
					continue
				}
				bind[local] = tsBinding{spec: spec, name: name}
				u.use(spec, name)
			}
		}
	}
}

// tsName is an identifier or a string module export name (`{ "a-b" as c }`).
func tsName(x *extractor, n *gts.Node) string {
	if n == nil {
		return ""
	}
	if s, ok := x.stringContent(n); ok {
		return s
	}
	return x.text(n)
}

func (u *usageCollector) tsExport(n *gts.Node, bind map[string]tsBinding) {
	x := u.x
	spec := ""
	if src := x.field(n, "source"); src != nil {
		spec, _ = x.stringContent(src)
	}
	clause := tsChildOfType(x, n, "export_clause")
	switch {
	case spec != "" && clause == nil:
		if ns := tsChildOfType(x, n, "namespace_export"); ns != nil {
			// export * as ns from './x'
			u.reExport(spec, "*", tsName(x, ns.NamedChild(0)))
		} else {
			u.reExport(spec, "*", "*")
		}
	case spec != "":
		for i := 0; i < clause.NamedChildCount(); i++ {
			sp := clause.NamedChild(i)
			if x.typ(sp) != "export_specifier" {
				continue
			}
			name := tsName(x, x.field(sp, "name"))
			as := name
			if a := x.field(sp, "alias"); a != nil {
				as = tsName(x, a)
			}
			u.reExport(spec, name, as)
		}
	case clause != nil:
		// export { a, b as c } of local or imported names
		for i := 0; i < clause.NamedChildCount(); i++ {
			sp := clause.NamedChild(i)
			if x.typ(sp) != "export_specifier" {
				continue
			}
			local := tsName(x, x.field(sp, "name"))
			as := local
			if a := x.field(sp, "alias"); a != nil {
				as = tsName(x, a)
			}
			u.tsExportLocal(local, as, bind)
		}
	case x.hasToken(n, "default"):
		if d := x.field(n, "declaration"); d != nil {
			// export default function foo() {} / class Foo {}
			if nm := x.fieldText(d, "name"); nm != "" {
				u.exports = append(u.exports, graph.ExportName{Name: "default", Local: nm})
			}
			return
		}
		if v := x.field(n, "value"); v != nil && x.typ(v) == "identifier" {
			u.tsExportLocal(x.text(v), "default", bind)
			return
		}
		// `export default bar` parses without a field in some grammars
		if id := tsChildOfType(x, n, "identifier"); id != nil {
			u.tsExportLocal(x.text(id), "default", bind)
		}
	}
}

// tsExportLocal records `export { local as as }` of a declared or imported name.
func (u *usageCollector) tsExportLocal(local, as string, bind map[string]tsBinding) {
	if local == "" {
		return
	}
	if b, ok := bind[local]; ok {
		if b.namespace {
			u.reExport(b.spec, "*", as)
		} else {
			u.reExport(b.spec, b.name, as)
		}
		return
	}
	if as != local {
		u.exports = append(u.exports, graph.ExportName{Name: as, Local: local})
	} else {
		// `function f() {}; export { f }`: f is exported although its
		// declaration is not
		u.exports = append(u.exports, graph.ExportName{Name: local, Local: local})
	}
}

// tsNode handles one node of the walk; it returns whether to descend.
func (u *usageCollector) tsNode(n *gts.Node, bind map[string]tsBinding, own, declared map[string]bool) bool {
	x := u.x
	switch x.typ(n) {
	case "call_expression":
		u.tsCall(n)
		return true
	case "string":
		// a path in a string ('./worker.ts' of new URL(), a bundler entry in a
		// config) loads that file. A glob does not: tailwind's content globs
		// only scan files (import.meta.glob / require.context are handled
		// in tsCall).
		if s, ok := x.stringContent(n); ok && isRelPath(s) && !strings.ContainsAny(s, "*{") {
			u.use(s, "")
		}
		return false
	case "member_expression":
		obj := x.field(n, "object")
		if obj != nil && x.typ(obj) == "identifier" {
			if b, ok := bind[x.text(obj)]; ok && b.namespace {
				if p := x.field(n, "property"); p != nil {
					u.use(b.spec, x.text(p))
				}
				return false
			}
		}
		return true
	case "nested_type_identifier", "nested_identifier":
		// ns.Type in a type, ns.Comp in JSX
		if n.NamedChildCount() == 2 {
			m, nm := n.NamedChild(0), n.NamedChild(1)
			if x.typ(m) == "identifier" {
				if b, ok := bind[x.text(m)]; ok && b.namespace {
					u.use(b.spec, x.text(nm))
					return false
				}
			}
		}
		return true
	case "identifier", "type_identifier", "shorthand_property_identifier":
		name := x.text(n)
		if b, ok := bind[name]; ok && b.namespace {
			u.use(b.spec, "*") // the namespace object escapes
		}
		if own[name] && !declared[name] {
			u.self(u.rel, name)
		}
		return false
	}
	return true
}

func (u *usageCollector) tsCall(n *gts.Node) {
	x := u.x
	fn := x.field(n, "function")
	args := x.field(n, "arguments")
	if fn == nil || args == nil || args.NamedChildCount() == 0 {
		return
	}
	first := args.NamedChild(0)
	switch {
	case x.typ(fn) == "import" || (x.typ(fn) == "identifier" && x.text(fn) == "require"):
		if s, ok := x.stringContent(first); ok {
			u.use(s, "*")
		} else if x.typ(first) == "template_string" {
			u.tsPrefix(x.staticPrefix(first))
		}
	case x.typ(fn) == "member_expression":
		// require.context('./dir'), import.meta.glob('./dir/*.ts' | [...])
		switch t := x.text(fn); t {
		case "require.context", "import.meta.glob", "import.meta.globEager":
			var strs []*gts.Node
			if x.typ(first) == "array" {
				for i := 0; i < first.NamedChildCount(); i++ {
					strs = append(strs, first.NamedChild(i))
				}
			} else {
				strs = append(strs, first)
			}
			for _, s := range strs {
				if v, ok := x.stringContent(s); ok {
					if t == "require.context" {
						v = strings.TrimSuffix(v, "/") + "/"
					}
					u.tsPrefix(v)
				}
			}
		}
	}
}

// tsPrefix records every file under the directory part of a path pattern
// ("./locales/" of "./locales/*.json", "./pages/" of `./pages/${p}`,
// "@/widgets" of `@/widgets/${w}.tsx`).
func (u *usageCollector) tsPrefix(p string) {
	if !isRelPath(p) {
		// under an alias or a package (`@/widgets/${name}`): the directory
		// is resolved like an import
		if i := strings.LastIndex(p, "/"); i > 0 && !strings.ContainsAny(p[:i], "*{") {
			u.usePrefix(p[:i])
		}
		return
	}
	if i := strings.IndexAny(p, "*{"); i >= 0 {
		p = p[:i]
	}
	dir := p
	if !strings.HasSuffix(dir, "/") {
		dir = path.Dir(dir)
	}
	d := path.Clean(path.Join(path.Dir(u.rel), dir))
	if d == ".." || strings.HasPrefix(d, "../") {
		return
	}
	u.tsPrefixes = append(u.tsPrefixes, d)
}

// tsPrefixUses turns the recorded directory prefixes into uses.
func (u *usageCollector) tsPrefixUses(root string) []graph.Use {
	var out []graph.Use
	for _, d := range u.tsPrefixes {
		if isDir(root, d) {
			t := d + "/"
			if d == "." {
				t = ""
			}
			out = append(out, graph.Use{Target: t, Name: "*"})
		}
	}
	return out
}

func isRelPath(s string) bool { return strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") }
