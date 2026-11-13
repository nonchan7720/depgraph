package deps

import (
	"bytes"
	"context"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/nonchan7720/depgraph/pkg/internal/safefile"
)

// Python distributions and Ruby gems are imported under names their
// manifests do not give (PyYAML is `yaml`, rack-cors is `Rack::Cors`). The
// names are read from where the project installs them inside the
// repository: the dist-info of a virtual environment (.venv, venv), the
// files of a vendored bundle (vendor/bundle, or BUNDLE_PATH of
// .bundle/config). A dependency that is not installed there, or whose names
// another one shares, is not known: it is left out, never reported.

// env is what is installed for a manifest.
type env struct {
	dir string // repo-relative: site-packages, or the directory of the gems
	// by dependency (as the manifest writes it)
	imports map[string][]string // the modules / require paths it is loaded as
	consts  map[string][]string // gem: the constants it declares at the top level
	groups  map[string][]string // pypi: the entry point groups it registers, other than scripts
	scripts map[string][]string // the commands it installs
	railtie map[string]bool     // gem: it hooks into Rails
	manual  map[string]bool     // gem: `require: false`, Bundler does not require it
	paths   map[string][]string // pypi: its packages in site-packages (directories and modules)
	dists   map[string]string   // pypi: every installed distribution -> its name as installed
	tops    map[string][]string // pypi: every installed distribution -> its packages
}

func newEnv(dir string) *env {
	return &env{dir: dir, imports: map[string][]string{}, consts: map[string][]string{}, groups: map[string][]string{},
		scripts: map[string][]string{}, railtie: map[string]bool{}, manual: map[string]bool{}, paths: map[string][]string{},
		dists: map[string]string{}, tops: map[string][]string{}}
}

// Constants are the top-level constants the gem name declares.
func (m Manifest) Constants(name string) []string {
	if m.env == nil {
		return nil
	}
	return m.env.consts[name]
}

// signature identifies what was read from the environment.
func (e *env) signature() string {
	if e == nil {
		return ""
	}
	var b strings.Builder
	for _, name := range slices.Sorted(maps.Keys(e.imports)) {
		b.WriteString(name + "=" + strings.Join(e.imports[name], ",") + ";" + strings.Join(e.consts[name], ",") + ";" +
			strings.Join(e.groups[name], ",") + ";" + strings.Join(e.scripts[name], ",") + "\x1f")
	}
	return b.String()
}

// ---- Python ---------------------------------------------------------------

var (
	// requirementRe: the name of a requirement (PEP 508), and what may follow it.
	requirementRe = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]*)\s*(?:$|[\[<>=!~;@ ])`)
	tomlStringRe  = regexp.MustCompile(`"([^"]*)"|'([^']*)'`)
	tomlKeyRe     = regexp.MustCompile(`^\s*("[^"]+"|'[^']+'|[A-Za-z0-9_.-]+)\s*=`)
	poetryTableRe = regexp.MustCompile(`^tool\.poetry\.(?:(?:dev-)?dependencies|group\.[^.]+\.dependencies)$`)
	pyNormRe      = regexp.MustCompile(`[-_.]+`)
)

// pyNorm is the normalized name of a distribution (PEP 503).
func pyNorm(name string) string { return pyNormRe.ReplaceAllString(strings.ToLower(name), "-") }

type declaredAt struct {
	name string
	line int
}

// readPython returns the Python manifests of dir: pyproject.toml and the
// requirements files (requirements*.txt, requirements/*.txt).
func readPython(root, dir string, sites map[string]*site) []Manifest {
	var out []Manifest
	add := func(rel string, decls []declaredAt, own string) {
		m := Manifest{Path: rel, Dir: dir, Ecosystem: PyPI}
		st := siteFor(root, dir, sites)
		if st != nil {
			m.env = newEnv(st.dir)
			m.env.dists, m.env.tops = st.names, st.tops
		}
		seen := map[string]bool{}
		for _, d := range decls {
			norm := pyNorm(d.name)
			if seen[norm] || norm == pyNorm(own) || st == nil {
				continue
			}
			seen[norm] = true
			unique := st.unique(norm)
			if len(unique) == 0 {
				continue // not installed, or imported under names another distribution shares
			}
			m.Deps = append(m.Deps, Dependency{Name: d.name, Section: "dependencies", Line: d.line})
			m.env.imports[d.name] = unique
			m.env.groups[d.name], m.env.scripts[d.name] = st.groups[norm], st.scripts[norm]
			m.env.paths[d.name] = st.tops[norm]
		}
		sort.Slice(m.Deps, func(i, j int) bool { return m.Deps[i].Name < m.Deps[j].Name })
		out = append(out, m)
	}
	if b, err := safefile.ReadManifest(root, path.Join(dir, "pyproject.toml")); err == nil {
		decls, own := pyprojectDependencies(b)
		add(path.Join(dir, "pyproject.toml"), decls, own)
	}
	var files []string
	for _, sub := range []string{"", "requirements"} {
		entries, _ := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir), sub))
		for _, e := range entries {
			name := e.Name()
			if e.Type().IsRegular() && strings.HasSuffix(name, ".txt") && (sub != "" || strings.HasPrefix(name, "requirements")) {
				files = append(files, path.Join(dir, sub, name))
			}
		}
	}
	for _, rel := range files {
		b, err := safefile.ReadManifest(root, rel)
		if err != nil {
			continue
		}
		var decls []declaredAt
		for i, line := range strings.Split(string(b), "\n") {
			if name := requirementName(line); name != "" {
				decls = append(decls, declaredAt{name, i + 1})
			}
		}
		add(rel, decls, "")
	}
	return out
}

// requirementName is the distribution a requirement line names ("" for a
// comment, an option, a URL or a path).
func requirementName(line string) string {
	line, _, _ = strings.Cut(line, "#")
	line = strings.TrimSpace(line)
	if m := requirementRe.FindStringSubmatch(line); m != nil {
		return m[1]
	}
	return ""
}

// pyprojectDependencies reads the dependencies a pyproject.toml declares
// ([project] dependencies and optional-dependencies, [dependency-groups],
// Poetry's tables) and the project's own name. It reads the few shapes
// these tables have, not TOML in general.
func pyprojectDependencies(b []byte) (decls []declaredAt, own string) {
	table, inArray := "", false
	requirements := func(text string, line int) {
		for _, sm := range tomlStringRe.FindAllStringSubmatch(text, -1) {
			if name := requirementName(sm[1] + sm[2]); name != "" {
				decls = append(decls, declaredAt{name, line})
			}
		}
	}
	for i, line := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case inArray:
			body, _, closed := strings.Cut(line, "]")
			requirements(body, i+1)
			inArray = !closed
		case strings.HasPrefix(t, "["):
			table = strings.Trim(strings.TrimSpace(strings.Trim(t, "[]")), `"'`)
		case t == "" || strings.HasPrefix(t, "#"):
		default:
			km := tomlKeyRe.FindStringSubmatch(line)
			if km == nil {
				continue
			}
			key := strings.Trim(km[1], `"'`)
			val := strings.TrimSpace(line[len(km[0]):])
			switch {
			case table == "project" && key == "name":
				if sm := tomlStringRe.FindStringSubmatch(val); sm != nil {
					own = sm[1] + sm[2]
				}
			case (table == "project" && key == "dependencies") || table == "project.optional-dependencies" || table == "dependency-groups":
				if rest, ok := strings.CutPrefix(val, "["); ok {
					body, _, closed := strings.Cut(rest, "]")
					requirements(body, i+1)
					inArray = !closed
				}
			case poetryTableRe.MatchString(table) && key != "python":
				decls = append(decls, declaredAt{key, i + 1})
			}
		}
	}
	return decls, own
}

// site is the site-packages directory of a virtual environment.
type site struct {
	dir     string
	names   map[string]string   // normalized name -> the name as installed
	tops    map[string][]string // normalized name -> its top-level modules and packages
	groups  map[string][]string // -> entry point groups other than scripts
	scripts map[string][]string // -> console scripts
	owners  map[string]int      // top-level name -> how many distributions install it
}

// unique are the top-level names only the distribution norm installs.
func (s *site) unique(norm string) []string {
	var out []string
	for _, t := range s.tops[norm] {
		if s.owners[t] == 1 {
			out = append(out, t)
		}
	}
	return out
}

// siteFor finds the site-packages for the manifests of dir: of the .venv or
// venv of dir or of a directory above it (nil when there is none).
func siteFor(root, dir string, sites map[string]*site) *site {
	for d := dir; ; d = path.Dir(d) {
		for _, venv := range []string{".venv", "venv"} {
			matches, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(d), venv, "lib", "python*", "site-packages"))
			if len(matches) == 0 {
				continue
			}
			sort.Strings(matches)
			rel, ok := relTo(root, matches[len(matches)-1])
			if !ok {
				continue
			}
			if s, ok := sites[rel]; ok {
				return s
			}
			s := readSite(root, rel)
			sites[rel] = s
			return s
		}
		if d == "." || d == "/" || d == "" {
			return nil
		}
	}
}

func readSite(root, rel string) *site {
	s := &site{dir: rel, names: map[string]string{}, tops: map[string][]string{}, groups: map[string][]string{},
		scripts: map[string][]string{}, owners: map[string]int{}}
	entries, _ := os.ReadDir(filepath.Join(root, filepath.FromSlash(rel)))
	for _, e := range entries {
		info, ok := strings.CutSuffix(e.Name(), ".dist-info")
		if !ok || !e.IsDir() {
			continue
		}
		name, _, _ := strings.Cut(info, "-")
		norm := pyNorm(name)
		s.names[norm] = name
		tops := map[string]bool{}
		if b, err := safefile.ReadManifest(root, path.Join(rel, e.Name(), "top_level.txt")); err == nil {
			for l := range strings.FieldsSeq(string(b)) {
				tops[l] = true
			}
		} else if b, err := safefile.ReadManifest(root, path.Join(rel, e.Name(), "RECORD")); err == nil {
			for l := range strings.SplitSeq(string(b), "\n") {
				file, _, _ := strings.Cut(l, ",")
				first, _, _ := strings.Cut(file, "/")
				first, _, _ = strings.Cut(first, ".") // x.py, x.cpython-312.so
				if first != "" && first != "__pycache__" && !strings.HasSuffix(strings.SplitN(file, "/", 2)[0], "-info") &&
					!strings.HasSuffix(strings.SplitN(file, "/", 2)[0], ".data") {
					tops[first] = true
				}
			}
		}
		s.tops[norm] = slices.Sorted(maps.Keys(tops))
		for _, t := range s.tops[norm] {
			s.owners[t]++
		}
		if b, err := safefile.ReadManifest(root, path.Join(rel, e.Name(), "entry_points.txt")); err == nil {
			group := ""
			for l := range strings.SplitSeq(string(b), "\n") {
				l = strings.TrimSpace(l)
				switch {
				case strings.HasPrefix(l, "["):
					group = strings.Trim(l, "[]")
					if group != "console_scripts" && group != "gui_scripts" {
						s.groups[norm] = append(s.groups[norm], group)
					}
				case group == "console_scripts" && strings.Contains(l, "="):
					script, _, _ := strings.Cut(l, "=")
					s.scripts[norm] = append(s.scripts[norm], strings.TrimSpace(script))
				}
			}
		}
	}
	return s
}

// pythonSearch starts the facts of the distributions names of m, which no
// file of the project imports. One that another installed distribution
// imports is used (Django imports the database driver the settings choose).
// For the others, what is left to find is a file that names it: its
// modules in a string (INSTALLED_APPS, a dotted path), its name or one of
// its commands in a file that is run.
func (m Manifest) pythonSearch(ctx context.Context, root string, names []string) *search {
	s := &search{m: m, facts: make(map[string]Fact, len(names)), words: map[string][]word{}}
	importedBy := m.importedByInstalled(ctx, root, names)
	for _, n := range names {
		s.facts[n] = Fact{}
		if by := importedBy[n]; by != "" {
			s.facts[n] = Fact{Use: "imported by " + by + ", which is installed"}
			continue
		}
		s.words[n] = []word{{text: n}}
		for _, mod := range m.env.imports[n] {
			if mod != n {
				s.words[n] = append(s.words[n], word{text: mod, as: " as " + mod, dotted: true})
			}
		}
		for _, script := range m.env.scripts[n] {
			if script != n {
				s.words[n] = append(s.words[n], word{text: script, bin: true})
			}
		}
	}
	s.doubt = func(n string, complete bool) string {
		norm := pyNorm(n)
		switch {
		case strings.HasPrefix(norm, "types-") || strings.HasSuffix(norm, "-stubs"):
			return "type checkers read type stubs without an import"
		case len(m.env.groups[n]) > 0:
			return "it registers entry points (" + strings.Join(m.env.groups[n], ", ") + ") that another package loads without an import"
		case !complete:
			return "the repository has too many files to search for its name"
		}
		return ""
	}
	return s
}

var pyImportRe = regexp.MustCompile(`(?m)^\s*(?:import|from)\s+([A-Za-z_][A-Za-z0-9_]*)`)

// importedByInstalled finds, for each distribution of names, another
// installed distribution whose code imports one of its modules ("" when
// none does). It reads the Python files of every other distribution of the
// environment, within the limits of the search.
func (m Manifest) importedByInstalled(ctx context.Context, root string, names []string) map[string]string {
	out := map[string]string{}
	modOf := map[string]string{} // module -> the distribution of names that installs it
	for _, n := range names {
		for _, mod := range m.env.imports[n] {
			modOf[mod] = n
		}
	}
	if len(modOf) == 0 {
		return out
	}
	read, size := 0, int64(0)
	for _, norm := range slices.Sorted(maps.Keys(m.env.tops)) {
		for _, top := range m.env.tops[norm] {
			base := filepath.Join(root, filepath.FromSlash(m.env.dir), top)
			_ = filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
				if err != nil {
					return skipOn(d)
				}
				if ctx.Err() != nil || len(out) == len(names) || read > maxSearchFiles || size > maxSearchBytes {
					return filepath.SkipAll
				}
				if d.IsDir() || !strings.HasSuffix(d.Name(), ".py") || !d.Type().IsRegular() {
					return nil
				}
				rel, ok := relTo(root, p)
				if !ok {
					return nil
				}
				read++
				b, _, err := safefile.Read(root, rel, maxSearchFile)
				if err != nil {
					return nil
				}
				size += int64(len(b))
				for _, im := range pyImportRe.FindAllSubmatch(b, -1) {
					n, ok := modOf[string(im[1])]
					if ok && out[n] == "" && pyNorm(n) != norm {
						out[n] = m.env.dists[norm]
					}
				}
				return nil
			})
		}
	}
	return out
}

// ---- Ruby -----------------------------------------------------------------

var (
	gemRe          = regexp.MustCompile(`^\s*gem\s+["']([A-Za-z0-9_.-]+)["'](.*)$`)
	gemManualRe    = regexp.MustCompile(`(?:\brequire:|:require\s*=>)\s*false\b`)
	gemLocalRe     = regexp.MustCompile(`(?:\b(?:path|git|github):|:(?:path|git|github)\s*=>)`)
	gemVersionRe   = regexp.MustCompile(`-\d[A-Za-z0-9.]*(?:-[a-z0-9_-]+)?$`)
	rbConstRe      = regexp.MustCompile(`(?m)^(?:module|class)\s+([A-Z][A-Za-z0-9_]*)`)
	bundlePathRe   = regexp.MustCompile(`(?m)^BUNDLE_PATH:\s*["']?([^"'\n]+)["']?\s*$`)
	railtieMarkers = [][]byte{[]byte("Rails::Railtie"), []byte("Rails::Engine")}
)

// maxGemFiles is how many Ruby files of one gem are read for its constants.
const maxGemFiles = 400

// readGemfile returns the Gemfile of dir with the gems whose files are in
// the bundle installed inside the repository.
func readGemfile(root, dir string) (Manifest, bool) {
	rel := path.Join(dir, "Gemfile")
	b, err := safefile.ReadManifest(root, rel)
	if err != nil {
		return Manifest{}, false
	}
	m := Manifest{Path: rel, Dir: dir, Ecosystem: Gem}
	gems := bundledGems(root, dir)
	if gems == nil {
		return m, true
	}
	m.env = newEnv(gems.dir)
	seen := map[string]bool{}
	for i, line := range strings.Split(string(b), "\n") {
		gm := gemRe.FindStringSubmatch(line)
		if gm == nil || seen[gm[1]] || gemLocalRe.MatchString(gm[2]) {
			continue
		}
		name := gm[1]
		seen[name] = true
		gemDir, ok := gems.byName[name]
		if !ok || !m.env.readGem(root, name, gemDir) {
			continue
		}
		m.env.manual[name] = gemManualRe.MatchString(gm[2])
		m.Deps = append(m.Deps, Dependency{Name: name, Section: "gem", Line: i + 1})
	}
	sort.Slice(m.Deps, func(i, j int) bool { return m.Deps[i].Name < m.Deps[j].Name })
	return m, true
}

type bundle struct {
	dir    string
	byName map[string]string // gem -> its directory, repo-relative
}

// bundledGems finds the gems installed for the Gemfile of dir inside the
// repository: <BUNDLE_PATH or vendor/bundle>/ruby/*/gems (nil when the
// bundle is installed elsewhere).
func bundledGems(root, dir string) *bundle {
	bundlePath := "vendor/bundle"
	if b, err := safefile.ReadManifest(root, path.Join(dir, ".bundle", "config")); err == nil {
		if bm := bundlePathRe.FindSubmatch(b); bm != nil {
			bundlePath = strings.TrimSpace(string(bm[1]))
		}
	}
	if path.IsAbs(bundlePath) || !inRepo(root, dir, bundlePath) {
		return nil
	}
	matches, _ := filepath.Glob(filepath.Join(root, filepath.FromSlash(dir), filepath.FromSlash(bundlePath), "ruby", "*", "gems"))
	if len(matches) == 0 {
		return nil
	}
	sort.Strings(matches)
	rel, ok := relTo(root, matches[len(matches)-1])
	if !ok {
		return nil
	}
	out := &bundle{dir: rel, byName: map[string]string{}}
	entries, _ := os.ReadDir(matches[len(matches)-1])
	for _, e := range entries {
		if e.IsDir() {
			out.byName[gemVersionRe.ReplaceAllString(e.Name(), "")] = path.Join(rel, e.Name())
		}
	}
	return out
}

// readGem reads what the gem name is required as (the entries of its lib
// directory), the constants its files declare at the top level, the
// commands it installs and whether it hooks into Rails. It reports whether
// the gem has anything to require.
func (e *env) readGem(root, name, gemDir string) bool {
	lib := filepath.Join(root, filepath.FromSlash(gemDir), "lib")
	entries, err := os.ReadDir(lib)
	if err != nil {
		return false
	}
	requires := map[string]bool{}
	for _, en := range entries {
		requires[strings.TrimSuffix(en.Name(), ".rb")] = true
	}
	consts := map[string]bool{}
	read := 0
	_ = filepath.WalkDir(lib, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return skipOn(d)
		}
		if read >= maxGemFiles {
			return filepath.SkipAll
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".rb") || !d.Type().IsRegular() {
			return nil
		}
		rel, ok := relTo(root, p)
		if !ok {
			return nil
		}
		read++
		b, _, err := safefile.Read(root, rel, maxSearchFile)
		if err != nil {
			return nil
		}
		for _, cm := range rbConstRe.FindAllSubmatch(b, -1) {
			consts[string(cm[1])] = true
		}
		if slices.ContainsFunc(railtieMarkers, func(mk []byte) bool { return bytes.Contains(b, mk) }) {
			e.railtie[name] = true
		}
		return nil
	})
	for _, bin := range []string{"exe", "bin"} {
		scripts, _ := os.ReadDir(filepath.Join(root, filepath.FromSlash(gemDir), bin))
		for _, s := range scripts {
			if s.Type().IsRegular() {
				e.scripts[name] = append(e.scripts[name], s.Name())
			}
		}
	}
	e.imports[name] = slices.Sorted(maps.Keys(requires))
	e.consts[name] = slices.Sorted(maps.Keys(consts))
	return len(requires) > 0
}

// rubySearch starts the facts of the gems names of m, which no file
// requires and whose constants no file refers to. What is left to find is
// a file that names the gem or one of its commands (a Procfile, a script, a
// configuration). A gem Bundler requires when the application boots
// (anything but `require: false`) may act without being referenced: it
// extends other classes, or Rails loads its railtie.
func (m Manifest) rubySearch(names []string) *search {
	s := &search{m: m, facts: make(map[string]Fact, len(names)), words: map[string][]word{}}
	for _, n := range names {
		s.facts[n] = Fact{}
		s.words[n] = []word{{text: n}}
		for _, script := range m.env.scripts[n] {
			if script != n {
				s.words[n] = append(s.words[n], word{text: script, bin: true})
			}
		}
	}
	s.doubt = func(n string, complete bool) string {
		switch {
		case m.env.railtie[n]:
			return "it hooks into Rails (a railtie or an engine), which loads it without a reference"
		case !m.env.manual[n]:
			return "Bundler requires it when the application boots, so it may act without being referenced"
		case !complete:
			return "the repository has too many files to search for its name"
		}
		return ""
	}
	return s
}
