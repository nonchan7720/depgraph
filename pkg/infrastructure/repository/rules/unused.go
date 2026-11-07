package rules

import (
	"context"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// Unused code at project level (unused-export, unreachable-file,
// unreachable-package). Every file's usage (graph.Usage, stored on its File
// node) is read in one query and joined in Go: uses are resolved through
// re-exports and Python packages, entry points seed a reachability walk,
// and unreachable files are grouped under the file nothing imports.

// unusedFile is one File node with its usage and declarations.
type unusedFile struct {
	Path, Lang, Scope, Entry string
	// Internal: Entry is the API of a package only the repository uses
	Internal  bool
	Generated bool
	HasUsage  bool
	Uses      []graph.Use
	Exports   []graph.ExportName
	Keep      []string
	Mentions  []string
	Symbols   []unusedSymbol
}

type unusedSymbol struct {
	Name, Kind string
	Exported   bool
	Line       int
}

func (r *Repository) evaluateUnused(ctx context.Context, cfg rules.Config) ([]rules.Finding, error) {
	rows, err := r.query(ctx, `
		MATCH (f:File {project: $project})
		OPTIONAL MATCH (f)-[:DECLARES]->(s:Symbol)
		WITH f, collect({name: s.name, kind: s.kind, exported: s.exported, line: s.line}) AS syms
		RETURN f.path AS path, f.language AS lang, f.scope AS scope, f.entry AS entry, f.internal AS internal,
		       f.generated AS generated, f.uses AS uses, f.exports AS exports, f.keep AS keep,
		       f.mentions AS mentions, syms
	`, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("unused: %w", err)
	}
	files := make([]unusedFile, 0, len(rows))
	for _, row := range rows {
		f := unusedFile{Path: str(row["path"]), Lang: str(row["lang"])}
		if row["scope"] != nil {
			f.HasUsage = true
			f.Scope, f.Entry = str(row["scope"]), str(row["entry"])
			f.Generated, _ = row["generated"].(bool)
			f.Internal, _ = row["internal"].(bool)
			for _, s := range toStrings(row["uses"]) {
				if u, ok := graph.DecodeUse(s); ok {
					f.Uses = append(f.Uses, u)
				}
			}
			for _, s := range toStrings(row["exports"]) {
				if e, ok := graph.DecodeExportName(s); ok {
					f.Exports = append(f.Exports, e)
				}
			}
			f.Keep = toStrings(row["keep"])
			f.Mentions = toStrings(row["mentions"])
		}
		if syms, ok := row["syms"].([]any); ok {
			for _, x := range syms {
				m, _ := x.(map[string]any)
				if m == nil || m["name"] == nil {
					continue
				}
				exp, _ := m["exported"].(bool)
				line, _ := m["line"].(int64)
				f.Symbols = append(f.Symbols, unusedSymbol{Name: str(m["name"]), Kind: str(m["kind"]), Exported: exp, Line: int(line)})
			}
		}
		files = append(files, f)
	}
	return evalUnused(files, cfg), nil
}

func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// reportedKinds are the declarations unused-export reports, per language.
// Go methods are never reported: an interface may need them, and interface
// satisfaction is invisible to a syntactic analysis.
var reportedKinds = map[string]map[string]bool{
	"go":         {"function": true, "struct": true, "interface": true, "type": true},
	"typescript": {"function": true, "class": true, "interface": true, "type": true, "enum": true, "variable": true},
	"javascript": {"function": true, "class": true, "variable": true},
	"python":     {"function": true, "class": true},
	// Ruby methods are called by name from callbacks, routes, views and
	// send: only constants are reported
	"ruby": {"class": true, "module": true},
}

// unusedIndex is the project's files and the uses resolved onto them.
type unusedIndex struct {
	files map[string]*unusedFile
	paths []string            // sorted
	dirs  map[string][]string // dir -> files directly in it
	// used[file][name]: the files using name of file (Go: by package, see goKey)
	used    map[string]map[string]map[string]bool
	usedAll map[string]bool // every export of the file is used
	edges   map[string][]string
	// importers[file]: files using it
	importers map[string]map[string]bool
	marking   map[string]bool // re-export resolution in progress (cycles)
	// rbConsts: the files declaring each Ruby constant (class, module,
	// constant), by full name; rbDefs: those whose declaration has
	// something to use (not a module reopened only as a namespace)
	rbConsts map[string][]string
	rbDefs   map[string][]string
	rbOnly   map[string][]string // files whose only constant it is (module Vacuum; end)
	rbNames  []string            // sorted keys of rbConsts
	rbLower  map[string][]string // lower-cased name -> names (acronym inflections)
	rbLast   map[string][]string // lower-cased last segment -> names
}

func goKey(dir string) string { return "go:" + dir }

func evalUnused(files []unusedFile, cfg rules.Config) []rules.Finding {
	for _, f := range files {
		if !f.HasUsage {
			return nil // stored by a depgraph without usage: rescan first
		}
	}
	ix := &unusedIndex{
		files: map[string]*unusedFile{}, dirs: map[string][]string{},
		used: map[string]map[string]map[string]bool{}, usedAll: map[string]bool{},
		edges: map[string][]string{}, importers: map[string]map[string]bool{},
		marking: map[string]bool{},
	}
	for i := range files {
		f := &files[i]
		ix.files[f.Path] = f
		ix.paths = append(ix.paths, f.Path)
		d := path.Dir(f.Path)
		ix.dirs[d] = append(ix.dirs[d], f.Path)
	}
	sort.Strings(ix.paths)
	ix.indexRuby()

	extra, unusedIgnore, err := cfg.UnusedGlobs()
	if err != nil {
		return nil // Validate reports it
	}
	pkgIgnore, err := cfg.IgnoreGlobs()
	if err != nil {
		return nil
	}
	// ignored: a path (file or Go package dir) the rules leave out: the
	// unused-code ignore globs, or a package of the top-level ignore
	ignored := func(p string, dir bool) bool {
		d := p
		if !dir {
			d = path.Dir(p)
		}
		return rules.MatchAny(unusedIgnore, p) || rules.MatchAny(unusedIgnore, d) || rules.MatchAny(pkgIgnore, d)
	}
	entries := ix.entries(extra)

	// 1. resolve every use, and the files named in strings
	byBase := map[string][]string{}
	for _, p := range ix.paths {
		byBase[path.Base(p)] = append(byBase[path.Base(p)], p)
	}
	for _, p := range ix.paths {
		f := ix.files[p]
		for _, u := range f.Uses {
			ix.resolve(f, u)
		}
		for _, m := range f.Mentions {
			for _, q := range byBase[path.Base(m)] {
				if q == m || strings.HasSuffix(q, "/"+m) {
					ix.edge(p, q)
				}
			}
		}
	}
	// 2. entries are used from outside: all their exports count as used,
	// except those of a package only the repository uses
	for p := range entries {
		if !ix.files[p].Internal {
			ix.markAll(p)
		}
	}

	// 3. reachability
	reached := ix.reach(entries)
	var out []rules.Finding
	// files reported as unreachable (their exports are not reported again);
	// a disabled rule reports nothing, so it hides nothing either
	dead := map[string]bool{}
	if cfg.Enabled(rules.RuleUnreachableFile) {
		out = append(out, ix.unreachableFiles(entries, reached, ignored, dead)...)
	}
	if cfg.Enabled(rules.RuleUnreachablePackage) {
		out = append(out, ix.unreachablePackages(entries, reached, ignored, dead)...)
	}

	// 4. unused exports (not in files already reported as unreachable)
	for _, p := range ix.paths {
		if !cfg.Enabled(rules.RuleUnusedExport) {
			break
		}
		f := ix.files[p]
		if (entries[p] != "" && !f.Internal) || f.Generated || dead[p] || ignored(p, false) || ix.usedAll[p] {
			continue
		}
		kinds := reportedKinds[f.Lang]
		keep := map[string]bool{}
		for _, k := range f.Keep {
			keep[k] = true
		}
		exportedLocal := map[string]bool{}
		for _, e := range f.Exports {
			exportedLocal[e.Local] = true
		}
		for _, s := range f.Symbols {
			if !kinds[s.Kind] || keep[s.Name] || (!s.Exported && !exportedLocal[s.Name]) {
				continue
			}
			if f.Lang == "python" && strings.HasPrefix(s.Name, "__") {
				continue
			}
			// A declaration only its own file uses is not reported: the
			// export is unneeded, but that is style, and common on purpose
			// (Go handlers registered in the same file, RTK Query slices).
			if other, local := ix.symbolUsed(f, s.Name); !other && !local {
				out = append(out, rules.UnusedExport(p, s.Line, s.Kind, s.Name))
			}
		}
	}
	for i := range out {
		out[i].Severity = string(cfg.Severity(out[i].Rule))
	}
	return out
}

// entries maps every entry file to why it is one; extra are the configured
// entry globs (files or Go package dirs).
func (ix *unusedIndex) entries(extra []rules.Glob) map[string]string {
	out := map[string]string{}
	// a Go module without a main package is a library: its non-internal
	// packages are its public API
	goMain := map[string]bool{}
	for _, p := range ix.paths {
		if f := ix.files[p]; f.Lang == "go" && f.Entry == "main package" {
			goMain[f.Scope] = true
		}
	}
	for _, p := range ix.paths {
		f := ix.files[p]
		switch {
		case f.Entry != "":
			out[p] = f.Entry
		case rules.MatchAny(extra, p) || (f.Lang == "go" && rules.MatchAny(extra, path.Dir(p))):
			out[p] = "configured entry"
		case f.Lang == "go" && !goMain[f.Scope] && !hasSegment(path.Dir(p), "internal"):
			out[p] = "library package"
		}
	}
	return out
}

func hasSegment(p, seg string) bool {
	return slices.Contains(strings.Split(p, "/"), seg)
}

func (ix *unusedIndex) edge(from, to string) {
	if from == to || ix.files[to] == nil {
		return
	}
	ix.edges[from] = append(ix.edges[from], to)
	if ix.importers[to] == nil {
		ix.importers[to] = map[string]bool{}
	}
	ix.importers[to][from] = true
}

// resolve applies one use of file f.
func (ix *unusedIndex) resolve(f *unusedFile, u graph.Use) {
	t := u.Target
	if f.Lang == "ruby" && strings.HasPrefix(t, graph.RubyConstPrefix) {
		ix.resolveRuby(f.Path, strings.Fields(strings.TrimPrefix(t, graph.RubyConstPrefix)), u.Name)
		return
	}
	if strings.HasPrefix(t, "?") {
		ix.resolveUnknown(f, u)
		return
	}
	if strings.HasSuffix(t, "/") || (t == "" && u.Name == "*") {
		// every file under a directory (dynamic import with a computed path)
		for _, p := range ix.paths {
			if strings.HasPrefix(p, t) {
				ix.edge(f.Path, p)
				ix.markAll(p)
			}
		}
		return
	}
	switch f.Lang {
	case "go":
		for _, p := range ix.dirs[t] {
			if ix.files[p].Lang == "go" {
				ix.edge(f.Path, p)
			}
		}
		switch u.Name {
		case "":
		case "*":
			ix.usedAll[goKey(t)] = true
		default:
			ix.mark(goKey(t), u.Name, f.Path)
		}
	case "python":
		ix.resolvePython(f.Path, t, u.Name)
	default:
		ix.edge(f.Path, t)
		if u.ReExport {
			return // counts when someone uses the re-exported name (markName)
		}
		switch u.Name {
		case "":
		case "*":
			ix.markAll(t)
		default:
			ix.markName(t, u.Name, f.Path)
		}
	}
}

// resolveUnknown applies a use whose import the resolver could not place
// ("?js/helpers/index", "?@site/src/x"): every TS/JS file the spec's path
// may name (its longest matching tail: js/helpers/index.js,
// js/helpers/index/index.ts) is used; for a directory ("?@/widgets/", a
// dynamic import with a computed path), every file under it.
func (ix *unusedIndex) resolveUnknown(f *unusedFile, u graph.Use) {
	dirPrefix := strings.HasSuffix(u.Target, "/")
	segs := strings.Split(strings.TrimSuffix(strings.TrimPrefix(u.Target, "?"), "/"), "/")
	for len(segs) > 0 && (segs[0] == "." || segs[0] == ".." || segs[0] == "" || strings.HasPrefix(segs[0], "@") || segs[0] == "~" || segs[0] == "#") {
		segs = segs[1:]
	}
	for i := 0; i < len(segs); i++ {
		tail := strings.Join(segs[i:], "/")
		var hits []string
		for _, p := range ix.paths {
			q := ix.files[p]
			if q.Lang != "typescript" && q.Lang != "javascript" {
				continue
			}
			if dirPrefix {
				// every file under a directory whose path ends with tail
				if d := path.Dir(p); d == tail || strings.HasSuffix(d, "/"+tail) || strings.Contains(d+"/", "/"+tail+"/") || strings.HasPrefix(d+"/", tail+"/") {
					hits = append(hits, p)
				}
				continue
			}
			stem := strings.TrimSuffix(p, path.Ext(p))
			if stem == tail || strings.HasSuffix(stem, "/"+tail) || strings.HasSuffix(stem, "/"+tail+"/index") {
				hits = append(hits, p)
			}
		}
		if len(hits) == 0 {
			continue
		}
		for _, p := range hits {
			ix.edge(f.Path, p)
			switch {
			case u.ReExport || u.Name == "":
			case u.Name == "*":
				ix.markAll(p)
			default:
				ix.markName(p, u.Name, f.Path)
			}
		}
		return
	}
}

// resolvePython applies a use of name (dotted) of target, a module file or
// a package directory: a package's name is its submodule when there is one,
// else a name of its __init__.py.
func (ix *unusedIndex) resolvePython(from, target, name string) {
	if ix.files[target] != nil {
		ix.edge(from, target)
		ix.pyName(target, name, from)
		return
	}
	cur := target
	parts := strings.Split(name, ".")
	if name == "" {
		parts = nil
	}
	ix.edge(from, path.Join(cur, "__init__.py"))
	for i, p := range parts {
		if p == "*" {
			ix.markAll(path.Join(cur, "__init__.py"))
			return
		}
		mod := path.Join(cur, p)
		for _, ext := range []string{".py", ".pyi"} {
			if ix.files[mod+ext] != nil {
				ix.edge(from, mod+ext)
				ix.pyName(mod+ext, strings.Join(parts[i+1:], "."), from)
				return
			}
		}
		if ix.isDir(mod) {
			cur = mod
			ix.edge(from, path.Join(cur, "__init__.py"))
			continue
		}
		ix.pyName(path.Join(cur, "__init__.py"), p, from)
		return
	}
}

func (ix *unusedIndex) pyName(file, dotted, from string) {
	if dotted == "" {
		return
	}
	first, _, _ := strings.Cut(dotted, ".")
	if first == "*" {
		ix.markAll(file)
		return
	}
	ix.mark(file, first, from)
}

func (ix *unusedIndex) isDir(d string) bool {
	prefix := d + "/"
	i := sort.SearchStrings(ix.paths, prefix)
	return i < len(ix.paths) && strings.HasPrefix(ix.paths[i], prefix)
}

// mark records that from uses name of key.
func (ix *unusedIndex) mark(key, name, from string) {
	if ix.used[key] == nil {
		ix.used[key] = map[string]map[string]bool{}
	}
	if ix.used[key][name] == nil {
		ix.used[key][name] = map[string]bool{}
	}
	ix.used[key][name][from] = true
}

// markName marks name of TS/JS file t used by from, following its exports
// and re-exports (`export { a as b } from`, `export * from`).
func (ix *unusedIndex) markName(t, name, from string) {
	f := ix.files[t]
	if f == nil {
		return
	}
	key := t + "#" + name + "#" + from
	if ix.marking[key] {
		return
	}
	ix.marking[key] = true
	declared := false
	for _, e := range f.Exports {
		if e.Name == name {
			ix.mark(t, e.Local, from)
			declared = true
		}
	}
	for _, s := range f.Symbols {
		if s.Name == name {
			ix.mark(t, name, from)
			declared = true
		}
	}
	for _, u := range f.Uses {
		if !u.ReExport {
			continue
		}
		switch {
		case u.As == name:
			if u.Name == "*" {
				ix.markAll(u.Target)
			} else {
				ix.markName(u.Target, u.Name, t)
			}
		case u.As == "*" && !declared && name != "default":
			ix.markName(u.Target, name, t)
		}
	}
}

// markAll marks every export of file p used (and, through `export *` and
// named re-exports, those of the files it re-exports).
func (ix *unusedIndex) markAll(p string) {
	f := ix.files[p]
	if f == nil || ix.usedAll[p] {
		return
	}
	ix.usedAll[p] = true
	if f.Lang == "go" {
		// names are per package; a test file uses what it names, it does
		// not export the package
		if !strings.HasSuffix(p, "_test.go") {
			ix.usedAll[goKey(path.Dir(p))] = true
		}
		return
	}
	for _, u := range f.Uses {
		if !u.ReExport {
			continue
		}
		if u.Name == "*" {
			ix.markAll(u.Target)
		} else {
			ix.markName(u.Target, u.Name, p)
		}
	}
}

// symbolUsed reports whether another file uses name of f, and whether f
// itself does.
func (ix *unusedIndex) symbolUsed(f *unusedFile, name string) (other, local bool) {
	key := f.Path
	if f.Lang == "go" {
		key = goKey(path.Dir(f.Path))
	}
	if ix.usedAll[key] {
		return true, false
	}
	for from := range ix.used[key][name] {
		if from == f.Path {
			local = true
		} else {
			other = true
		}
	}
	return other, local
}

// reach walks the edges from the entries. A Go package is reached as a whole.
func (ix *unusedIndex) reach(entries map[string]string) map[string]bool {
	reached := map[string]bool{}
	var queue []string
	visit := func(p string) {
		if !reached[p] {
			reached[p] = true
			queue = append(queue, p)
		}
	}
	for _, p := range ix.paths {
		if entries[p] != "" {
			visit(p)
		}
	}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		for _, q := range ix.edges[p] {
			visit(q)
		}
		if f := ix.files[p]; f.Lang == "go" {
			for _, q := range ix.dirs[path.Dir(p)] {
				if ix.files[q].Lang == "go" {
					visit(q)
				}
			}
		}
	}
	return reached
}

func langFamily(l string) string {
	if l == "javascript" {
		return "typescript"
	}
	return l
}

// unreachableFiles reports the TS/JS/Python/Ruby files no entry reaches, one
// finding per file nothing imports (with the files only it leads to).
// Scopes without any entry point are skipped: their entry points are unknown.
func (ix *unusedIndex) unreachableFiles(entries map[string]string, reached map[string]bool, ignored func(string, bool) bool, dead map[string]bool) []rules.Finding {
	hasEntry := map[string]bool{}
	for p := range entries {
		f := ix.files[p]
		hasEntry[langFamily(f.Lang)+"|"+f.Scope] = true
	}
	cand := map[string]bool{}
	for _, p := range ix.paths {
		f := ix.files[p]
		if f.Lang == "go" || reached[p] || f.Generated || path.Base(p) == "__init__.py" || ignored(p, false) {
			continue
		}
		if !hasEntry[langFamily(f.Lang)+"|"+f.Scope] {
			continue
		}
		cand[p] = true
	}
	var out []rules.Finding
	for _, g := range ix.group(cand, func(p string) []string { return ix.edges[p] }, func(p string) bool {
		for q := range ix.importers[p] {
			if q != p {
				return true
			}
		}
		return false
	}) {
		dead[g.root] = true
		for _, o := range g.only {
			dead[o] = true
		}
		out = append(out, rules.UnreachableFile(g.root, g.only, g.cycle))
	}
	return out
}

// unreachablePackages reports the Go packages no entry reaches.
func (ix *unusedIndex) unreachablePackages(entries map[string]string, reached map[string]bool, ignored func(string, bool) bool, dead map[string]bool) []rules.Finding {
	pkgs := map[string][]string{} // dir -> non-test files
	for _, p := range ix.paths {
		if f := ix.files[p]; f.Lang == "go" && !strings.HasSuffix(p, "_test.go") {
			pkgs[path.Dir(p)] = append(pkgs[path.Dir(p)], p)
		}
	}
	cand := map[string]bool{}
	for d, fs := range pkgs {
		// a package that declares nothing (doc.go only) is documentation
		ok := !ignored(d, true) && ix.declaresAnything(fs)
		for _, p := range fs {
			if reached[p] || entries[p] != "" || ix.files[p].Generated || ignored(p, false) {
				ok = false
			}
		}
		if ok {
			cand[d] = true
		}
	}
	pkgEdges := func(d string) []string {
		seen := map[string]bool{}
		var out []string
		for _, p := range pkgs[d] {
			for _, q := range ix.edges[p] {
				if qd := path.Dir(q); qd != d && !seen[qd] && ix.files[q].Lang == "go" {
					seen[qd] = true
					out = append(out, qd)
				}
			}
		}
		sort.Strings(out)
		return out
	}
	imported := func(d string) bool {
		for _, p := range pkgs[d] {
			for q := range ix.importers[p] {
				if path.Dir(q) != d {
					return true
				}
			}
		}
		return false
	}
	var out []rules.Finding
	for _, g := range ix.group(cand, pkgEdges, imported) {
		for _, d := range append([]string{g.root}, g.only...) {
			for _, p := range pkgs[d] {
				dead[p] = true
			}
		}
		out = append(out, rules.UnreachablePackage(g.root, g.only, g.cycle))
	}
	return out
}

func (ix *unusedIndex) declaresAnything(files []string) bool {
	for _, p := range files {
		if len(ix.files[p].Symbols) > 0 {
			return true
		}
	}
	return false
}

type deadGroup struct {
	root  string
	only  []string
	cycle bool
}

// group splits the unreachable nodes into groups led by a node nothing
// imports (or, for a cycle nothing outside imports, its first node), each
// with the nodes only reachable through it.
func (ix *unusedIndex) group(cand map[string]bool, next func(string) []string, imported func(string) bool) []deadGroup {
	nodes := make([]string, 0, len(cand))
	for n := range cand {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	taken := map[string]bool{}
	var out []deadGroup
	collect := func(root string, cycle bool) {
		taken[root] = true
		g := deadGroup{root: root, cycle: cycle}
		queue := []string{root}
		for len(queue) > 0 {
			n := queue[0]
			queue = queue[1:]
			for _, m := range next(n) {
				if cand[m] && !taken[m] {
					taken[m] = true
					g.only = append(g.only, m)
					queue = append(queue, m)
				}
			}
		}
		sort.Strings(g.only)
		out = append(out, g)
	}
	for _, n := range nodes {
		if !imported(n) {
			collect(n, false)
		}
	}
	for _, n := range nodes {
		if !taken[n] {
			collect(n, true)
		}
	}
	return out
}

// indexRuby indexes the constants the Ruby files declare.
func (ix *unusedIndex) indexRuby() {
	ix.rbConsts, ix.rbDefs, ix.rbOnly = map[string][]string{}, map[string][]string{}, map[string][]string{}
	add := func(m map[string][]string, name, p string) {
		if fs := m[name]; len(fs) == 0 || fs[len(fs)-1] != p {
			m[name] = append(fs, p)
		}
	}
	for _, p := range ix.paths {
		f := ix.files[p]
		if f.Lang != "ruby" {
			continue
		}
		only := ""
		for _, s := range f.Symbols {
			switch s.Kind {
			case "class", "module", "constant":
				add(ix.rbConsts, s.Name, p)
				if s.Exported {
					add(ix.rbDefs, s.Name, p)
				}
				if only == "" {
					only = s.Name
				} else if only != s.Name {
					only = "\x00"
				}
			}
		}
		if only != "" && only != "\x00" {
			add(ix.rbOnly, only, p)
		}
	}
	ix.rbLower = map[string][]string{}
	for n := range ix.rbConsts {
		ix.rbNames = append(ix.rbNames, n)
	}
	sort.Strings(ix.rbNames)
	ix.rbLast = map[string][]string{}
	for _, n := range ix.rbNames {
		l := strings.ToLower(n)
		ix.rbLower[l] = append(ix.rbLower[l], n)
		last := l[strings.LastIndex(l, ":")+1:]
		ix.rbLast[last] = append(ix.rbLast[last], n)
	}
}

// resolveRuby applies a use of the Ruby constant written in the lexical
// nesting levels (innermost first), the way Ruby looks it up: levels[0]::C,
// levels[1]::C, ..., C; "::C" is absolute, "A::*" every constant under A
// and "*Data" every constant whose name ends with Data. A constant found in none of them (inherited from a superclass, or
// reopened in an unusual place) uses every declared constant of that name
// in any namespace: a missed use would be a false report. Every file that
// declares the constant is used, and so are the files of its namespaces
// (Zeitwerk loads admin.rb for Admin::Audit, and vacuum.rb that is only
// `module Vacuum; end`), but not the files that only reopen a namespace
// module to nest their own classes in it.
func (ix *unusedIndex) resolveRuby(from string, levels []string, written string) {
	if suffix, ok := strings.CutPrefix(written, "*"); ok {
		// a name built at run time: every constant whose name ends with it
		for _, n := range ix.rbNames {
			if strings.HasSuffix(n[strings.LastIndex(n, ":")+1:], suffix) {
				ix.rbUse(from, n)
			}
		}
		return
	}
	if p, ok := strings.CutSuffix(written, "::*"); ok {
		p = strings.TrimPrefix(p, "::") + "::"
		for _, l := range append(append([]string(nil), levels...), "") {
			q := p
			if l != "" {
				q = l + "::" + p
			}
			i := sort.SearchStrings(ix.rbNames, q)
			for ; i < len(ix.rbNames) && strings.HasPrefix(ix.rbNames[i], q); i++ {
				ix.rbUse(from, ix.rbNames[i])
			}
		}
		return
	}
	var cands []string
	if a, ok := strings.CutPrefix(written, "::"); ok {
		cands = []string{a}
	} else {
		for _, l := range levels {
			cands = append(cands, l+"::"+written)
		}
		cands = append(cands, written)
	}
	for _, c := range cands {
		if len(ix.rbConsts[c]) > 0 {
			ix.rbUse(from, c)
			return
		}
	}
	// a name derived from a symbol (has_many :api_keys, url: true) camelized
	// without the application's acronyms (APIKey, URLValidator)
	for _, c := range cands {
		if ns := ix.rbLower[strings.ToLower(c)]; len(ns) > 0 {
			for _, n := range ns {
				ix.rbUse(from, n)
			}
			return
		}
	}
	w := strings.ToLower(strings.TrimPrefix(written, "::"))
	suffix := "::" + w
	for _, n := range ix.rbLast[w[strings.LastIndex(w, ":")+1:]] {
		if strings.HasSuffix(strings.ToLower(n), suffix) {
			ix.rbUse(from, n)
		}
	}
}

// rbUse marks the constant name (and its namespaces) used by from: in the
// declarations that have something to use (every class; a module with more
// than other classes in it) and the files that declare nothing else.
func (ix *unusedIndex) rbUse(from, name string) {
	for n := name; n != ""; {
		for _, p := range ix.rbDefs[n] {
			ix.edge(from, p)
			ix.mark(p, n, from)
		}
		for _, p := range ix.rbOnly[n] {
			ix.edge(from, p)
		}
		i := strings.LastIndex(n, "::")
		if i < 0 {
			break
		}
		n = n[:i]
	}
}
