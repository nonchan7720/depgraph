package rules

import (
	"context"
	"fmt"
	"sort"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver/imports"
)

// pythonImport is an external import of a Python file, classified now.
type pythonImport struct {
	pkg, file, key string
	class, basis   string // class "" when unknown; basis says why
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
		im := pythonImport{pkg: fmt.Sprint(row["pkg"]), file: fmt.Sprint(row["file"]), key: fmt.Sprint(row["key"])}
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

func (im pythonImport) refs(thirdParty bool) (from, to rules.PackageRef) {
	return rules.PackageRef{Key: im.pkg}, rules.PackageRef{Key: im.key, External: true, Python: true, ThirdParty: thirdParty}
}

// violations are the rows of the layer violations of Python imports, in
// the shape of the store query's.
func (p *pythonImports) violations(lp []rules.LayerPair) []map[string]any {
	if p == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []map[string]any
	for _, im := range p.list {
		from, to := im.refs(im.class == graph.ClassThirdParty)
		for _, pr := range lp {
			if !pr.Reports(from, to) {
				continue
			}
			k := im.pkg + "\x00" + im.key + "\x00" + pr.From + "\x00" + pr.To + "\x00" + pr.Component
			if seen[k] {
				continue
			}
			seen[k] = true
			out = append(out, map[string]any{"from": im.pkg, "to": im.key, "fromLayer": pr.From, "toLayer": pr.To, "component": pr.Component})
		}
	}
	return out
}

// unclassified lists the unknown Python imports a wildcard of a pair would
// match if they were third-party and that the pair does not decide
// otherwise, as "key (basis)".
func (p *pythonImports) unclassified(lp []rules.LayerPair) []string {
	if p == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, im := range p.list {
		if im.class != "" {
			continue
		}
		for _, pr := range lp {
			if !pr.ToSet.HasWildcard() {
				continue
			}
			from, tp := im.refs(true)
			_, notTP := im.refs(false)
			if pr.Reports(from, tp) && !pr.Reports(from, notTP) {
				item := im.key + " (" + im.basis + ")"
				if !seen[item] {
					seen[item] = true
					out = append(out, item)
				}
				break
			}
		}
	}
	sort.Strings(out)
	return out
}
