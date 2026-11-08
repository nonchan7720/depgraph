package imports

import (
	"io/fs"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver"
)

// PinCandidate is a file of a directory that pins a Python version.
type PinCandidate struct {
	File    string // relative to its directory
	Version string // as pinned
}

// PlanEntry is a languages.python setting to add.
type PlanEntry struct {
	Dir         string // "." for the root
	VersionFile string // relative to Dir
	Version     string
}

// PlanUndecided is a directory whose files pin different versions: a
// person chooses.
type PlanUndecided struct {
	Dir        string
	Candidates []PinCandidate
}

// PythonPlan is what `depgraph init` proposes for languages.python.
type PythonPlan struct {
	Entries   []PlanEntry
	Undecided []PlanUndecided
	Notes     []string
}

// pinPreference orders the files of a directory that agree: the one a
// person edits to change the version first, a Dockerfile last.
var pinPreference = []string{".python-version", ".tool-versions", "mise.toml", ".mise.toml", ".config/mise.toml",
	"pyproject.toml", "pyrightconfig.json", "mypy.ini", ".mypy.ini", "setup.cfg", "Dockerfile"}

// PlanPythonVersions proposes a languages.python setting for every
// directory of root that pins a Python version and holds Python files,
// unless cfg already sets that directory. Files of one directory that
// agree on the version propose the preferred one; files that disagree are
// left to a person (Undecided). Nothing is chosen across directories:
// each directory with a pin gets its own setting.
func PlanPythonVersions(root string, cfg rules.Config) PythonPlan {
	pins := map[string][]PinCandidate{} // dir -> candidates
	hasPy := map[string]bool{}          // dirs with Python files at or below
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, ok := toRel(root, p)
		if !ok {
			return nil
		}
		if d.IsDir() {
			if rel != "." && resolver.ShouldSkipDir(rel, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		dir := path.Dir(rel)
		if strings.HasSuffix(d.Name(), ".py") {
			for x := dir; ; x = path.Dir(x) {
				if hasPy[x] {
					break
				}
				hasPy[x] = true
				if x == "." {
					break
				}
			}
			return nil
		}
		if d.Name() == "mise.toml" && path.Base(dir) == ".config" { // .config/mise.toml pins its parent
			dir = path.Dir(dir)
		}
		file, _ := filepath.Rel(filepath.Join(root, filepath.FromSlash(dir)), p)
		file = filepath.ToSlash(file)
		if !isPreferred(file) {
			return nil
		}
		if v, err := readPythonPin(root, path.Join(dir, file)); err == nil {
			pins[dir] = append(pins[dir], PinCandidate{File: file, Version: v})
		}
		return nil
	})
	set := map[string]bool{}
	for _, s := range cfg.Languages {
		if s.Lang == "python" {
			set[s.Dir] = true
		}
	}
	dirs := make([]string, 0, len(pins))
	for d := range pins {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	var plan PythonPlan
	for _, d := range dirs {
		if set[d] || !hasPy[d] {
			continue
		}
		if s, ok := cfg.LanguageSettingFor("python", path.Join(d, "x.py")); ok && s.Dir == d {
			continue // a pattern (services/*) already sets it
		}
		cs := pins[d]
		sort.Slice(cs, func(i, j int) bool { return prefIndex(cs[i].File) < prefIndex(cs[j].File) })
		agree := true
		for _, c := range cs[1:] {
			a, _ := rules.MinorVersion(c.Version)
			b, _ := rules.MinorVersion(cs[0].Version)
			agree = agree && a == b
		}
		if !agree {
			plan.Undecided = append(plan.Undecided, PlanUndecided{Dir: d, Candidates: cs})
			continue
		}
		plan.Entries = append(plan.Entries, PlanEntry{Dir: d, VersionFile: cs[0].File, Version: cs[0].Version})
		if note := outsideNote(root, d, cs[0].File); note != "" {
			plan.Notes = append(plan.Notes, note)
		}
	}
	return plan
}

func isPreferred(file string) bool { return prefIndex(file) < len(pinPreference) }

func prefIndex(file string) int {
	for i, p := range pinPreference {
		if file == p {
			return i
		}
	}
	return len(pinPreference)
}

// outsideNote warns about a version file whose target is outside the
// repository (by its relative path only).
func outsideNote(root, dir, file string) string {
	rel := path.Join(dir, file)
	real, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return ""
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return ""
	}
	if _, ok := toRel(realRoot, real); ok {
		return ""
	}
	return rel + " is a symlink to a file outside the repository: it works here, but a checkout without that file (CI, the gate's worktrees) leaves its imports unclassified"
}
