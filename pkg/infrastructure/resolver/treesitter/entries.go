package treesitter

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver/imports"
)

// Entry points: files that are run, loaded or imported from outside the
// repository's own import graph. They are reachable by definition and their
// exports are used from outside, so the unused-code rules never report
// them. A missing entry point is a false positive, so when in doubt a file
// is an entry:
//
//   - per file: Go main packages and _test.go; test, story and config files;
//     .d.ts; scripts / tools / examples / docs directories; conventional
//     entry names at a package root (index, main, server, cli, ...); a
//     Python __main__ guard; a `depgraph:entry` comment
//   - from manifests (cached per root, dropped by ConfigChanged): package.json
//     main / module / browser / types / bin / exports and the files its
//     scripts run (dist/ paths mapped back to their sources), <script src> of
//     HTML pages, pyproject.toml / setup.cfg scripts and entry points, and
//     the files and modules commands in Dockerfiles, Procfiles, Makefiles,
//     compose files, CI workflows and shell scripts run
//   - frameworks: file-system routes of Next.js, Remix, Gatsby, Expo Router,
//     ... (app/, pages/, routes/); Django's conventional modules; every file
//     of a package that uses Vue, Svelte, Astro, MDX, ... (their component
//     files are not parsed, so their imports are not seen)

// manifests is what the manifests of one root say about entry points.
type manifests struct {
	tsScopes map[string]bool // dirs with a package.json
	pyScopes map[string]bool // dirs with pyproject.toml / setup.py / setup.cfg
	goScopes map[string]bool // dirs with a go.mod

	files    map[string]string // file -> why it is an entry
	prefixes map[string]string // dir/ -> why every file under it is an entry
	opaque   map[string]string // TS scope -> framework whose files are not parsed
	routing  map[string]string // TS scope -> framework with file-system routes
	django   map[string]bool   // Python scope -> uses Django
}

type manifestEntry struct {
	once sync.Once
	m    *manifests
}

type manifestCache struct {
	mu sync.Mutex
	m  map[string]*manifestEntry
}

func (c *manifestCache) get(root string, load func() *manifests) *manifests {
	c.mu.Lock()
	if c.m == nil {
		c.m = map[string]*manifestEntry{}
	}
	e, ok := c.m[root]
	if !ok {
		e = &manifestEntry{}
		c.m[root] = e
	}
	c.mu.Unlock()
	e.once.Do(func() { e.m = load() })
	return e.m
}

func (c *manifestCache) drop(root string) {
	c.mu.Lock()
	delete(c.m, root)
	c.mu.Unlock()
}

// manifests returns the (cached) manifests of root.
func (r *Resolver) manifests(ctx context.Context, root string) *manifests {
	return r.man.get(root, func() *manifests { return loadManifests(ctx, root, r.imp) })
}

// isEntryManifest reports whether entry points depend on rel (repo-relative):
// a change to it must be followed by ConfigChanged.
func isEntryManifest(rel string) bool {
	rel = filepath.ToSlash(rel)
	base := path.Base(rel)
	if strings.HasPrefix(rel, ".github/workflows/") || strings.Contains(rel, "/.github/workflows/") {
		return true
	}
	return commandFile(base) || strings.HasSuffix(base, ".html") || base == "package.json" ||
		base == "pyproject.toml" || base == "setup.cfg" || base == "setup.py" || base == "manage.py" ||
		base == "go.mod" || base == "tsconfig.json" || isRequirements(base) || opaqueExt(base) != ""
}

// commandFile: files whose text runs commands that may name source files.
func commandFile(base string) bool {
	switch base {
	case "Procfile", "Makefile", "makefile", "GNUmakefile", "justfile", "Justfile", ".justfile",
		"mise.toml", ".mise.toml", "tox.ini", "pyproject.toml", "setup.cfg", "netlify.toml",
		"vercel.json", "fly.toml", "app.yaml", "app.yml", "serverless.yml", "serverless.yaml",
		".gitlab-ci.yml", "Taskfile.yml", "Taskfile.yaml", "nodemon.json", "turbo.json":
		return true
	}
	if strings.HasPrefix(base, "Dockerfile") || strings.HasSuffix(base, ".dockerfile") ||
		strings.HasSuffix(base, ".mk") || strings.HasSuffix(base, ".sh") || strings.HasSuffix(base, ".bash") {
		return true
	}
	for _, p := range []string{"docker-compose", "compose."} {
		if strings.HasPrefix(base, p) && (strings.HasSuffix(base, ".yml") || strings.HasSuffix(base, ".yaml")) {
			return true
		}
	}
	return false
}

func isRequirements(base string) bool {
	return strings.HasPrefix(base, "requirements") && strings.HasSuffix(base, ".txt") || base == "Pipfile"
}

// opaqueExt names the framework of a component file depgraph does not parse.
func opaqueExt(base string) string {
	switch path.Ext(base) {
	case ".vue":
		return "Vue"
	case ".svelte":
		return "Svelte"
	case ".astro":
		return "Astro"
	case ".mdx":
		return "MDX"
	case ".marko":
		return "Marko"
	}
	return ""
}

// opaqueDeps: packages whose component files are not parsed.
var opaqueDeps = map[string]string{
	"vue": "Vue", "nuxt": "Nuxt", "svelte": "Svelte", "@sveltejs/kit": "SvelteKit",
	"astro": "Astro", "@mdx-js/mdx": "MDX", "@mdx-js/react": "MDX", "@mdx-js/loader": "MDX",
	"@builder.io/qwik": "Qwik", "ember-source": "Ember", "marko": "Marko",
}

// routingDeps: frameworks that load app/, pages/, routes/ files by convention.
var routingDeps = map[string]string{
	"next": "Next.js", "@remix-run/react": "Remix", "@remix-run/node": "Remix", "@remix-run/dev": "Remix",
	"@react-router/dev": "React Router", "gatsby": "Gatsby", "expo-router": "Expo Router",
	"@docusaurus/core": "Docusaurus", "@solidjs/start": "SolidStart", "@tanstack/react-start": "TanStack Start",
	"@tanstack/react-router": "TanStack Router", "vitepress": "VitePress", "@redwoodjs/core": "RedwoodJS",
}

type packageJSON struct {
	Main     any               `json:"main"`
	Module   any               `json:"module"`
	Browser  any               `json:"browser"`
	Types    any               `json:"types"`
	Typings  any               `json:"typings"`
	Source   any               `json:"source"`
	Bin      any               `json:"bin"`
	Exports  any               `json:"exports"`
	Scripts  map[string]string `json:"scripts"`
	Deps     map[string]any    `json:"dependencies"`
	DevDeps  map[string]any    `json:"devDependencies"`
	PeerDeps map[string]any    `json:"peerDependencies"`
}

func loadManifests(ctx context.Context, root string, imp *imports.Multi) *manifests {
	m := &manifests{
		tsScopes: map[string]bool{}, pyScopes: map[string]bool{}, goScopes: map[string]bool{},
		files: map[string]string{}, prefixes: map[string]string{}, opaque: map[string]string{},
		routing: map[string]string{}, django: map[string]bool{},
	}
	var pkgs, htmls, commands, pyManifests []string
	opaqueFiles := map[string]string{} // dir -> framework
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || ctx.Err() != nil {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && resolver.ShouldSkipDir(rel, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if resolver.ShouldSkipPath(rel) {
			return nil
		}
		base := d.Name()
		dir := path.Dir(rel)
		switch {
		case base == "package.json":
			m.tsScopes[dir] = true
			pkgs = append(pkgs, rel)
		case base == "go.mod":
			m.goScopes[dir] = true
		case base == "pyproject.toml" || base == "setup.py" || base == "setup.cfg":
			m.pyScopes[dir] = true
			pyManifests = append(pyManifests, rel)
		case base == "manage.py":
			m.django[dir] = true
		case isRequirements(base):
			pyManifests = append(pyManifests, rel)
		case strings.HasSuffix(base, ".html"):
			htmls = append(htmls, rel)
		}
		if strings.Contains(rel, ".github/workflows/") || commandFile(base) {
			commands = append(commands, rel)
		}
		if fw := opaqueExt(base); fw != "" {
			opaqueFiles[dir] = fw
		}
		return nil
	})

	for dir, fw := range opaqueFiles {
		s := nearest(m.tsScopes, dir)
		if _, ok := m.opaque[s]; !ok {
			m.opaque[s] = fw + " components (not parsed)"
		}
	}
	for _, rel := range pkgs {
		m.loadPackageJSON(root, rel)
	}
	for _, rel := range htmls {
		m.loadHTML(root, rel)
	}
	py := func(fromDir, spec string) string {
		res := imp.Py.Resolve(ctx, root, path.Join(fromDir, "__entry__.py"), spec)
		if res.Kind != imports.InRepo || res.File == "" {
			return ""
		}
		if isDir(root, res.File) {
			for _, f := range []string{"__main__.py", "__init__.py"} {
				if isFile(root, path.Join(res.File, f)) {
					return path.Join(res.File, f)
				}
			}
			return ""
		}
		return res.File
	}
	for _, rel := range commands {
		m.loadCommands(root, rel, py)
	}
	for _, rel := range pyManifests {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err == nil && djangoRe.Match(b) {
			m.django[nearest(m.pyScopes, path.Dir(rel))] = true
		}
	}
	return m
}

var djangoRe = regexp.MustCompile(`(?im)^[\s"'\[]*django\b`)

// nearest is the deepest dir of scopes at or above dir ("." when none).
func nearest(scopes map[string]bool, dir string) string {
	for d := dir; ; d = path.Dir(d) {
		if scopes[d] {
			return d
		}
		if d == "." || d == "/" || d == "" {
			return "."
		}
	}
}

func (m *manifests) addFile(rel, why string) {
	if _, ok := m.files[rel]; !ok {
		m.files[rel] = why
	}
}

func (m *manifests) loadPackageJSON(root, rel string) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return
	}
	var pj packageJSON
	if json.Unmarshal(b, &pj) != nil {
		return
	}
	dir := path.Dir(rel)
	out := tsOutDirs(root, dir)
	for _, deps := range []map[string]any{pj.Deps, pj.DevDeps, pj.PeerDeps} {
		for name := range deps {
			if fw, ok := opaqueDeps[name]; ok {
				m.opaque[dir] = fw + " components (not parsed)"
			}
			if fw, ok := routingDeps[name]; ok {
				m.routing[dir] = fw
			}
		}
	}
	for field, v := range map[string]any{
		"main": pj.Main, "module": pj.Module, "browser": pj.Browser, "types": pj.Types,
		"typings": pj.Typings, "source": pj.Source, "bin": pj.Bin, "exports": pj.Exports,
	} {
		for _, s := range jsonStrings(v) {
			why := "package.json " + field
			if strings.Contains(s, "*") {
				m.addPrefix(root, dir, s, out, why)
				continue
			}
			for _, f := range tsCandidates(root, dir, s, out) {
				m.addFile(f, why)
			}
		}
	}
	if pj.Main == nil && pj.Exports == nil && pj.Module == nil {
		// Node falls back to index.js
		for _, f := range tsCandidates(root, dir, "index.js", out) {
			m.addFile(f, "package.json (implicit main)")
		}
	}
	names := make([]string, 0, len(pj.Scripts))
	for k := range pj.Scripts {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		for _, tok := range commandTokens(pj.Scripts[k]) {
			if hasSourceExt(tok) || strings.Contains(tok, "/") {
				for _, f := range tsCandidates(root, dir, tok, out) {
					m.addFile(f, "package.json script "+k)
				}
			}
		}
	}
}

// addPrefix makes every file under the directory part of an exports pattern
// ("./dist/*.js") an entry.
func (m *manifests) addPrefix(root, dir, pat string, out [][2]string, why string) {
	p := pat[:strings.Index(pat, "*")]
	d := path.Clean(path.Join(dir, path.Dir(p+"x")))
	for _, c := range append([]string{d}, mapOutDir(d, dir, out)...) {
		if isDir(root, c) {
			m.prefixes[c+"/"] = why
		}
	}
}

// jsonStrings collects every string leaf of a package.json field.
func jsonStrings(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case map[string]any:
		var out []string
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out = append(out, jsonStrings(t[k])...)
		}
		return out
	case []any:
		var out []string
		for _, x := range t {
			out = append(out, jsonStrings(x)...)
		}
		return out
	}
	return nil
}

var (
	tsSourceExts = []string{".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs"}
	tsOutputRe   = regexp.MustCompile(`(\.d)?\.(ts|tsx|mts|cts|js|jsx|mjs|cjs)$`)
)

func hasSourceExt(s string) bool {
	return tsOutputRe.MatchString(s) || strings.HasSuffix(s, ".py")
}

// tsOutDirs are [outDir, rootDir] pairs (relative to the package dir) a
// build output path may be mapped back with: tsconfig.json compilerOptions
// first, then the usual dist/ build/ lib/ out/ -> src/ or the package root.
func tsOutDirs(root, dir string) [][2]string {
	var pairs [][2]string
	if b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(dir), "tsconfig.json")); err == nil {
		var tc struct {
			CompilerOptions struct {
				OutDir  string `json:"outDir"`
				RootDir string `json:"rootDir"`
			} `json:"compilerOptions"`
		}
		if json.Unmarshal(stripJSONComments(b), &tc) == nil && tc.CompilerOptions.OutDir != "" {
			rd := tc.CompilerOptions.RootDir
			if rd == "" {
				rd = "src"
			}
			pairs = append(pairs, [2]string{path.Clean(tc.CompilerOptions.OutDir), path.Clean(rd)})
		}
	}
	for _, o := range []string{"dist", "build", "lib", "out", "esm", "cjs", "es", "umd", "types"} {
		pairs = append(pairs, [2]string{o, "src"}, [2]string{o, "."})
	}
	return pairs
}

var jsonCommentRe = regexp.MustCompile(`(?m)^\s*//.*$`)

func stripJSONComments(b []byte) []byte { return jsonCommentRe.ReplaceAll(b, nil) }

// mapOutDir maps a path under an output dir of pkgDir to its source dirs.
func mapOutDir(p, pkgDir string, out [][2]string) []string {
	rel := strings.TrimPrefix(strings.TrimPrefix(p, pkgDir), "/")
	if pkgDir == "." {
		rel = p
	}
	var res []string
	for _, pr := range out {
		if rel == pr[0] || strings.HasPrefix(rel, pr[0]+"/") {
			res = append(res, path.Clean(path.Join(pkgDir, pr[1], strings.TrimPrefix(rel, pr[0]))))
		}
	}
	return res
}

// tsCandidates are the existing source files a path named by a manifest
// stands for: the file itself, its sources (.js -> .ts, dist/ -> src/), or
// the index of a directory.
func tsCandidates(root, dir, p string, out [][2]string) []string {
	p = strings.TrimPrefix(p, "./")
	if strings.HasPrefix(p, "/") || strings.Contains(p, "://") || strings.HasPrefix(p, "-") {
		return nil
	}
	full := path.Clean(path.Join(dir, p))
	if full == ".." || strings.HasPrefix(full, "../") {
		return nil
	}
	bases := append([]string{full}, mapOutDir(full, dir, out)...)
	var res []string
	seen := map[string]bool{}
	add := func(f string) {
		if !seen[f] && isFile(root, f) && graphLanguage(f) != "" {
			seen[f] = true
			res = append(res, f)
		}
	}
	for _, b := range bases {
		add(b)
		stem := tsOutputRe.ReplaceAllString(b, "")
		for _, e := range tsSourceExts {
			add(stem + e)
			add(path.Join(stem, "index"+e))
		}
		if len(res) > 0 {
			return res
		}
	}
	return res
}

func graphLanguage(rel string) graph.Language {
	l, _ := detectLanguage(rel)
	return l
}

var scriptSrcRe = regexp.MustCompile(`(?is)<script\b[^>]*\bsrc\s*=\s*["']([^"']+)["']`)

// loadHTML: <script src> of a page (Vite and other bundlers start there).
func (m *manifests) loadHTML(root, rel string) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return
	}
	dir := path.Dir(rel)
	scope := nearest(m.tsScopes, dir)
	for _, sm := range scriptSrcRe.FindAllSubmatch(b, -1) {
		src := string(sm[1])
		base := dir
		if strings.HasPrefix(src, "/") {
			base, src = scope, strings.TrimPrefix(src, "/")
		}
		for _, f := range tsCandidates(root, base, src, nil) {
			m.addFile(f, "<script> of "+rel)
		}
	}
}

var (
	commandTokenRe = regexp.MustCompile(`[\w@./:${}-]+`)
	pyModAttrRe    = regexp.MustCompile(`^([A-Za-z_]\w*(?:\.[A-Za-z_]\w*)*):[A-Za-z_]\w*$`)
)

func commandTokens(s string) []string { return commandTokenRe.FindAllString(s, -1) }

// loadCommands: source files and Python modules a command file runs
// (`node dist/server.js`, `python scripts/x.py`, `python -m pkg.tool`,
// `uvicorn app.main:app`, `celery -A app.worker`).
func (m *manifests) loadCommands(root, rel string, py func(fromDir, spec string) string) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil || len(b) > 1<<20 {
		return
	}
	dir := path.Dir(rel)
	why := "run by " + rel
	toks := commandTokens(string(b))
	for i, tok := range toks {
		tok = strings.Trim(tok, ".:")
		switch {
		case hasSourceExt(tok):
			for _, base := range []string{dir, "."} {
				scope := nearest(m.tsScopes, base)
				for _, f := range tsCandidates(root, base, tok, tsOutDirs(root, scope)) {
					m.addFile(f, why)
				}
				if strings.HasSuffix(tok, ".py") {
					if f := path.Clean(path.Join(base, strings.TrimPrefix(tok, "./"))); isFile(root, f) {
						m.addFile(f, why)
					}
				}
			}
		case i > 0 && (toks[i-1] == "-m" || toks[i-1] == "-A" || toks[i-1] == "--app"):
			mod := tok
			if mm := pyModAttrRe.FindStringSubmatch(tok); mm != nil {
				mod = mm[1]
			}
			if f := py(dir, mod); f != "" {
				m.addFile(f, why)
			}
		default:
			if mm := pyModAttrRe.FindStringSubmatch(tok); mm != nil {
				if f := py(dir, mm[1]); f != "" {
					m.addFile(f, why)
				}
			}
		}
	}
}

// scope is the manifest dir governing rel.
func (m *manifests) scope(rel string, lang graph.Language) string {
	dir := path.Dir(rel)
	switch lang {
	case graph.Go:
		return nearest(m.goScopes, dir)
	case graph.Python:
		return nearest(m.pyScopes, dir)
	}
	return nearest(m.tsScopes, dir)
}

// entry is why rel is an entry point ("" when it is not).
func (m *manifests) entry(rel string, lang graph.Language, u *usageCollector) string {
	if entryMarkerRe.Match(u.x.src) {
		return "depgraph:entry comment"
	}
	if why, ok := m.files[rel]; ok {
		return why
	}
	for p, why := range m.prefixes {
		if strings.HasPrefix(rel, p) {
			return why
		}
	}
	scope := m.scope(rel, lang)
	inScope := strings.TrimPrefix(rel, scope+"/")
	if scope == "." {
		inScope = rel
	}
	base := path.Base(rel)
	segs := strings.Split(path.Dir(inScope), "/")
	switch lang {
	case graph.Go:
		if strings.HasSuffix(base, "_test.go") {
			return "test file"
		}
		if u.goMain {
			return "main package"
		}
		return ""
	case graph.Python:
		return m.pyEntry(scope, inScope, base, segs, u)
	}
	return m.tsEntry(scope, inScope, base, segs)
}

var (
	tsTestRe   = regexp.MustCompile(`\.(test|spec|e2e|stories|story|bench)\.[cm]?[jt]sx?$`)
	tsConfigRe = regexp.MustCompile(`^(\.?[\w-]+\.config(\.[\w-]+)*|\.[\w-]+rc|[\w-]+\.conf|setup[\w-]*|[\w-]+\.setup|Gruntfile|gulpfile|Jakefile|webpack\.[\w.-]+|rollup\.[\w.-]+)\.[cm]?[jt]sx?$`)
	entryDirs  = map[string]bool{
		"scripts": true, "script": true, "bin": true, "tools": true, "examples": true, "example": true,
		"docs": true, "doc": true, "benchmarks": true, "bench": true, "hack": true, ".github": true,
		".husky": true, ".storybook": true,
	}
	tsTestDirs = map[string]bool{
		"__tests__": true, "__mocks__": true, "__fixtures__": true, "test": true, "tests": true,
		"spec": true, "e2e": true, "cypress": true, "playwright": true, "fixtures": true,
	}
	rootEntryNames = map[string]bool{"index": true, "main": true, "server": true, "app": true, "cli": true, "worker": true}
)

func hasSeg(segs []string, set map[string]bool) bool {
	for _, s := range segs {
		if set[s] {
			return true
		}
	}
	return false
}

func stem(base string) string {
	if i := strings.Index(base, "."); i > 0 {
		return base[:i]
	}
	return base
}

func (m *manifests) tsEntry(scope, inScope, base string, segs []string) string {
	switch {
	case strings.HasSuffix(base, ".d.ts") || strings.HasSuffix(base, ".d.mts") || strings.HasSuffix(base, ".d.cts"):
		return "declaration file"
	case tsTestRe.MatchString(base) || hasSeg(segs, tsTestDirs):
		return "test file"
	case tsConfigRe.MatchString(base):
		return "config file"
	case hasSeg(segs, entryDirs):
		return "script / tool directory"
	}
	if why, ok := m.opaque[scope]; ok {
		return why
	}
	dir := path.Dir(inScope)
	if (dir == "." || dir == "src") && rootEntryNames[stem(base)] {
		return "package entry name"
	}
	if fw, ok := m.routing[scope]; ok {
		first := segs[0]
		if first == "src" && len(segs) > 1 {
			first = segs[1]
		}
		if first == "app" || first == "pages" || first == "routes" {
			return fw + " route"
		}
		if (dir == "." || dir == "src") && (stem(base) == "middleware" || stem(base) == "instrumentation" || stem(base) == "root" || stem(base) == "entry") {
			return fw + " convention"
		}
	}
	return ""
}

var (
	pyEntryNames = map[string]bool{
		"__main__.py": true, "setup.py": true, "manage.py": true, "conftest.py": true, "noxfile.py": true,
		"fabfile.py": true, "wsgi.py": true, "asgi.py": true, "gunicorn.conf.py": true, "gunicorn_config.py": true,
	}
	pyRootEntryNames = map[string]bool{"main": true, "app": true, "server": true, "cli": true, "run": true, "worker": true}
	pyTestDirs       = map[string]bool{"tests": true, "test": true, "migrations": true, "alembic": true}
	djangoNames      = map[string]bool{
		"models": true, "admin": true, "apps": true, "urls": true, "views": true, "signals": true,
		"forms": true, "serializers": true, "tasks": true, "consumers": true, "routing": true,
		"middleware": true, "context_processors": true, "receivers": true, "handlers": true, "settings": true,
	}
	djangoDirs = map[string]bool{"management": true, "templatetags": true, "settings": true}
)

func (m *manifests) pyEntry(scope, inScope, base string, segs []string, u *usageCollector) string {
	s := strings.TrimSuffix(base, ".py")
	switch {
	case pyEntryNames[base]:
		return "Python convention (" + base + ")"
	case strings.HasPrefix(base, "test_") || strings.HasSuffix(s, "_test") || s == "tests" || hasSeg(segs, pyTestDirs):
		return "test file"
	case hasSeg(segs, entryDirs):
		return "script / tool directory"
	case u.mainGuard:
		return `if __name__ == "__main__"`
	}
	dir := path.Dir(inScope)
	if (dir == "." || dir == "src") && pyRootEntryNames[s] {
		return "package entry name"
	}
	if m.django[scope] || m.django[nearest(m.django, path.Dir(path.Join(scope, inScope)))] {
		if djangoNames[s] || strings.HasPrefix(s, "settings") || hasSeg(segs, djangoDirs) {
			return "Django convention"
		}
	}
	return ""
}
