package rules

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver/imports"
)

// pythonImport is an external import of a Python (or Ruby) file,
// classified when the rules are evaluated.
type pythonImport struct {
	pkg, file, key string
	class, basis   string // class "" when unknown; basis says why
	lang           graph.Language
	python         bool // dotted submodules
	// unlisted: an unknown import not worth listing as unclassified (a Ruby
	// constant that is likely Ruby's own)
	unlisted bool
}

// pythonImports are the external imports of the project's Python files.
type pythonImports struct{ list []pythonImport }

// pythonImports classifies the external imports of Python files from the
// project's files when the rules are evaluated: the languages: setting of
// the file's directory names the version (rules.LanguageSetting), the
// typeshed copy of the project's type checker the standard library of that
// version; anything external that is not the standard library is
// third-party. Nothing is executed, depgraph keeps no version data, and
// no setting, an unreadable version file, a conflicting pin or no typeshed
// copy leaves an import unknown (with the reason).
func (r *Repository) pythonImports(ctx context.Context, cfg rules.Config, ignore []string) (*pythonImports, error) {
	rows, err := r.query(ctx, `
		MATCH (a:Package {project: $project})-[:CONTAINS]->(f:File {language: 'python'})-[:IMPORTS]->(b:Package)
		WHERE a.external = false AND b.external = true
		  AND NOT any(rx IN $ignore WHERE a.key =~ rx OR b.key =~ rx)
		RETURN DISTINCT a.key AS pkg, f.path AS file, b.key AS key
	`, ignore, nil)
	if err != nil {
		return nil, fmt.Errorf("python imports: %w", err)
	}
	type perDir struct {
		version imports.PythonVersion
		std     *imports.PythonStdlib
		err     error
	}
	dirs := map[string]*perDir{}
	var pins *imports.PythonPins
	if r.root != "" {
		pins = imports.NewPythonPins(r.root)
	}
	out := &pythonImports{}
	for _, row := range rows {
		im := pythonImport{pkg: fmt.Sprint(row["pkg"]), file: fmt.Sprint(row["file"]), key: fmt.Sprint(row["key"]), lang: graph.Python, python: true}
		s, ok := cfg.LanguageSettingFor("python", im.file)
		switch {
		case r.root == "":
			im.basis = "the project's files are not available here"
		case !ok:
			im.basis = "no languages.python setting for " + im.file
		default:
			d := dirs[s.Dir]
			if d == nil {
				d = &perDir{}
				if d.version, d.err = imports.ReadPythonVersion(r.root, s); d.err == nil {
					d.std, d.err = imports.LoadPythonStdlib(r.root, s)
				}
				dirs[s.Dir] = d
			}
			switch {
			case d.err != nil:
				im.basis = d.err.Error()
			default:
				if c := pins.Conflict(s, d.version, im.file); c != "" {
					im.basis = c
					break
				}
				im.class, im.basis = d.std.Classify(im.key, d.version.Minor)
				im.basis = d.version.Basis() + ": " + im.basis
			}
		}
		out.list = append(out.list, im)
	}
	return out, nil
}

// extImports are the external imports of the project's Python and Ruby
// files, classified from the project's files (nil when no layer names
// external packages: nothing reads them then).
func (r *Repository) extImports(ctx context.Context, cfg rules.Config, ignore []string) (*pythonImports, error) {
	_, lp, err := r.layerPairs(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(lp, func(p rules.LayerPair) bool { return p.ToSet.HasExternal() }) {
		return nil, nil
	}
	py, err := r.pythonImports(ctx, cfg, ignore)
	if err != nil {
		return nil, err
	}
	rb, err := r.rubyImports(ctx, ignore)
	if err != nil {
		return nil, err
	}
	py.add(rb)
	return py, nil
}

// signature identifies the classification (the link is redone when a
// version file or a Gemfile.lock changed it).
func (p *pythonImports) signature() string {
	if p == nil {
		return ""
	}
	var b strings.Builder
	for _, im := range p.list {
		_, _ = fmt.Fprintf(&b, "%s\x00%s\x00%s\x00%s\x00%v\n", im.file, im.key, im.class, im.basis, im.unlisted)
	}
	return b.String()
}

// link adds the imports to the linked graph: an ExtImport per file and
// external package, with its class ("third-party", "standard"; none when
// it could not be told) and the reason. The rules match them against the
// layers like the IMPORTS of the other languages (implement.go).
//
//	(ExtImport {key, pkg, file, ext, lang, python, class, basis, unlisted})
func (p *pythonImports) link(want *linked) {
	if p == nil {
		return
	}
	for _, im := range p.list {
		props := map[string]any{"pkg": im.pkg, "file": im.file, "ext": im.key, "lang": string(im.lang),
			"python": im.python, "basis": im.basis, "unlisted": im.unlisted}
		if im.class != "" {
			props["class"] = im.class
		}
		want.node(nodeID{"ExtImport", im.file + "|" + im.key}, props)
	}
}

// add appends q's imports to p's (q may be nil).
func (p *pythonImports) add(q *pythonImports) {
	if q != nil {
		p.list = append(p.list, q.list...)
	}
}
