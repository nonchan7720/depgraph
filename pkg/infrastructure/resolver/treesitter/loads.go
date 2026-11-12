package treesitter

import (
	"sort"
	"strings"

	gts "github.com/odvcencio/gotreesitter"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver/imports"
)

// Loads (graph.Usage.Loads): the imports of a file that load their module
// when the file is loaded, the edges of file-cycle. An import that only
// names types is erased by the compiler and one made on demand runs later,
// so neither closes a cycle:
//
//   - TypeScript: `import type`, a `type` specifier, and an import none of
//     whose names is used as a value (the compiler drops it); `export type`
//   - TypeScript / JavaScript: import() (require() does load)
//   - Python: an import under `if TYPE_CHECKING:`. An import inside a
//     function counts, marked graph.LoadDeferred: that is how a cycle is
//     hidden, not removed
//   - a path or module name in a string

// load records that the file loads spec (name: the name a Python
// `from spec import name` imports).
func (u *usageCollector) load(spec, name string) {
	if spec != "" {
		u.loads = append(u.loads, pendingUse{spec: spec, name: name})
	}
}

// loadUses resolves the recorded loads into the repository's files.
func (u *usageCollector) loadUses(bySpec map[string]imports.Result) []graph.Use {
	var out []graph.Use
	for _, p := range u.loads {
		if res, ok := bySpec[p.spec]; ok && res.Kind == imports.InRepo && res.File != "" {
			out = append(out, graph.Use{Target: strings.TrimSuffix(res.File, "/"), Name: p.name, As: p.as})
		}
	}
	// the same module loaded at the top and again inside a function is loaded
	sort.SliceStable(out, func(i, j int) bool { return out[i].As < out[j].As })
	seen := map[[2]string]bool{}
	kept := out[:0]
	for _, l := range out {
		if k := [2]string{l.Target, l.Name}; !seen[k] {
			seen[k] = true
			kept = append(kept, l)
		}
	}
	return dedupUses(kept)
}

// tsValue records that the local name is used as a value: when an import
// binds it (not for types only), the file loads that module.
func (u *usageCollector) tsValue(local string) {
	if b, ok := u.tsBind[local]; ok && !b.typeOnly && u.tsTypeDepth == 0 {
		if u.tsValues == nil {
			u.tsValues = map[string]bool{}
		}
		u.tsValues[b.spec] = true
	}
}

// tsExportLoads records what an export statement that is not `export type`
// loads: the module it re-exports from, or the modules of the imported
// names in its clause (`import { a } from './x'; export { a }`).
func (u *usageCollector) tsExportLoads(spec string, clause *gts.Node, bind map[string]tsBinding) {
	x := u.x
	if spec != "" {
		u.load(spec, "")
		return
	}
	if clause == nil {
		return
	}
	for i := 0; i < clause.NamedChildCount(); i++ {
		if sp := clause.NamedChild(i); x.typ(sp) == "export_specifier" && !x.hasToken(sp, "type") {
			u.tsValue(tsName(x, x.field(sp, "name")))
		}
	}
}

// tsLoads records the modules the file's import statements load, once the
// whole file was walked: a side-effect import, and an import with a name
// used as a value. JavaScript has no types: every import loads.
func (u *usageCollector) tsLoads(root *gts.Node) {
	x := u.x
	for i := 0; i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		if x.typ(n) != "import_statement" {
			continue
		}
		src := x.field(n, "source")
		if src == nil {
			continue
		}
		spec, ok := x.stringContent(src)
		if !ok {
			continue
		}
		sideEffect := tsChildOfType(x, n, "import_clause") == nil
		if sideEffect || u.tsValues[spec] || (u.lang == graph.JavaScript && !x.hasToken(n, "type")) {
			u.load(spec, "")
		}
	}
}

// pyLoad records an import statement's load, unless the statement is under
// `if TYPE_CHECKING:`.
func (u *usageCollector) pyLoad(spec, name string) {
	if u.pyTypeOnly || spec == "" {
		return
	}
	l := pendingUse{spec: spec, name: name}
	if u.pyInFunc {
		l.as = graph.LoadDeferred
	}
	u.loads = append(u.loads, l)
}

// pyInFunction reports whether n is inside a function or method body.
func (x *extractor) pyInFunction(n *gts.Node) bool {
	for p := n.Parent(); p != nil; p = p.Parent() {
		if x.typ(p) == "function_definition" {
			return true
		}
	}
	return false
}

// pyTypeChecking reports whether n is in the body of an
// `if TYPE_CHECKING:` (or `if typing.TYPE_CHECKING:`): read by the type
// checker only.
func (x *extractor) pyTypeChecking(n *gts.Node) bool {
	for child, p := n, n.Parent(); p != nil; child, p = p, p.Parent() {
		if x.typ(p) != "if_statement" || x.field(p, "consequence") != child {
			continue
		}
		if c := x.field(p, "condition"); c != nil {
			if t := x.text(c); t == "TYPE_CHECKING" || strings.HasSuffix(t, ".TYPE_CHECKING") {
				return true
			}
		}
	}
	return false
}
