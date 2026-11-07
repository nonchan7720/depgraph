package imports

import (
	"context"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/nonchan7720/depgraph/pkg/internal/rootcache"

	"golang.org/x/mod/modfile"
)

// Go resolves Go import paths with the modules of the repository: every
// go.mod found in it (nested modules included), the modules a go.work uses,
// and the replace directives (of go.mod and go.work) that point at a local
// directory inside the repository.
type Go struct {
	cache rootcache.Cache[*goRoot]
}

var _ Resolver = (*Go)(nil)

// NewGo returns a Go resolver.
func NewGo() *Go { return &Go{} }

type goModule struct {
	path     string            // module path
	dir      string            // repo-relative dir ("." for the root)
	replaces map[string]string // module path -> repo-relative dir (local replaces of this go.mod)
	// provides: module paths that come from outside the repository (require,
	// and replace with a module or a directory outside it) -> why
	provides map[string]string
}

type goWork struct {
	dir      string
	replaces map[string]string
	provides map[string]string
}

type goRoot struct {
	byPath   map[string]*goModule // modules of the repository by module path
	pathDirs map[string]string    // byPath as module path -> dir
	byDir    map[string]*goModule // every go.mod dir, replace targets included
	modDirs  map[string]bool      // dirs that are module roots (go.mod or replace target)
	works    []goWork
}

// Resolve implements Resolver.
func (g *Go) Resolve(ctx context.Context, root, fromFile, spec string) Result {
	if err := ctx.Err(); err != nil {
		return unresolvedf(err.Error())
	}
	r := g.cache.Get(root, loadGo)
	fromDir := path.Dir(filepath.ToSlash(fromFile))

	// replaces apply to the importing module (and its workspace) only
	var replaces []map[string]string
	if m := r.owner(fromDir); m != nil {
		replaces = append(replaces, m.replaces)
	}
	for _, w := range r.works {
		if within(fromDir, w.dir) {
			replaces = append(replaces, w.replaces)
		}
	}
	for _, rep := range replaces {
		if old, dir, ok := longestPrefix(spec, rep); ok {
			return r.check(root, spec, old, dir)
		}
	}
	if mod, dir, ok := longestPrefix(spec, r.pathDirs); ok {
		return r.check(root, spec, mod, dir)
	}
	return r.classify(fromDir, spec)
}

// classify an external Go import: provided by a module the importing module
// (or its go.work) requires or replaces => third-party; a path whose first
// element has no dot => the standard library (the go command's rule: such a
// path is never fetched); else not known (the go command reports it).
func (r *goRoot) classify(fromDir, spec string) Result {
	res := external(spec)
	var provides []map[string]string
	m := r.owner(fromDir)
	if m != nil {
		provides = append(provides, m.provides)
	}
	for _, w := range r.works {
		if within(fromDir, w.dir) {
			provides = append(provides, w.provides)
		}
	}
	for _, p := range provides {
		if _, why, ok := longestPrefix(spec, p); ok {
			return res.classed(ClassThirdParty, why)
		}
	}
	first, _, _ := strings.Cut(spec, "/")
	if !strings.Contains(first, ".") {
		return res.classed(ClassStandard, "Go standard library (no dot in the first path element)")
	}
	if m == nil {
		return res.classed("", "no go.mod requires it")
	}
	return res.classed("", path.Join(m.dir, "go.mod")+" does not require it")
}

// check maps spec (inside module modPath rooted at modDir) to its package
// directory and verifies the directory exists and belongs to that module.
func (r *goRoot) check(root, spec, modPath, modDir string) Result {
	rest := strings.TrimPrefix(strings.TrimPrefix(spec, modPath), "/")
	dir := path.Clean(path.Join(modDir, rest))
	st, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir)))
	if err != nil || !st.IsDir() {
		return unresolvedf("no package directory " + dir + " in module " + modPath)
	}
	if owner := r.ownerDir(dir); owner != modDir {
		return unresolvedf(dir + " belongs to the module at " + owner + ", not " + modPath)
	}
	return inRepoDir(dir)
}

// ownerDir is the nearest module root at or above dir ("" when none).
func (r *goRoot) ownerDir(dir string) string {
	for d := dir; ; d = path.Dir(d) {
		if r.modDirs[d] {
			return d
		}
		if d == "." || d == "/" {
			return ""
		}
	}
}

func (r *goRoot) owner(dir string) *goModule {
	if d := r.ownerDir(dir); d != "" {
		return r.byDir[d]
	}
	return nil
}

// Invalidate implements Resolver.
func (g *Go) Invalidate(root string) { g.cache.Drop(root) }

func loadGo(root string) *goRoot {
	r := &goRoot{byPath: map[string]*goModule{}, byDir: map[string]*goModule{}, modDirs: map[string]bool{}}
	var workFiles []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := toRel(root, p)
		if d.IsDir() {
			if rel != "." && skipGoDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		switch d.Name() {
		case "go.mod":
			if m := readGoMod(root, path.Dir(rel)); m != nil {
				r.add(m, true)
			}
		case "go.work":
			workFiles = append(workFiles, rel)
		}
		return nil
	})
	for _, wf := range workFiles {
		r.addWork(root, wf)
	}
	// replace targets are module roots too, even in skipped directories
	for _, m := range r.byDir {
		r.addReplaceTargets(root, m.replaces)
	}
	for _, w := range r.works {
		r.addReplaceTargets(root, w.replaces)
	}
	r.pathDirs = make(map[string]string, len(r.byPath))
	for p, m := range r.byPath {
		r.pathDirs[p] = m.dir
	}
	return r
}

func (r *goRoot) add(m *goModule, public bool) {
	if _, ok := r.byDir[m.dir]; !ok {
		r.byDir[m.dir] = m
	}
	r.modDirs[m.dir] = true
	if public && m.path != "" {
		if _, ok := r.byPath[m.path]; !ok {
			r.byPath[m.path] = m
		}
	}
}

func (r *goRoot) addWork(root, rel string) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return
	}
	wf, err := modfile.ParseWork(rel, b, nil)
	if err != nil {
		return
	}
	dir := path.Dir(rel)
	for _, u := range wf.Use {
		if d, ok := localDir(root, dir, u.Path); ok {
			if m := readGoMod(root, d); m != nil {
				r.add(m, true)
			}
		}
	}
	w := goWork{dir: dir, replaces: map[string]string{}, provides: map[string]string{}}
	for _, rep := range wf.Replace {
		addReplace(root, dir, rep, w.replaces, w.provides, rel)
	}
	r.works = append(r.works, w)
}

func (r *goRoot) addReplaceTargets(root string, reps map[string]string) {
	for _, d := range reps {
		if _, ok := r.byDir[d]; ok {
			continue
		}
		if m := readGoMod(root, d); m != nil {
			r.add(m, false)
		} else {
			r.modDirs[d] = true
		}
	}
}

// readGoMod reads <dir>/go.mod (dir repo-relative).
func readGoMod(root, dir string) *goModule {
	name := path.Join(dir, "go.mod")
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil {
		return nil
	}
	f, err := modfile.Parse(name, b, nil)
	if err != nil {
		if f, err = modfile.ParseLax(name, b, nil); err != nil {
			return nil
		}
	}
	m := &goModule{dir: dir, replaces: map[string]string{}, provides: map[string]string{}}
	if f.Module != nil {
		m.path = f.Module.Mod.Path
	}
	for _, req := range f.Require {
		m.provides[req.Mod.Path] = "required by " + name
	}
	for _, rep := range f.Replace {
		addReplace(root, dir, rep, m.replaces, m.provides, name)
	}
	return m
}

// addReplace records a replace: a directory inside root in into, anything
// else (a module, a directory outside the repository) in provides.
func addReplace(root, fromDir string, rep *modfile.Replace, into, provides map[string]string, file string) {
	if rep.New.Version == "" { // a directory
		if d, ok := localDir(root, fromDir, rep.New.Path); ok {
			into[rep.Old.Path] = d
			return
		}
	}
	provides[rep.Old.Path] = "replaced in " + file
}

// localDir resolves a go.mod / go.work directory path (relative to fromDir,
// or absolute) to a repo-relative dir, reporting false when outside root.
func localDir(root, fromDir, p string) (string, bool) {
	abs := filepath.FromSlash(p)
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, filepath.FromSlash(fromDir), abs)
	}
	return toRel(root, filepath.Clean(abs))
}

// skipGoDir mirrors what the go command ignores ("." / "_" prefixed dirs,
// testdata, vendor) plus node_modules.
func skipGoDir(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") ||
		name == "testdata" || name == "vendor" || name == "node_modules"
}

// longestPrefix finds the key of m that is spec or a path prefix of spec,
// preferring the longest.
func longestPrefix(spec string, m map[string]string) (key, val string, ok bool) {
	for k, v := range m {
		if (spec == k || strings.HasPrefix(spec, k+"/")) && len(k) > len(key) {
			key, val, ok = k, v, true
		}
	}
	return key, val, ok
}

// within reports whether dir is base or below it (repo-relative).
func within(dir, base string) bool {
	return base == "." || dir == base || strings.HasPrefix(dir, base+"/")
}
