package imports

import (
	"context"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/nonchan7720/depgraph/pkg/internal/rootcache"
)

// Ruby resolves the two ways Ruby code reaches other files:
//
//   - require / require_relative / load: a spec "./x" or "../x" (what the
//     extractor makes of require_relative) is relative to the importing
//     file; any other spec is looked up on the load path the repository
//     implies: lib/, spec/ and test/ of the importing file's project (the
//     directory of its Gemfile / *.gemspec), then lib/ of every gem of the
//     repository, then lib/ of the root. A spec found nowhere is External
//     (a gem or the standard library).
//   - constants (spec "::A::B", an absolute constant path): Zeitwerk's
//     convention, which Rails, engines and packwerk packs autoload with.
//     The autoload roots are every directory under app/ (but assets,
//     javascript and views), their concerns/, and lib/, of every
//     application root: the repository root, and each directory with app/
//     and a Gemfile, *.gemspec, package.yml (a packwerk pack) or
//     config/application.rb. A::B is a/b.rb under one of them, or the
//     directory a/b (an implicit namespace). The roots of the importing
//     file's own application are tried first. A constant found nowhere is
//     External (Ruby's own, or a gem's).
//
// The files under the autoload roots are indexed once per root; Note adds a
// file created since (the tree-sitter resolver notes every file it parses).
type Ruby struct {
	cache rootcache.Cache[*rbRoot]
}

var _ BatchResolver = (*Ruby)(nil)

// NewRuby returns a Ruby resolver.
func NewRuby() *Ruby { return &Ruby{} }

type rbRoot struct {
	scopes   []string // dirs with a Gemfile or *.gemspec, deepest first
	gemLibs  []string // lib/ of every gem (dir with a *.gemspec)
	apps     []string // application roots, deepest first
	autoload []rbAutoload

	mu    sync.Mutex
	byRel map[string][]string // path under an autoload root -> files / dirs
}

type rbAutoload struct{ dir, app string }

// Resolve implements Resolver.
func (r *Ruby) Resolve(ctx context.Context, root, fromFile, spec string) Result {
	if err := ctx.Err(); err != nil {
		return unresolvedf(err.Error())
	}
	rr := r.cache.Get(root, loadRuby)
	fromFile = filepath.ToSlash(fromFile)
	if c, ok := strings.CutPrefix(spec, "::"); ok {
		return rr.constant(root, fromFile, c)
	}
	return rr.require(root, fromFile, spec)
}

// ResolveBatch implements BatchResolver.
func (r *Ruby) ResolveBatch(ctx context.Context, root string, reqs []Request) []Result {
	return resolveEach(ctx, r, root, reqs)
}

// Invalidate implements Resolver.
func (r *Ruby) Invalidate(root string) { r.cache.Drop(root) }

// Note adds rel (repo-relative), a file that may have been created after the
// index of root was built, to the index.
func (r *Ruby) Note(root, rel string) {
	rel = filepath.ToSlash(rel)
	if !strings.HasSuffix(rel, ".rb") {
		return
	}
	rr := r.cache.Get(root, loadRuby)
	rr.mu.Lock()
	defer rr.mu.Unlock()
	rr.addRootOf(root, rel)
	for _, a := range rr.autoload {
		if sub, ok := strings.CutPrefix(rel, a.dir+"/"); ok && rr.owner(rel) == a.dir {
			if !slices.Contains(rr.byRel[sub], rel) {
				rr.byRel[sub] = append(rr.byRel[sub], rel)
			}
		}
	}
}

// addRootOf adds the autoload root of rel when rel is in a directory of an
// application's app/ created after the index was built (app/forms/x.rb).
// The caller holds rr.mu.
func (rr *rbRoot) addRootOf(root, rel string) {
	for _, app := range rr.apps {
		in, ok := strings.CutPrefix(rel, path.Join(app, "app")+"/")
		if app == "." {
			in, ok = strings.CutPrefix(rel, "app/")
		}
		sub, _, nested := strings.Cut(in, "/")
		if !ok || !nested || rbNotAutoloaded[sub] || skipRbDir(sub) {
			continue
		}
		dir := path.Join(app, "app", sub)
		for _, a := range rr.autoload {
			if a.dir == dir {
				return
			}
		}
		if isDirAt(root, dir) {
			rr.autoload = append(rr.autoload, rbAutoload{dir: dir, app: app})
		}
		return
	}
}

// owner is the deepest autoload root rel is under ("" when none): a root
// nested in another (app/models/concerns) is not a namespace of it.
func (rr *rbRoot) owner(rel string) string {
	best := ""
	for _, a := range rr.autoload {
		if strings.HasPrefix(rel, a.dir+"/") && len(a.dir) > len(best) {
			best = a.dir
		}
	}
	return best
}

func (rr *rbRoot) require(root, fromFile, spec string) Result {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return unresolvedf("empty require")
	}
	withExt := func(p string) string {
		if strings.HasSuffix(p, ".rb") {
			return p
		}
		return p + ".rb"
	}
	if strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") {
		p := path.Clean(path.Join(path.Dir(fromFile), spec))
		if p == ".." || strings.HasPrefix(p, "../") {
			return unresolvedf("require_relative beyond the repository root")
		}
		if isFileAt(root, withExt(p)) {
			return inRepoFile(withExt(p))
		}
		if isFileAt(root, p) {
			return inRepoFile(p)
		}
		return unresolvedf("no file " + withExt(p))
	}
	if strings.HasPrefix(spec, "/") {
		return external(spec)
	}
	scope := nearestDir(rr.scopes, path.Dir(fromFile))
	var bases []string
	for _, d := range []string{"lib", "spec", "test"} {
		bases = append(bases, path.Join(scope, d))
	}
	bases = append(bases, rr.gemLibs...)
	bases = append(bases, "lib")
	for _, b := range bases {
		f := path.Join(b, withExt(spec))
		if f == ".." || strings.HasPrefix(f, "../") {
			continue // "x/../../y" never leaves the repository
		}
		if isFileAt(root, f) {
			return inRepoFile(f)
		}
	}
	name, _, _ := strings.Cut(spec, "/")
	return external(strings.TrimSuffix(name, ".rb"))
}

func (rr *rbRoot) constant(root, fromFile, c string) Result {
	segs := strings.Split(c, "::")
	under := make([]string, len(segs))
	lower := make([]string, len(segs))
	for i, s := range segs {
		if s == "" {
			return unresolvedf("bad constant")
		}
		under[i] = rubyUnderscore(s)
		lower[i] = strings.ToLower(s)
	}
	app := nearestDir(rr.apps, path.Dir(fromFile))
	for _, rel := range dedup(strings.Join(under, "/"), strings.Join(lower, "/")) {
		if f := rr.pick(root, rr.lookup(rel+".rb"), fromFile, app, false); f != "" {
			return inRepoFile(f)
		}
		if d := rr.pick(root, rr.lookup(rel), fromFile, app, true); d != "" {
			return inRepoDir(d)
		}
	}
	return external(segs[0])
}

// lookup copies the index entry of sub (the files are stat'ed unlocked).
func (rr *rbRoot) lookup(sub string) []string {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	return slices.Clone(rr.byRel[sub])
}

func dedup(a, b string) []string {
	if a == b {
		return []string{a}
	}
	return []string{a, b}
}

// pick is the candidate that still exists closest to fromFile: of its own
// application first, then sharing the longest directory prefix with it
// (Admin from app/models/user.rb is app/models/admin, not
// app/controllers/admin), then the first one.
func (rr *rbRoot) pick(root string, cands []string, fromFile, app string, dir bool) string {
	exists := func(p string) bool {
		if dir {
			return isDirAt(root, p)
		}
		return isFileAt(root, p)
	}
	best, bestScore := "", -1
	for _, p := range cands {
		if !exists(p) {
			continue
		}
		score := commonDirs(p, fromFile)
		if nearestDir(rr.apps, path.Dir(p)) == app {
			score += 1 << 16
		}
		if score > bestScore {
			best, bestScore = p, score
		}
	}
	return best
}

// commonDirs counts the leading directories a and b share.
func commonDirs(a, b string) int {
	as, bs := strings.Split(path.Dir(a), "/"), strings.Split(path.Dir(b), "/")
	n := 0
	for n < len(as) && n < len(bs) && as[n] == bs[n] {
		n++
	}
	return n
}

// nearestDir is the deepest of dirs (sorted deepest first) at or above dir.
func nearestDir(dirs []string, dir string) string {
	for _, d := range dirs {
		if d == "." || dir == d || strings.HasPrefix(dir, d+"/") {
			return d
		}
	}
	return "."
}

// rubyUnderscore is ActiveSupport's String#underscore of one constant name
// ("HTMLParser" -> "html_parser", "UsersController" -> "users_controller").
func rubyUnderscore(s string) string {
	rs := []rune(s)
	var b strings.Builder
	for i, c := range rs {
		if unicode.IsUpper(c) && i > 0 {
			prev := rs[i-1]
			nextLower := i+1 < len(rs) && unicode.IsLower(rs[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				b.WriteByte('_')
			}
		}
		b.WriteRune(unicode.ToLower(c))
	}
	return b.String()
}

// rbNotAutoloaded: directories under app/ Rails does not autoload.
var rbNotAutoloaded = map[string]bool{"assets": true, "javascript": true, "javascripts": true, "views": true, "frontend": true}

func skipRbDir(name string) bool {
	switch name {
	case "node_modules", "vendor", "tmp", "log", "coverage", "__pycache__", "dist", "build":
		return true
	}
	return strings.HasPrefix(name, ".")
}

func loadRuby(root string) *rbRoot {
	rr := &rbRoot{byRel: map[string][]string{}}
	appDirs := map[string]bool{}
	marks := map[string]bool{} // dirs that make an app/ an application root
	gems := map[string]bool{}
	scopes := map[string]bool{}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := toRel(root, p)
		name := d.Name()
		if d.IsDir() {
			if rel != "." && skipRbDir(name) {
				return filepath.SkipDir
			}
			if name == "app" {
				appDirs[path.Dir(rel)] = true
			}
			return nil
		}
		dir := path.Dir(rel)
		switch {
		case name == "Gemfile":
			scopes[dir], marks[dir] = true, true
		case strings.HasSuffix(name, ".gemspec"):
			scopes[dir], marks[dir], gems[dir] = true, true, true
		case name == "package.yml":
			marks[dir] = true
		case name == "application.rb" && path.Base(dir) == "config":
			marks[path.Dir(dir)] = true
		}
		return nil
	})
	for d := range appDirs {
		if d == "." || marks[d] {
			rr.apps = append(rr.apps, d)
		}
	}
	if !slices.Contains(rr.apps, ".") {
		rr.apps = append(rr.apps, ".")
	}
	byDepth := func(ss []string) {
		sort.Slice(ss, func(i, j int) bool {
			di, dj := strings.Count(ss[i], "/"), strings.Count(ss[j], "/")
			if ss[i] == "." {
				di = -1
			}
			if ss[j] == "." {
				dj = -1
			}
			if di != dj {
				return di > dj
			}
			return ss[i] < ss[j]
		})
	}
	byDepth(rr.apps)
	for d := range scopes {
		rr.scopes = append(rr.scopes, d)
	}
	byDepth(rr.scopes)
	for d := range gems {
		rr.gemLibs = append(rr.gemLibs, path.Join(d, "lib"))
	}
	sort.Strings(rr.gemLibs)

	// autoload roots
	seen := map[string]bool{}
	add := func(dir, app string) {
		dir = path.Clean(dir)
		if !seen[dir] && isDirAt(root, dir) {
			seen[dir] = true
			rr.autoload = append(rr.autoload, rbAutoload{dir: dir, app: app})
		}
	}
	for _, app := range rr.apps {
		appDir := path.Join(app, "app")
		if subs, err := readDirNames(root, appDir); err == nil {
			for _, s := range subs {
				if rbNotAutoloaded[s] || skipRbDir(s) {
					continue
				}
				add(path.Join(appDir, s), app)
				add(path.Join(appDir, s, "concerns"), app)
			}
		}
		add(path.Join(app, "lib"), app)
	}
	for _, l := range rr.gemLibs {
		add(l, nearestDir(rr.apps, l))
	}
	sort.Slice(rr.autoload, func(i, j int) bool { return rr.autoload[i].dir < rr.autoload[j].dir })

	// index every file and directory under the roots by its path under the
	// root that owns it
	for _, a := range rr.autoload {
		base := filepath.Join(root, filepath.FromSlash(a.dir))
		_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			rel, ok := toRel(root, p)
			if !ok || rel == a.dir {
				return nil
			}
			if d.IsDir() {
				if skipRbDir(d.Name()) {
					return filepath.SkipDir
				}
				if seen[rel] {
					return filepath.SkipDir // another root (concerns/)
				}
			} else if !strings.HasSuffix(rel, ".rb") {
				return nil
			}
			sub := strings.TrimPrefix(rel, a.dir+"/")
			rr.byRel[sub] = append(rr.byRel[sub], rel)
			return nil
		})
	}
	return rr
}

func readDirNames(root, rel string) ([]string, error) {
	es, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range es {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// IsRubyFile reports whether rel is Ruby source (.rb, .rake, config.ru,
// Rakefile; Jbuilder / Builder / .ruby templates under views/) or a Ruby view
// template (ERB, Haml, Slim; see IsRubyTemplate).
func IsRubyFile(rel string) bool {
	rel = filepath.ToSlash(rel)
	base := path.Base(rel)
	if base == "Rakefile" || base == "config.ru" {
		return true
	}
	switch strings.ToLower(path.Ext(base)) {
	case ".rb", ".rake":
		return true
	case ".jbuilder", ".builder", ".ruby": // view templates (.ruby: the Ruby template handler)
		// only in views/: Dockerfile.builder is no Ruby
		return strings.HasPrefix(rel, "views/") || strings.Contains(rel, "/views/")
	}
	return IsRubyTemplate(rel)
}

// IsRubyTemplate reports whether rel is a view template with embedded Ruby
// (ERB, Haml, Slim): not parsed as Ruby, only its code is scanned.
func IsRubyTemplate(rel string) bool {
	switch strings.ToLower(path.Ext(rel)) {
	case ".erb", ".haml", ".slim":
		return true
	}
	return false
}
