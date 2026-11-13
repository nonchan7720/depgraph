// Package deps reads the dependencies the manifests of a repository declare
// (go.mod, package.json) and what, besides an import, says one of them is
// used: the facts of the unused-dependency rule.
//
// Only ecosystems where an import names its package exactly are read: a Go
// import path starts with the module's path, a JavaScript import with the
// package's name. A Python distribution or a Ruby gem is imported under
// names its manifest does not give, so telling "unused" from "used under
// another name" would be a guess.
//
// Nothing is executed and nothing is written; files are read with safefile.
package deps

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/nonchan7720/depgraph/pkg/internal/safefile"
)

// Ecosystems.
const (
	Go  = "go"
	Npm = "npm"
)

// extPrefix prefixes the key of a package outside the repository.
const extPrefix = "ext:"

// Dependency is one dependency a manifest declares.
type Dependency struct {
	Name string
	// Section is where the manifest declares it: require (go.mod),
	// dependencies, devDependencies or optionalDependencies (package.json).
	Section string
	// Line is the 1-based line of the declaration (0: not known).
	Line int
}

// Manifest is a manifest and the dependencies it declares.
type Manifest struct {
	Path      string // repo-relative, slash-separated
	Dir       string // its directory ("." for the root)
	Ecosystem string
	// Deps are the dependencies that can be unused, sorted by name: not
	// what the repository provides itself (a module or workspace package of
	// it, a local replace).
	Deps []Dependency

	requires []string        // go: every module required, the indirect ones too
	tools    []string        // go: the packages of the tool directives
	declared map[string]bool // npm: every package a section names, peers too
	name     string          // npm: the package's own name
}

// Languages are the languages of the files whose imports use the
// manifest's dependencies.
func (m Manifest) Languages() []string {
	if m.Ecosystem == Go {
		return []string{"go"}
	}
	return []string{"javascript", "typescript"}
}

// Governs reports whether the files in dir resolve their imports with m:
// a Go module only with its own go.mod (dir is the module's directory), a
// JavaScript file with every package.json at or above it (a package of a
// workspace resolves what a package.json above it installs).
func (m Manifest) Governs(dir string) bool {
	if dir == m.Dir {
		return true
	}
	return m.Ecosystem == Npm && (m.Dir == "." || strings.HasPrefix(dir, m.Dir+"/"))
}

// Declares reports whether m names what provides the external package key:
// a dependency of any section (peers and packages of the repository too),
// for Go a required module, direct or not.
func (m Manifest) Declares(key string) bool {
	spec, ok := strings.CutPrefix(key, extPrefix)
	if !ok {
		return false
	}
	if m.Ecosystem == Npm {
		return m.declared[spec]
	}
	return slices.ContainsFunc(m.requires, func(mod string) bool { return spec == mod || strings.HasPrefix(spec, mod+"/") })
}

// Ref names a dependency of a manifest.
type Ref struct {
	Manifest string // Manifest.Path
	Name     string
}

// Index resolves imports to the dependencies of a repository's manifests.
type Index struct {
	ms        []Manifest
	chains    map[string][]int // ecosystem + directory -> the manifests that govern it, nearest first
	providers map[string][]Ref // ecosystem + package -> every dependency that provides it
}

// NewIndex returns an index over ms.
func NewIndex(ms []Manifest) *Index {
	return &Index{ms: ms, chains: map[string][]int{}, providers: map[string][]Ref{}}
}

// ecosystemOf is the ecosystem of the files of a language ("" for one
// whose manifests are not read).
func ecosystemOf(lang string) string {
	switch lang {
	case "go":
		return Go
	case "javascript", "typescript":
		return Npm
	}
	return ""
}

// Users are the dependencies an import of the external package key uses,
// written in a file of language lang that dir governs (the directory of the
// manifest nearest to the file, or the file's own). It is the dependency of
// the nearest manifest that declares the package. When none of the file's
// manifests does, something else provides the package to it (a workspace
// that hoists, a go.work): every manifest that declares it may be the one.
func (ix *Index) Users(lang, dir, key string) []Ref {
	eco := ecosystemOf(lang)
	if eco == "" {
		return nil
	}
	chain, ok := ix.chains[eco+" "+dir]
	if !ok {
		for i, m := range ix.ms {
			if m.Ecosystem == eco && m.Governs(dir) {
				chain = append(chain, i)
			}
		}
		sort.SliceStable(chain, func(i, j int) bool { return len(ix.ms[chain[i]].Dir) > len(ix.ms[chain[j]].Dir) })
		ix.chains[eco+" "+dir] = chain
	}
	for _, i := range chain {
		m := ix.ms[i]
		if !m.Declares(key) {
			continue
		}
		if name := m.Provider(key); name != "" {
			return []Ref{{m.Path, name}}
		}
		return nil // declared, but not as a dependency that is reported
	}
	refs, ok := ix.providers[eco+" "+key]
	if !ok {
		for _, m := range ix.ms {
			if m.Ecosystem != eco {
				continue
			}
			if name := m.Provider(key); name != "" {
				refs = append(refs, Ref{m.Path, name})
			}
		}
		ix.providers[eco+" "+key] = refs
	}
	return refs
}

// Provider is the dependency of m.Deps that provides the external package
// key ("ext:<import>"), "" when none does. A Go import belongs to the
// required module with the longest path that is a prefix of it.
func (m Manifest) Provider(key string) string {
	spec, ok := strings.CutPrefix(key, extPrefix)
	if !ok {
		return ""
	}
	name := spec
	if m.Ecosystem == Go {
		name = ""
		for _, mod := range m.requires {
			if (spec == mod || strings.HasPrefix(spec, mod+"/")) && len(mod) > len(name) {
				name = mod
			}
		}
	}
	if _, ok := slices.BinarySearchFunc(m.Deps, name, func(d Dependency, n string) int { return strings.Compare(d.Name, n) }); ok {
		return name
	}
	return ""
}

// Declared returns the manifests of root's directory and of dirs
// (repo-relative directories of modules), sorted by path; one may declare no
// dependency that is reported (Deps), and still say which packages its
// files get from it. A manifest that is missing or cannot be parsed is left
// out: its own tool reports it.
func Declared(root string, dirs []string) []Manifest {
	if root == "" {
		return nil
	}
	dirs = append([]string{"."}, dirs...)
	sort.Strings(dirs)
	dirs = slices.Compact(dirs)
	var all []Manifest
	for _, dir := range dirs {
		if m, ok := readGoMod(root, dir); ok {
			all = append(all, m)
		}
		if m, ok := readPackageJSON(root, dir); ok {
			all = append(all, m)
		}
	}
	// what the repository provides itself is not a dependency
	own := map[string]bool{}
	for _, m := range all {
		if m.name != "" {
			own[m.Ecosystem+" "+m.name] = true
		}
	}
	for i, m := range all {
		all[i].Deps = slices.DeleteFunc(m.Deps, func(d Dependency) bool { return own[m.Ecosystem+" "+d.Name] })
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Path < all[j].Path })
	return all
}

// Signature identifies what the manifests declare and what is installed
// for them (the facts of an npm dependency are read from node_modules); ""
// without manifests.
func Signature(root string, ms []Manifest) string {
	var b strings.Builder
	for _, m := range ms {
		state := ""
		if m.Ecosystem == Npm && len(m.Deps) > 0 {
			state = installState(root, m.Dir)
		}
		_, _ = fmt.Fprintf(&b, "%s\x00%s", m.Path, state)
		for _, d := range m.Deps {
			_, _ = fmt.Fprintf(&b, "\x00%s\x1f%s\x1f%d", d.Name, d.Section, d.Line)
		}
		_, _ = fmt.Fprintf(&b, "\x00%s\x00%s\x00%s\n", strings.Join(m.tools, "\x1f"), strings.Join(m.requires, "\x1f"),
			strings.Join(slices.Sorted(maps.Keys(m.declared)), "\x1f"))
	}
	return b.String()
}

func readGoMod(root, dir string) (Manifest, bool) {
	rel := path.Join(dir, "go.mod")
	b, err := safefile.ReadManifest(root, rel)
	if err != nil {
		return Manifest{}, false
	}
	f, err := modfile.Parse(rel, b, nil)
	if err != nil {
		return Manifest{}, false
	}
	m := Manifest{Path: rel, Dir: dir, Ecosystem: Go}
	if f.Module != nil {
		m.name = f.Module.Mod.Path
	}
	local := map[string]bool{} // replaced by a directory of the repository
	for _, rep := range f.Replace {
		if rep.New.Version == "" && inRepo(root, dir, rep.New.Path) {
			local[rep.Old.Path] = true
		}
	}
	for _, req := range f.Require {
		m.requires = append(m.requires, req.Mod.Path)
		if req.Indirect || local[req.Mod.Path] {
			continue
		}
		d := Dependency{Name: req.Mod.Path, Section: "require"}
		if req.Syntax != nil {
			d.Line = req.Syntax.Start.Line
		}
		m.Deps = append(m.Deps, d)
	}
	for _, t := range f.Tool {
		m.tools = append(m.tools, t.Path)
	}
	sort.Strings(m.tools)
	sort.Slice(m.Deps, func(i, j int) bool { return m.Deps[i].Name < m.Deps[j].Name })
	return m, true
}

// inRepo reports whether p (a directory of a replace, relative to dir or
// absolute) is inside root.
func inRepo(root, dir, p string) bool {
	abs := filepath.FromSlash(p)
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, filepath.FromSlash(dir), abs)
	}
	rel, err := filepath.Rel(root, filepath.Clean(abs))
	return err == nil && rel != ".." && !strings.HasPrefix(filepath.ToSlash(rel), "../")
}

// npmSections are the sections of package.json whose packages the package
// itself needs. peerDependencies is a statement to who installs it.
var npmSections = []string{"dependencies", "devDependencies", "optionalDependencies"}

type packageJSON struct {
	Name     string            `json:"name"`
	Deps     map[string]string `json:"dependencies"`
	DevDeps  map[string]string `json:"devDependencies"`
	OptDeps  map[string]string `json:"optionalDependencies"`
	PeerDeps map[string]string `json:"peerDependencies"`
	Bin      json.RawMessage   `json:"bin"`
}

func (p packageJSON) section(name string) map[string]string {
	switch name {
	case "dependencies":
		return p.Deps
	case "devDependencies":
		return p.DevDeps
	}
	return p.OptDeps
}

// localSpecs start the version of a package that is a directory, not a
// package of a registry: its imports resolve inside the repository.
var localSpecs = []string{"workspace:", "link:", "file:", "portal:"}

func readPackageJSON(root, dir string) (Manifest, bool) {
	rel := path.Join(dir, "package.json")
	b, err := safefile.ReadManifest(root, rel)
	if err != nil {
		return Manifest{}, false
	}
	var pj packageJSON
	if json.Unmarshal(b, &pj) != nil {
		return Manifest{}, false
	}
	m := Manifest{Path: rel, Dir: dir, Ecosystem: Npm, name: pj.Name, declared: map[string]bool{}}
	lines := sectionLines(b)
	seen := map[string]bool{}
	for _, section := range npmSections {
		for name, spec := range pj.section(section) {
			m.declared[name] = true
			if seen[name] || slices.ContainsFunc(localSpecs, func(p string) bool { return strings.HasPrefix(spec, p) }) {
				continue
			}
			seen[name] = true
			m.Deps = append(m.Deps, Dependency{Name: name, Section: section, Line: lines[section+" "+name]})
		}
	}
	for name := range pj.PeerDeps {
		m.declared[name] = true
	}
	sort.Slice(m.Deps, func(i, j int) bool { return m.Deps[i].Name < m.Deps[j].Name })
	return m, true
}

var (
	sectionRe = regexp.MustCompile(`"(dependencies|devDependencies|optionalDependencies)"\s*:\s*\{`)
	keyRe     = regexp.MustCompile(`"((?:[^"\\]|\\.)+)"\s*:`)
)

// sectionLines are the lines of the packages a package.json declares, by
// "section name". The sections hold no nested object, so one ends at the
// next "}".
func sectionLines(b []byte) map[string]int {
	out := map[string]int{}
	section := ""
	for i, line := range strings.Split(string(b), "\n") {
		for line != "" {
			if section == "" {
				loc := sectionRe.FindStringSubmatchIndex(line)
				if loc == nil {
					break
				}
				section, line = line[loc[2]:loc[3]], line[loc[1]:]
				continue
			}
			body, rest, closed := strings.Cut(line, "}")
			for _, km := range keyRe.FindAllStringSubmatch(body, -1) {
				if k := section + " " + km[1]; out[k] == 0 {
					out[k] = i + 1
				}
			}
			if !closed {
				break
			}
			section, line = "", rest
		}
	}
	return out
}

// installMarkers are the files package managers rewrite in node_modules on
// every install (npm, pnpm, yarn).
var installMarkers = []string{".package-lock.json", ".modules.yaml", ".yarn-integrity", ".yarn-state.yml"}

// installState identifies what is installed for dir: when the node_modules
// of dir, or of the nearest directory above it that has one, last changed
// ("" when nothing is installed).
func installState(root, dir string) string {
	for d := dir; ; d = path.Dir(d) {
		nm := filepath.Join(root, filepath.FromSlash(d), "node_modules")
		if st, err := os.Stat(nm); err == nil && st.IsDir() {
			state := []string{fmt.Sprint(st.ModTime().UnixNano())}
			for _, f := range installMarkers {
				if st, err := os.Stat(filepath.Join(nm, f)); err == nil {
					state = append(state, fmt.Sprint(st.ModTime().UnixNano()))
				}
			}
			return strings.Join(state, " ")
		}
		if d == "." || d == "/" || d == "" {
			return ""
		}
	}
}
