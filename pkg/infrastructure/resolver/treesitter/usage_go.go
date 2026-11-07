package treesitter

import (
	"bufio"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	gts "github.com/odvcencio/gotreesitter"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver/imports"
)

// Go usage:
//
//   - import "m/b"; b.Fn(), b.Typ{}   -> b#Fn, b#Typ (and b# for the import)
//   - import . "m/c"                  -> c#* (every name may be used)
//   - import _ "m/d"                  -> d# (the package only)
//   - an exported name of the own package used without qualifier, outside
//     its own declaration (a method's receiver does not count: a type only
//     its methods mention is unused) -> <own package>#Name
func (u *usageCollector) goFile(root *gts.Node) {
	x := u.x
	pkg := x.fg.Package
	for i := 0; i < root.NamedChildCount(); i++ {
		n := root.NamedChild(i)
		switch x.typ(n) {
		case "comment":
			u.goEmbed(x.text(n))
			continue
		case "package_clause":
			if n.NamedChildCount() > 0 && x.text(n.NamedChild(0)) == "main" {
				u.goMain = true
			}
			continue
		case "import_declaration":
			x.forEachDescendant(n, "import_spec", func(spec *gts.Node) {
				p := unquote(x.fieldText(spec, "path"))
				if p != "" {
					u.goImports = append(u.goImports, goImport{alias: x.fieldText(spec, "name"), spec: p})
				}
			})
			continue
		}
		declared := x.declNames(n)
		u.goWalk(n, pkg, declared)
	}
}

// goEmbed: the files a //go:embed directive embeds are used by the file
// (a file name is a mention, a directory or pattern every file under it).
func (u *usageCollector) goEmbed(comment string) {
	rest, ok := strings.CutPrefix(comment, "//go:embed ")
	if !ok {
		return
	}
	dir := path.Dir(u.rel)
	for f := range strings.FieldsSeq(rest) {
		f = strings.TrimPrefix(strings.Trim(f, "`\""), "all:")
		if f == "" || strings.HasPrefix(f, "..") {
			continue
		}
		if i := strings.IndexAny(f, "*?["); i >= 0 {
			u.dirPrefixes = append(u.dirPrefixes, path.Join(dir, path.Dir(f[:i]+"x")))
			continue
		}
		p := path.Join(dir, f)
		u.mentions = append(u.mentions, p)
		u.dirPrefixes = append(u.dirPrefixes, p) // a directory: everything under it
	}
}

func (u *usageCollector) goWalk(n *gts.Node, pkg string, declared map[string]bool) {
	x := u.x
	x.walk(n, func(c *gts.Node) bool {
		switch x.typ(c) {
		case "method_declaration":
			recv := x.field(c, "receiver")
			for i := 0; i < c.NamedChildCount(); i++ {
				if ch := c.NamedChild(i); ch != recv {
					u.goWalk(ch, pkg, declared)
				}
			}
			return false
		case "selector_expression":
			if op := x.field(c, "operand"); op != nil && x.typ(op) == "identifier" {
				if f := x.field(c, "field"); f != nil {
					u.goSelectors = append(u.goSelectors, goSelector{q: x.text(op), name: x.text(f)})
				}
			}
			return true
		case "qualified_type":
			if c.NamedChildCount() == 2 {
				u.goSelectors = append(u.goSelectors, goSelector{q: x.text(c.NamedChild(0)), name: x.text(c.NamedChild(1))})
			}
			return false
		case "keyed_element":
			// T{Field: v}: the key is a field name, not a use
			for i := 1; i < c.NamedChildCount(); i++ {
				u.goWalk(c.NamedChild(i), pkg, declared)
			}
			return false
		case "identifier", "type_identifier":
			if name := x.text(c); goExported(name) && !declared[name] {
				u.self(pkg, name)
			}
			return false
		}
		return true
	})
}

// goUses resolves the imports of a Go file and its qualified names.
func (u *usageCollector) goUses(root string, bySpec map[string]imports.Result) []graph.Use {
	var out []graph.Use
	binds := map[string]string{} // local package name -> package dir
	for _, gi := range u.goImports {
		res, ok := bySpec[gi.spec]
		if !ok || res.Kind != imports.InRepo || res.File == "" {
			continue
		}
		dir := res.File
		out = append(out, graph.Use{Target: dir})
		switch gi.alias {
		case "_":
		case ".":
			out = append(out, graph.Use{Target: dir, Name: "*"})
		case "":
			if name := goPackageName(root, dir); name != "" {
				binds[name] = dir
			}
			binds[goDefaultName(gi.spec)] = dir
		default:
			binds[gi.alias] = dir
		}
	}
	for _, s := range u.goSelectors {
		if dir, ok := binds[s.q]; ok {
			out = append(out, graph.Use{Target: dir, Name: s.name})
		}
	}
	return out
}

// goDefaultName is the package name an import path suggests (its last
// element, without a major version suffix or "go-" prefix).
func goDefaultName(spec string) string {
	base := path.Base(spec)
	if len(base) >= 2 && base[0] == 'v' && strings.Trim(base[1:], "0123456789") == "" {
		base = path.Base(path.Dir(spec))
	}
	base = strings.TrimPrefix(base, "go-")
	base = strings.TrimSuffix(base, ".go")
	return strings.ReplaceAll(base, "-", "_")
}

var goPackageRe = regexp.MustCompile(`^package\s+([A-Za-z_]\w*)`)

// goPackageName reads the package clause of a non-test Go file in dir.
func goPackageName(root, dir string) string {
	abs := filepath.Join(root, filepath.FromSlash(dir))
	entries, err := os.ReadDir(abs)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		if name := readPackageClause(filepath.Join(abs, n)); name != "" {
			return name
		}
	}
	return ""
}

func readPackageClause(p string) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	// bufio.Reader, not Scanner: a generated file can start with a comment
	// line longer than Scanner's 64KB limit.
	r := bufio.NewReader(f)
	for {
		line, err := r.ReadString('\n')
		if m := goPackageRe.FindStringSubmatch(line); m != nil {
			return m[1]
		}
		if err != nil {
			return ""
		}
	}
}
