package deps

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver"
	"github.com/nonchan7720/depgraph/pkg/internal/safefile"
)

// Fact is what the repository says about a dependency no code imports.
type Fact struct {
	// Use: it is used all the same, and how ("named in babel.config.json",
	// "a peer dependency of some-ui", "a tool of go.mod").
	Use string
	// Doubt: nothing shows a use, but one that is not an import cannot be
	// ruled out, and why.
	Doubt string
}

// Lookup is what the graph knows.
type Lookup struct {
	// Imported reports whether a file the manifest governs imports the
	// package name.
	Imported func(m Manifest, name string) bool
	// Scanned reports whether the file rel is in the graph (its imports
	// are known).
	Scanned func(rel string) bool
}

// Facts are the facts of the dependencies no code imports: open maps a
// manifest (its path) to their names, the result a manifest to the fact of
// each. A dependency with an empty Fact is unused for certain. It stops
// with ctx's error when ctx ends before every file was read.
func Facts(ctx context.Context, root string, ms []Manifest, open map[string][]string, l Lookup) (map[string]map[string]Fact, error) {
	out := map[string]map[string]Fact{}
	var searches []*search
	for _, m := range ms {
		names := open[m.Path]
		if len(names) == 0 {
			continue
		}
		var s *search
		switch m.Ecosystem {
		case Go:
			out[m.Path] = m.goFacts(ctx, root, names, l)
			continue
		case PyPI:
			s = m.pythonSearch(ctx, root, names)
		case Gem:
			s = m.rubySearch(names)
		default:
			s = m.npmSearch(root, names, l)
		}
		out[m.Path] = s.facts
		searches = append(searches, s)
	}
	complete := searchNames(ctx, root, NewIndex(ms), searches)
	for _, s := range searches {
		s.conclude(complete)
	}
	return out, ctx.Err()
}

// goFacts: a module is used without an import of a scanned file when go.mod
// names a tool of it, or a Go file of the module that is not in the graph
// imports it (the go command reads every Go file of the module, whatever
// depgraph was told to leave out). Nothing else keeps a requirement: this
// is what `go mod tidy` decides from.
func (m Manifest) goFacts(ctx context.Context, root string, names []string, l Lookup) map[string]Fact {
	facts := make(map[string]Fact, len(names))
	var open []string
	for _, n := range names {
		facts[n] = Fact{}
		if slices.ContainsFunc(m.tools, func(t string) bool { return t == n || strings.HasPrefix(t, n+"/") }) {
			facts[n] = Fact{Use: "a tool of " + m.Path}
			continue
		}
		open = append(open, n)
	}
	if len(open) == 0 {
		return facts
	}
	base := filepath.Join(root, filepath.FromSlash(m.Dir))
	_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return skipOn(d)
		}
		if len(open) == 0 || ctx.Err() != nil {
			return filepath.SkipAll
		}
		rel, ok := relTo(root, p)
		if !ok {
			return nil
		}
		if d.IsDir() {
			if p != base && (skipGoDir(d.Name()) || isFile(filepath.Join(p, "go.mod"))) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || !d.Type().IsRegular() || (l.Scanned != nil && l.Scanned(rel)) {
			return nil
		}
		b, err := safefile.ReadSource(root, rel)
		if err != nil {
			return nil
		}
		open = slices.DeleteFunc(open, func(n string) bool {
			if !bytes.Contains(b, []byte(`"`+n+`"`)) && !bytes.Contains(b, []byte(`"`+n+`/`)) {
				return false
			}
			facts[n] = Fact{Use: "imported by " + rel + ", which is not scanned"}
			return true
		})
		return nil
	})
	return facts
}

// skipGoDir: the directories the go command does not read.
func skipGoDir(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" || name == "vendor"
}

func skipOn(d fs.DirEntry) error {
	if d != nil && d.IsDir() {
		return filepath.SkipDir
	}
	return nil
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func relTo(root, abs string) (string, bool) {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// compilerHelpers are the packages compilers import from what they build
// (TypeScript's importHelpers, Babel's and SWC's runtime): the source never
// names them.
var compilerHelpers = map[string]bool{
	"tslib": true, "core-js": true, "regenerator-runtime": true, "@babel/runtime": true, "@swc/helpers": true,
}

// word is a word that names a dependency: its name, another name it goes
// by (as: " as x"; dotted: a module, also named by a dotted path below it)
// or a binary it installs.
type word struct {
	text   string
	as     string
	bin    bool
	dotted bool
}

// search is the search for what names the dependencies of one manifest
// that nothing imports.
type search struct {
	m     Manifest
	facts map[string]Fact
	words map[string][]word // dependency -> the words that name it; removed when found
	// doubt says why a dependency nothing names may be used all the same
	// ("" when nothing says so). complete: every file was searched.
	doubt func(name string, complete bool) string
}

// npmSearch starts the facts of the packages names of m: a package nothing
// imports is used when it holds the types of a package that is used or
// another dependency needs it as a peer. For the others, what is left to
// find is a file that names it or one of its binaries (searchNames).
func (m Manifest) npmSearch(root string, names []string, l Lookup) *search {
	s := &search{m: m, facts: make(map[string]Fact, len(names)), words: map[string][]word{}}
	own := map[string]*packageJSON{} // the package.json of the installed packages
	var missing []string             // declared packages that are not installed
	peerOf := map[string]string{}    // package -> a declared dependency that needs it as a peer
	for _, name := range slices.Sorted(maps.Keys(m.declared)) {
		pj := installedPackage(root, m.Dir, name)
		if pj == nil {
			missing = append(missing, name)
			continue
		}
		own[name] = pj
		for _, p := range slices.Sorted(maps.Keys(pj.PeerDeps)) {
			if p != name && peerOf[p] == "" {
				peerOf[p] = name
			}
		}
	}
	for _, n := range names {
		s.facts[n] = Fact{}
		base := typesOf(n)
		switch {
		case base != "" && (base == "node" || m.declared[base] || (l.Imported != nil && l.Imported(m, base))):
			s.facts[n] = Fact{Use: "the types of " + base}
		case peerOf[n] != "":
			s.facts[n] = Fact{Use: "a peer dependency of " + peerOf[n]}
		default:
			s.words[n] = []word{{text: n}}
			if base != "" {
				s.words[n] = append(s.words[n], word{text: base, as: " as " + base})
			}
			if _, short := addon(n); short != "" {
				s.words[n] = append(s.words[n], word{text: short, as: " as " + short})
			}
			if pj := own[n]; pj != nil {
				for _, b := range binNames(n, pj.Bin) {
					s.words[n] = append(s.words[n], word{text: b, bin: true})
				}
			}
		}
	}
	s.doubt = func(n string, complete bool) string { return m.npmDoubt(n, own[n], missing, complete) }
	return s
}

// conclude gives the dependencies no file names their doubt.
func (s *search) conclude(complete bool) {
	for n := range s.words {
		s.facts[n] = Fact{Doubt: s.doubt(n, complete)}
	}
}

// npmDoubt says why the package n, which nothing imports or names, may be
// used all the same ("" when nothing says so). pj is its own package.json
// (nil: not installed).
func (m Manifest) npmDoubt(n string, pj *packageJSON, missing []string, complete bool) string {
	if typesOf(n) != "" {
		return "TypeScript includes type declarations without an import"
	}
	if compilerHelpers[n] {
		return "compilers insert the imports of it into what they build"
	}
	if pj != nil {
		for _, p := range slices.Sorted(maps.Keys(pj.PeerDeps)) {
			if p != n && m.declared[p] {
				return "it is an add-on of " + p + " (its peer), which may load it without an import"
			}
		}
	}
	if host := m.hostByName(n); host != "" {
		return "its name marks it as an add-on of " + host + ", which may load it without an import"
	}
	if kind, _ := addon(n); kind != "" {
		return "its name marks it as an add-on (a " + kind + "), which its host may load without an import"
	}
	switch {
	case pj == nil:
		return "it is not installed, so its binaries and its peers could not be read"
	case len(missing) == 1:
		return missing[0] + " is not installed, so whether it needs this package as a peer could not be read"
	case len(missing) > 1:
		return fmt.Sprintf("%d declared packages are not installed, so whether they need this package as a peer could not be read", len(missing))
	case !complete:
		return "the repository has too many files to search for its name"
	}
	return ""
}

// hostByName is the declared package whose add-on n is by the way add-ons
// are named: eslint-plugin-x for eslint, @vitest/coverage-v8 for vitest,
// babel-plugin-x and @babel/preset-env for @babel/core. Hosts find such
// packages by name (a configuration says "x", or nothing at all).
func (m Manifest) hostByName(n string) string {
	scope, name := splitScope(n)
	for _, host := range slices.Sorted(maps.Keys(m.declared)) {
		if host == n {
			continue
		}
		hostScope, hostName := splitScope(host)
		tokens := []string{hostName}
		if hostScope != "" && (hostName == "core" || hostName == "cli") {
			tokens = append(tokens, hostScope)
		}
		for _, t := range tokens {
			if strings.HasPrefix(name, t+"-") || scope == t {
				return host
			}
		}
	}
	return ""
}

// addonRe: the words by which the name of a package says what it adds to
// another one, and what follows them.
var addonRe = regexp.MustCompile(`^(?:.+-)?(plugin|preset|config|loader|addon|adapter|transformer|reporter|environment|resolver|formatter|launcher)(?:-(.+))?$`)

// addon tells whether n is an add-on by its name: kind is the word that
// says so ("plugin" of babel-plugin-x, "config" of eslint-config-airbnb,
// "loader" of sass-loader; "" when none does) and short the name its host
// knows it by (x, airbnb; "" when there is none): a configuration names an
// add-on without the prefix.
func addon(n string) (kind, short string) {
	_, name := splitScope(n)
	if m := addonRe.FindStringSubmatch(name); m != nil {
		return m[1], m[2]
	}
	return "", ""
}

// splitScope splits "@scope/name" ("" for a package without a scope).
func splitScope(n string) (scope, name string) {
	if rest, ok := strings.CutPrefix(n, "@"); ok {
		if s, nm, ok := strings.Cut(rest, "/"); ok {
			return s, nm
		}
	}
	return "", n
}

// typesOf is the package whose types the DefinitelyTyped package n holds
// (@types/react: react; @types/babel__core: @babel/core), "" for any other
// package.
func typesOf(n string) string {
	base, ok := strings.CutPrefix(n, "@types/")
	if !ok || base == "" {
		return ""
	}
	if scope, name, ok := strings.Cut(base, "__"); ok {
		return "@" + scope + "/" + name
	}
	return base
}

// binNames are the commands a package installs (bin of its package.json: a
// path, named after the package, or a map), other than its own name.
func binNames(pkg string, raw json.RawMessage) []string {
	var byName map[string]string
	if json.Unmarshal(raw, &byName) != nil {
		return nil // a path: the command is the package's name
	}
	_, own := splitScope(pkg)
	out := make([]string, 0, len(byName))
	for name := range byName {
		if name != pkg && name != own {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// installedPackage reads the package.json of the package name as installed
// for dir: in the node_modules of dir or of a directory above it.
func installedPackage(root, dir, name string) *packageJSON {
	for d := dir; ; d = path.Dir(d) {
		if b, err := safefile.ReadManifest(root, path.Join(d, "node_modules", name, "package.json")); err == nil {
			var pj packageJSON
			if json.Unmarshal(b, &pj) != nil {
				return nil
			}
			return &pj
		}
		if d == "." || d == "/" || d == "" {
			return nil
		}
	}
}

// The search for names reads at most maxSearchFiles files of at most
// maxSearchFile bytes each (a larger one is a bundle or data, not a
// configuration) and maxSearchBytes in all; past that it stops and says so.
var (
	maxSearchFiles       = 50000
	maxSearchFile  int64 = 1 << 20
	maxSearchBytes int64 = 256 << 20
)

// notUses are files that name packages without using them: lockfiles, and
// what tells an update bot which packages to touch.
var notUses = map[string]bool{
	"package-lock.json": true, "npm-shrinkwrap.json": true, "yarn.lock": true, "pnpm-lock.yaml": true,
	"bun.lock": true, "bun.lockb": true, "deno.lock": true,
	"renovate.json": true, "renovate.json5": true, ".renovaterc": true, ".renovaterc.json": true, "dependabot.yml": true,
	// the manifests and lockfiles of Python and Ruby
	"pyproject.toml": true, "setup.py": true, "setup.cfg": true, "Pipfile": true, "Pipfile.lock": true, "poetry.lock": true,
	"uv.lock": true, "pdm.lock": true, "Gemfile": true, "Gemfile.lock": true, "gems.rb": true, "gems.locked": true,
}

// docExts are documentation: prose about a package is not a use of it.
var docExts = map[string]bool{".md": true, ".markdown": true, ".txt": true, ".rst": true, ".adoc": true}

// codeExts are source files: a package is named in them by a string (an
// import depgraph does not parse, require.resolve, a loader or a driver
// given by name, a path into node_modules), not by a word of a comment or
// an identifier.
var codeExts = map[string]bool{
	".js": true, ".jsx": true, ".mjs": true, ".cjs": true, ".ts": true, ".tsx": true, ".mts": true, ".cts": true,
	".vue": true, ".svelte": true, ".astro": true, ".mdx": true, ".gjs": true, ".gts": true,
	".go": true, ".py": true, ".pyi": true, ".rb": true,
}

// searchNames looks for the words of the packages of every search in the
// files of the repository: every file but documentation, lockfiles, the
// directories no scan reads (node_modules, build output) and the dependency
// sections of the package.json files. A file names a package for the
// manifest its own imports would use (Users): a CI workflow at the root
// names the tools of every package.json, a file of a package that declares
// the package itself only its own. It reports whether every file was
// searched.
func searchNames(ctx context.Context, root string, ix *Index, searches []*search) (complete bool) {
	left := 0
	for _, s := range searches {
		left += len(s.words)
	}
	if left == 0 {
		return true
	}
	read, size := 0, int64(0)
	complete = true
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return skipOn(d)
		}
		if left == 0 || !complete || ctx.Err() != nil {
			return filepath.SkipAll
		}
		name := d.Name()
		if d.IsDir() {
			if p != root && resolver.SkipDirs[name] {
				return filepath.SkipDir
			}
			return nil
		}
		ext := strings.ToLower(path.Ext(name))
		if !d.Type().IsRegular() || notUses[name] || docExts[ext] {
			return nil
		}
		rel, ok := relTo(root, p)
		if !ok {
			return nil
		}
		if read++; read > maxSearchFiles || size > maxSearchBytes {
			complete = false
			return filepath.SkipAll
		}
		b, _, err := safefile.Read(root, rel, maxSearchFile)
		size += int64(len(b))
		if err != nil || bytes.IndexByte(b[:min(len(b), 512)], 0) >= 0 {
			return nil // too large, unreadable or binary
		}
		if name == "package.json" {
			b = withoutDependencies(b)
		}
		dir := path.Dir(rel)
		for _, s := range searches {
			for dep, ws := range s.words {
				for _, w := range ws {
					// a binary is a word of a command, wherever it is written
					if !mentions(b, w.text, codeExts[ext] && !w.bin, w.dotted) || !s.usedFrom(ix, dir, dep) {
						continue
					}
					use := "named in " + rel + w.as
					if w.bin {
						use = "its binary " + w.text + " is named in " + rel
					}
					s.facts[dep] = Fact{Use: use}
					delete(s.words, dep)
					left--
					break
				}
			}
		}
		return nil
	})
	return complete
}

// usedFrom reports whether a file in dir that names the dependency dep
// uses the declaration of s.m: of the manifest its imports would use
// (JavaScript), of every manifest (Python and Ruby: one environment).
func (s *search) usedFrom(ix *Index, dir, dep string) bool {
	if s.m.Ecosystem != Npm {
		return true
	}
	return slices.Contains(ix.Users("javascript", dir, extPrefix+dep), Ref{s.m.Path, dep})
}

// withoutDependencies is a package.json without the sections that list
// packages (declaring a package is not using it): what is left are its
// scripts and the configuration tools keep in it.
func withoutDependencies(b []byte) []byte {
	var top map[string]json.RawMessage
	if json.Unmarshal(b, &top) != nil {
		return b
	}
	for _, k := range []string{"dependencies", "devDependencies", "peerDependencies", "optionalDependencies",
		"bundledDependencies", "bundleDependencies", "peerDependenciesMeta", "overrides", "resolutions", "pnpm"} {
		delete(top, k)
	}
	out, err := json.Marshal(top)
	if err != nil {
		return b
	}
	return out
}

// mentions reports whether text names word: as a whole word (a package
// name ends where another could go on: react is not named by react-dom,
// lodash not by lodash.debounce, core not by @babel/core), or, when quoted,
// as a string or the start of one ('pg', "pg/lib"), or in the path of an
// installed package ("node_modules/pg/lib"). dotted: a quoted module is
// also named by a dotted path below it ('storages.backends.s3').
func mentions(text []byte, word string, quoted, dotted bool) bool {
	w := []byte(word)
	for at := 0; ; {
		i := bytes.Index(text[at:], w)
		if i < 0 {
			return false
		}
		start, end := at+i, at+i+len(w)
		at = start + 1
		if quoted {
			if start > 0 && (isQuote(text[start-1]) || installPath(text[:start])) && end < len(text) && (isQuote(text[end]) || text[end] == '/' || (dotted && text[end] == '.')) {
				return true
			}
			continue
		}
		if startsWord(text, start) && endsWord(text, end) {
			return true
		}
	}
}

func isQuote(c byte) bool { return c == '"' || c == '\'' || c == '`' }

func isNameByte(c byte) bool {
	return c == '_' || c == '-' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// installPath reports whether before ends with the path to where packages
// (node_modules/) or their binaries (.bin/) are installed.
func installPath(before []byte) bool {
	return bytes.HasSuffix(before, []byte("node_modules/")) || bytes.HasSuffix(before, []byte(".bin/"))
}

func startsWord(text []byte, start int) bool {
	if start == 0 {
		return true
	}
	switch c := text[start-1]; c {
	case '/':
		return installPath(text[:start])
	case '.', '@':
		return false
	default:
		return !isNameByte(c)
	}
}

func endsWord(text []byte, end int) bool {
	if end == len(text) {
		return true
	}
	if c := text[end]; c == '.' {
		return end+1 == len(text) || !isNameByte(text[end+1])
	} else if isNameByte(c) {
		return false
	}
	return true
}
