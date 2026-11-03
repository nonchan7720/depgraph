package imports

import (
	"bufio"
	"bytes"
	"context"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// Python resolves Python imports the way the import system does, over the
// package roots (sys.path entries) the repository implies:
//
//  1. the parent of the importing file's top-level package (backend/ for
//     backend/app/api/x.py importing "app.core"), or the importing script's
//     own directory when it is not inside a package
//  2. the repository root and src/
//  3. package dirs declared by pyproject.toml (setuptools package-dir /
//     packages.find where, poetry packages from, hatch wheel packages,
//     pdm package-dir) and setup.cfg (package_dir, packages.find where), and
//     the directory of each pyproject.toml / setup.cfg with its src/
//  4. the parent of every top-level package (a dir with __init__.py whose
//     parent has none)
//
// Relative imports resolve against the importing file's package. Absolute
// imports found under no root are External; relative imports that point at
// nothing are Unresolved.
type Python struct {
	cache rootCache[*pyRoot]
}

var _ Resolver = (*Python)(nil)

// NewPython returns a Python resolver.
func NewPython() *Python { return &Python{} }

type pyRoot struct {
	roots []string // repo-relative package roots, in lookup order
}

// Resolve implements Resolver.
func (p *Python) Resolve(ctx context.Context, root, fromFile, spec string) Result {
	if err := ctx.Err(); err != nil {
		return unresolvedf(err.Error())
	}
	fromDir := path.Dir(filepath.ToSlash(fromFile))
	if strings.HasPrefix(spec, ".") {
		return resolvePyRelative(root, fromDir, spec)
	}
	r := p.cache.get(root, loadPy)
	parts := strings.Split(spec, ".")
	tried := map[string]bool{}
	// A top-level directory without __init__.py is only a namespace package:
	// the import system keeps looking, and a regular package or module in any
	// later sys.path entry (the standard library included) wins over it.
	var namespace *Result
	for _, base := range append(importerRoots(root, fromDir), r.roots...) {
		if tried[base] {
			continue
		}
		tried[base] = true
		res, ok := pyModule(root, path.Join(append([]string{base}, parts...)...))
		if !ok {
			continue
		}
		if !pyNamespace(root, path.Join(base, parts[0])) {
			return res
		}
		if namespace == nil {
			namespace = &res
		}
	}
	if namespace != nil && !pyStdlib[parts[0]] {
		return *namespace
	}
	return external(spec)
}

// pyNamespace reports whether the top-level name at dir is a namespace
// package: a directory with no __init__.py and no module file of that name.
func pyNamespace(root, dir string) bool {
	return isDirAt(root, dir) && !isFileAt(root, path.Join(dir, "__init__.py")) &&
		!isFileAt(root, dir+".py") && !isFileAt(root, dir+".pyi")
}

func resolvePyRelative(root, fromDir, spec string) Result {
	dots := 0
	for dots < len(spec) && spec[dots] == '.' {
		dots++
	}
	base := fromDir
	for i := 1; i < dots; i++ {
		if base == "." {
			return unresolvedf("relative import beyond the repository root")
		}
		base = path.Dir(base)
	}
	rest := spec[dots:]
	if rest == "" {
		return inRepoDir(base)
	}
	cand := path.Join(append([]string{base}, strings.Split(rest, ".")...)...)
	if res, ok := pyModule(root, cand); ok {
		return res
	}
	return unresolvedf("no module " + cand)
}

// pyModule resolves a module path (repo-relative, no extension): a package
// directory or a .py / .pyi file.
func pyModule(root, cand string) (Result, bool) {
	if strings.HasPrefix(cand, "../") || cand == ".." {
		return Result{}, false
	}
	if isDirAt(root, cand) {
		return inRepoDir(cand), true
	}
	for _, e := range []string{".py", ".pyi"} {
		if isFileAt(root, cand+e) {
			return inRepoFile(cand + e), true
		}
	}
	return Result{}, false
}

// importerRoots are the sys.path entries implied by the importing file: the
// parent of its top-level package, or its own directory for a script.
func importerRoots(root, dir string) []string {
	if !isFileAt(root, path.Join(dir, "__init__.py")) {
		return []string{dir}
	}
	top := dir
	for top != "." && isFileAt(root, path.Join(path.Dir(top), "__init__.py")) {
		top = path.Dir(top)
	}
	if top == "." {
		return nil
	}
	return []string{path.Dir(top)}
}

// Invalidate implements Resolver.
func (p *Python) Invalidate(root string) { p.cache.drop(root) }

func loadPy(root string) *pyRoot {
	var configured, projects, packageParents []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := toRel(root, p)
		if d.IsDir() {
			if rel != "." && skipPyDir(root, rel, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		dir := path.Dir(rel)
		switch d.Name() {
		case "pyproject.toml", "setup.cfg":
			b, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			var dirs []string
			if d.Name() == "pyproject.toml" {
				dirs = pyprojectPackageDirs(b)
			} else {
				dirs = setupCfgPackageDirs(b)
			}
			for _, pd := range dirs {
				if r, ok := localDir(root, dir, pd); ok {
					configured = append(configured, r)
				}
			}
			projects = append(projects, dir)
			if isDirAt(root, path.Join(dir, "src")) {
				projects = append(projects, path.Join(dir, "src"))
			}
		case "__init__.py":
			if dir != "." && !isFileAt(root, path.Join(path.Dir(dir), "__init__.py")) {
				packageParents = append(packageParents, path.Dir(dir))
			}
		}
		return nil
	})
	roots := []string{"."}
	if isDirAt(root, "src") {
		roots = append(roots, "src")
	}
	roots = append(roots, configured...)
	roots = append(roots, projects...)
	roots = append(roots, packageParents...)
	return &pyRoot{roots: dedupe(roots)}
}

func skipPyDir(root, rel, name string) bool {
	switch name {
	case "node_modules", "__pycache__", "site-packages":
		return true
	}
	if strings.HasPrefix(name, ".") {
		return true
	}
	// virtualenvs not named .venv
	return isFileAt(root, path.Join(rel, "pyvenv.cfg"))
}

// ---- pyproject.toml / setup.cfg --------------------------------------------

var (
	quotedRe      = regexp.MustCompile(`"([^"]*)"|'([^']*)'`)
	inlineTableRe = regexp.MustCompile(`\{[^}]*\}`)
	tomlKeyRe     = regexp.MustCompile(`^\s*("[^"]*"|'[^']*'|[A-Za-z0-9_.-]+)\s*=\s*(.*)$`)
)

// pyprojectPackageDirs returns the package root directories (relative to the
// pyproject.toml) declared by the common build backends. It reads the small
// TOML subset these keys use (strings, arrays, inline tables).
func pyprojectPackageDirs(b []byte) []string {
	var out []string
	for _, kv := range tomlEntries(b) {
		switch kv.table + "." + kv.key {
		case "tool.setuptools.package-dir.":
			out = append(out, quoted(kv.value)...)
		case "tool.setuptools.packages.find.where":
			out = append(out, quoted(kv.value)...)
		case "tool.pdm.build.package-dir":
			out = append(out, quoted(kv.value)...)
		case "tool.poetry.packages":
			for _, tbl := range inlineTableRe.FindAllString(kv.value, -1) {
				from := "."
				for _, e := range tomlEntries([]byte(strings.ReplaceAll(strings.Trim(tbl, "{}"), ",", "\n"))) {
					if e.key == "from" {
						if q := quoted(e.value); len(q) > 0 {
							from = q[0]
						}
					}
				}
				out = append(out, from)
			}
		case "tool.hatch.build.targets.wheel.packages":
			for _, p := range quoted(kv.value) {
				out = append(out, path.Dir(strings.TrimSuffix(p, "/")))
			}
		}
	}
	return out
}

type tomlEntry struct{ table, key, value string }

// tomlEntries lists `key = value` pairs with the table they are in; a value
// spanning lines (an array) is joined until its brackets balance.
func tomlEntries(b []byte) []tomlEntry {
	var out []tomlEntry
	table := ""
	sc := bufio.NewScanner(bytes.NewReader(b))
	var cur *tomlEntry
	depth := 0
	for sc.Scan() {
		line := stripTOMLComment(sc.Text())
		if cur != nil {
			cur.value += " " + line
			depth += bracketDepth(line)
			if depth <= 0 {
				out = append(out, *cur)
				cur = nil
			}
			continue
		}
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") && !strings.HasPrefix(t, "[[") && strings.HasSuffix(t, "]") {
			table = strings.TrimSpace(strings.Trim(t, "[]"))
			continue
		}
		if strings.HasPrefix(t, "[[") {
			table = strings.TrimSpace(strings.Trim(t, "[]"))
			continue
		}
		m := tomlKeyRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		e := tomlEntry{table: table, key: strings.Trim(m[1], `"'`), value: m[2]}
		if depth = bracketDepth(m[2]); depth > 0 {
			cur = &e
			continue
		}
		out = append(out, e)
	}
	if cur != nil {
		out = append(out, *cur)
	}
	return out
}

func bracketDepth(s string) int {
	d := 0
	for _, q := range quotedRe.FindAllStringIndex(s, -1) { // ignore brackets in strings
		s = s[:q[0]] + strings.Repeat(" ", q[1]-q[0]) + s[q[1]:]
	}
	for _, c := range s {
		switch c {
		case '[', '{':
			d++
		case ']', '}':
			d--
		}
	}
	return d
}

func stripTOMLComment(line string) string {
	inStr := byte(0)
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case inStr != 0:
			if c == inStr {
				inStr = 0
			}
		case c == '"' || c == '\'':
			inStr = c
		case c == '#':
			return line[:i]
		}
	}
	return line
}

func quoted(s string) []string {
	var out []string
	for _, m := range quotedRe.FindAllStringSubmatch(s, -1) {
		if m[1] != "" || strings.HasPrefix(m[0], `"`) {
			out = append(out, m[1])
		} else {
			out = append(out, m[2])
		}
	}
	return out
}

// setupCfgPackageDirs reads [options] package_dir ("=src" / "\n  = src")
// and [options.packages.find] where.
func setupCfgPackageDirs(b []byte) []string {
	var out []string
	section, key := "", ""
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		raw := sc.Text()
		t := strings.TrimSpace(raw)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, ";") {
			continue
		}
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			section, key = strings.TrimSpace(t[1:len(t)-1]), ""
			continue
		}
		var val string
		if raw[0] == ' ' || raw[0] == '\t' { // continuation of key
			val = t
		} else {
			k, v, ok := strings.Cut(t, "=")
			if !ok {
				k, v, ok = strings.Cut(t, ":")
			}
			if !ok {
				continue
			}
			key, val = strings.TrimSpace(k), strings.TrimSpace(v)
		}
		if val == "" {
			continue
		}
		switch {
		case section == "options" && key == "package_dir":
			// "=src" or "pkg = dir"; only the root mapping ("" = dir) sets a root
			if k, v, ok := strings.Cut(val, "="); ok && strings.TrimSpace(k) == "" {
				out = append(out, strings.TrimSpace(v))
			}
		case section == "options.packages.find" && key == "where":
			out = append(out, val)
		}
	}
	return out
}

func dedupe(xs []string) []string {
	seen := map[string]bool{}
	out := xs[:0]
	for _, x := range xs {
		x = path.Clean(x)
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

func isDirAt(root, rel string) bool {
	st, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil && st.IsDir()
}

func isFileAt(root, rel string) bool {
	st, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil && !st.IsDir()
}
