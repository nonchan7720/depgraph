package treesitter

import (
	"regexp"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
)

// pyBinding is a local name bound by an import.
type pyBinding struct {
	// specs: `import a.b.c` binds a to every imported module under it.
	specs []string
	// alias: `import a.b as x` binds x to exactly a.b.
	alias string
	// from: `from m import n` binds n to (m, n).
	fromSpec, fromName string
}

// Python usage:
//
//   - import a.b.c; a.b.c.f()         -> a/b/c.py#f
//   - import m as x; x.g               -> m#g
//   - from m import n, k as kk         -> m#n, m#k (m may be a package: the
//     rule decides between its submodule n and the name n of __init__.py)
//   - from pkg import sub; sub.h()     -> pkg#sub.h
//   - from m import *                  -> m#*
//   - "pkg.mod:attr", "pkg.mod.Class"  -> the module (a dotted path in a
//     string is how Django settings, Celery, uvicorn ... name code)
//   - importlib.import_module(f"pkg.plugins.{n}") -> every file under pkg/plugins
//
// Imports anywhere count (Python code imports inside functions).
func (u *usageCollector) pyFile(root *gts.Node) {
	x := u.x
	bind := map[string]*pyBinding{}
	u.pyBind = bind
	x.walk(root, func(n *gts.Node) bool {
		switch x.typ(n) {
		case "import_statement":
			u.pyDeferred = x.pyTypeChecking(n)
			u.pyImport(n, bind)
			return false
		case "import_from_statement":
			u.pyDeferred = x.pyTypeChecking(n)
			u.pyImportFrom(n, bind)
			return false
		}
		return true
	})
	own := map[string]bool{}
	for _, s := range x.fg.Symbols {
		own[s.Name] = true
	}
	for i := 0; i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		switch x.typ(n) {
		case "import_statement", "import_from_statement":
			continue
		case "decorated_definition":
			// registered by its decorator (a route, a fixture, a task, a command)
			if d := x.field(n, "definition"); d != nil {
				if nm := x.fieldText(d, "name"); nm != "" {
					u.keep = append(u.keep, nm)
				}
			}
		case "if_statement":
			if c := x.field(n, "condition"); c != nil {
				t := x.text(c)
				if strings.Contains(t, "__name__") && strings.Contains(t, "__main__") {
					u.mainGuard = true
				}
			}
		case "expression_statement":
			u.pyDunderAll(n)
		}
		declared := x.declNames(n)
		x.walk(n, func(c *gts.Node) bool { return u.pyNode(c, bind, own, declared) })
	}
}

func (u *usageCollector) pyImport(n *gts.Node, bind map[string]*pyBinding) {
	x := u.x
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		switch x.typ(c) {
		case "dotted_name":
			spec := x.text(c)
			u.use(spec, "")
			u.pyLoad(spec, "")
			top, _, _ := strings.Cut(spec, ".")
			b := bind[top]
			if b == nil || b.alias != "" || b.fromSpec != "" {
				b = &pyBinding{}
				bind[top] = b
			}
			b.specs = append(b.specs, spec)
		case "aliased_import":
			spec := x.fieldText(c, "name")
			alias := x.fieldText(c, "alias")
			u.use(spec, "")
			u.pyLoad(spec, "")
			if alias != "" {
				bind[alias] = &pyBinding{alias: spec}
			}
		}
	}
}

func (u *usageCollector) pyImportFrom(n *gts.Node, bind map[string]*pyBinding) {
	x := u.x
	mod := x.fieldText(n, "module_name")
	if mod == "" {
		return
	}
	modNode := x.field(n, "module_name")
	u.use(mod, "")
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		if c == modNode {
			continue
		}
		switch x.typ(c) {
		case "wildcard_import":
			u.use(mod, "*")
			u.pyLoad(mod, "")
		case "dotted_name":
			name := x.text(c)
			u.use(mod, name)
			u.pyLoad(mod, name)
			bind[name] = &pyBinding{fromSpec: mod, fromName: name}
		case "aliased_import":
			name := x.fieldText(c, "name")
			alias := x.fieldText(c, "alias")
			if name == "" {
				continue
			}
			u.use(mod, name)
			u.pyLoad(mod, name)
			if alias == "" {
				alias = name
			}
			bind[alias] = &pyBinding{fromSpec: mod, fromName: name}
		}
	}
}

// pyDunderAll keeps the names of `__all__ = [...]`: the module declares them
// public on purpose.
func (u *usageCollector) pyDunderAll(n *gts.Node) {
	x := u.x
	if n.NamedChildCount() == 0 {
		return
	}
	a := n.NamedChild(0)
	if x.typ(a) != "assignment" && x.typ(a) != "augmented_assignment" {
		return
	}
	if l := x.field(a, "left"); l == nil || x.text(l) != "__all__" {
		return
	}
	x.walk(x.field(a, "right"), func(c *gts.Node) bool {
		if s, ok := x.stringContent(c); ok {
			u.keep = append(u.keep, s)
			return false
		}
		return true
	})
}

// pyNode handles one node of the walk; it returns whether to descend.
func (u *usageCollector) pyNode(n *gts.Node, bind map[string]*pyBinding, own, declared map[string]bool) bool {
	x := u.x
	switch x.typ(n) {
	case "import_statement", "import_from_statement":
		return false // handled by pyFile
	case "attribute":
		chain, ok := x.pyDotted(n)
		if !ok {
			return true
		}
		head, rest, _ := strings.Cut(chain, ".")
		if b := bind[head]; b != nil {
			u.pyBindingUse(b, head, rest)
			return false
		}
		// the object chain may still use own names (own.attr)
		if own[head] && !declared[head] {
			u.self(u.rel, head)
		}
		return false
	case "identifier":
		name := x.text(n)
		if p := n.Parent(); p != nil && x.typ(p) == "keyword_argument" && x.field(p, "name") == n {
			return false
		}
		if b := bind[name]; b != nil {
			u.pyBindingUse(b, name, "*")
		}
		if own[name] && !declared[name] {
			u.self(u.rel, name)
		}
		return false
	case "string":
		u.pyString(n)
		return false
	}
	return true
}

// pyDotted is the dotted text of an attribute chain of identifiers
// ("a.b.c.f"); ok is false for chains over calls, subscripts, ...
func (x *extractor) pyDotted(n *gts.Node) (string, bool) {
	switch x.typ(n) {
	case "identifier":
		return x.text(n), true
	case "attribute":
		obj, ok := x.pyDotted(x.field(n, "object"))
		if !ok {
			return "", false
		}
		a := x.field(n, "attribute")
		if a == nil {
			return "", false
		}
		return obj + "." + x.text(a), true
	}
	return "", false
}

// pyBindingUse records the use of rest (dotted, "*" for the bound object
// itself) through binding b named head.
func (u *usageCollector) pyBindingUse(b *pyBinding, head, rest string) {
	switch {
	case b.fromSpec != "":
		u.use(b.fromSpec, b.fromName+"."+rest)
	case b.alias != "":
		u.use(b.alias, rest)
	default:
		// import a.b.c binds a: a.b.c.f uses f of a.b.c (the longest import)
		chain := head + "." + rest
		best := ""
		for _, s := range b.specs {
			if strings.HasPrefix(chain, s+".") && len(s) > len(best) {
				best = s
			}
		}
		if best != "" {
			u.use(best, strings.TrimPrefix(chain, best+"."))
		}
	}
}

var (
	pyDottedRe = regexp.MustCompile(`^[A-Za-z_]\w*(\.[A-Za-z_]\w*)+$`)
	pyModuleRe = regexp.MustCompile(`^[A-Za-z_]\w*(\.[A-Za-z_]\w*)*$`)
	pyColonRe  = regexp.MustCompile(`^([A-Za-z_]\w*(?:\.[A-Za-z_]\w*)*):([A-Za-z_]\w*)$`)
)

// pyString records a module named by a string: "pkg.mod:attr",
// "pkg.mod.Name" / "pkg.mod", or the static prefix "pkg.plugins." of an
// f-string.
func (u *usageCollector) pyString(n *gts.Node) {
	x := u.x
	s, ok := x.stringContent(n)
	if !ok {
		if p := x.staticPrefix(n); strings.HasSuffix(p, ".") && pyModuleRe.MatchString(strings.TrimSuffix(p, ".")) {
			u.usePrefix(strings.TrimSuffix(p, "."))
		}
		return
	}
	if m := pyColonRe.FindStringSubmatch(s); m != nil {
		u.use(m[1], m[2])
		return
	}
	if pyDottedRe.MatchString(s) {
		u.use(s, "*")
		if i := strings.LastIndex(s, "."); i > 0 {
			u.use(s[:i], s[i+1:])
		}
	}
}
