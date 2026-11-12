package rules

import (
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// loadFile is a file with what it loads and the names it declares.
type loadFile struct {
	loads    []graph.Use
	declares map[string]bool
}

// fileCycles finds the cycles between the files of one directory from
// what each file loads (graph.Usage.Loads). A cycle through several
// directories is a cycle of packages, which import-cycle reports, so only
// loads inside a directory are edges here.
func (r *Repository) fileCycles(ctx context.Context, cfg rules.Config, ignore []string) ([]rules.Finding, error) {
	rows, err := r.query(ctx, `
		MATCH (p:Package {project: $project})-[:CONTAINS]->(f:File)
		WHERE p.external = false AND none(rx IN $ignore WHERE p.key =~ rx)
		OPTIONAL MATCH (f)-[:DECLARES]->(s:Symbol)
		RETURN f.path AS file, f.loads AS loads, collect(s.name) AS names
	`, ignore, nil)
	if err != nil {
		return nil, fmt.Errorf("file cycles: %w", err)
	}
	files := make(loadFiles, len(rows))
	for _, row := range rows {
		f := &loadFile{declares: map[string]bool{}}
		for _, s := range toStrings(row["loads"]) {
			if u, ok := graph.DecodeUse(s); ok {
				f.loads = append(f.loads, u)
			}
		}
		for _, n := range toStrings(row["names"]) {
			f.declares[n] = true
		}
		files[fmt.Sprint(row["file"])] = f
	}
	var loads []rules.FileLoad
	for from, f := range files {
		for _, u := range f.loads {
			to, module := files.loaded(u, 0)
			if to == "" || path.Dir(to) != path.Dir(from) {
				continue
			}
			if module && path.Base(to) == "__init__.py" {
				// its own package, imported as a module: the import system
				// loaded the package before this module, with or without it
				continue
			}
			loads = append(loads, rules.FileLoad{From: from, To: to, Deferred: u.As == graph.LoadDeferred})
		}
	}
	out := rules.FileCycleFindings(loads, cfg.FileCycleDeferred)
	for i := range out {
		out[i].Severity = string(cfg.Severity(rules.RuleFileCycle))
	}
	return out, nil
}

// loadFiles are the project's files by path.
type loadFiles map[string]*loadFile

// maxReExports bounds how many packages a name is followed through.
const maxReExports = 5

// loaded is the file of the project a load names: the file itself, or for
// a Python package directory the submodule the imported name is, else the
// module the name comes from: the package's __init__.py when that declares
// it, or the module the __init__.py imports it from (a module that takes a
// name its package only passes on depends on the module that declares it,
// not on the package). "" when it is none, or cannot be told (the
// __init__.py does not declare the name but has a wildcard import).
// module: the load is of the file as a whole module (import pkg, from pkg
// import submodule), not of a name in it.
func (fs loadFiles) loaded(u graph.Use, depth int) (file string, module bool) {
	if fs[u.Target] != nil {
		return u.Target, u.Name == ""
	}
	first, _, _ := strings.Cut(u.Name, ".")
	if first != "" {
		for _, mod := range []string{first + ".py", first + ".pyi", first + "/__init__.py"} {
			if p := path.Join(u.Target, mod); fs[p] != nil {
				return p, true
			}
		}
	}
	init := path.Join(u.Target, "__init__.py")
	pkg := fs[init]
	switch {
	case pkg == nil:
		return "", false
	case first == "":
		return init, true
	case pkg.declares[first]:
		return init, false
	case depth >= maxReExports:
		return "", false
	}
	for _, l := range pkg.loads {
		if name, _, _ := strings.Cut(l.Name, "."); name == first {
			return fs.loaded(l, depth+1)
		}
	}
	return "", false
}
