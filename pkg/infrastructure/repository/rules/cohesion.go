package rules

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// lowCohesion builds the units of low-cohesion from the stored usage: Go
// packages (files linked when one uses a name another declares) and
// TypeScript / Python modules (declarations linked by their references),
// with the code outside each unit that uses its names. Test and generated
// files are neither parts nor users. A unit used whole (a Go dot import, a
// namespace used as a value, export *) or that is an entry point (it may
// have users outside the repository) is left out.
func (r *Repository) lowCohesion(ctx context.Context, cfg rules.Config, ignore []string) ([]rules.Finding, error) {
	rows, err := r.query(ctx, `
		MATCH (p:Package {project: $project})-[:CONTAINS]->(f:File)
		WHERE p.external = false AND none(rx IN $ignore WHERE p.key =~ rx) AND coalesce(f.generated, false) = false
		  AND f.language IN ['go', 'typescript', 'javascript', 'python']
		RETURN p.key AS pkg, f.path AS file, f.language AS lang, f.entry AS entry, f.decls AS decls, f.idents AS idents,
		       f.declRefs AS declRefs, f.uses AS uses, f.exports AS exports
	`, ignore, nil)
	if err != nil {
		return nil, fmt.Errorf("low cohesion: %w", err)
	}
	type file struct {
		pkg, path, lang string
		entry           bool
		decls, idents   []string
		declRefs        []graph.DeclRef
		exports         map[string]string // exported name -> local
	}
	var files []file
	users := map[string]map[string][]string{} // unit -> name -> users
	whole := map[string]bool{}                // units used whole
	addUser := func(unit, name, user string) {
		if users[unit] == nil {
			users[unit] = map[string][]string{}
		}
		users[unit][name] = append(users[unit][name], user)
	}
	byPath := map[string]*file{}
	for _, row := range rows {
		f := file{pkg: fmt.Sprint(row["pkg"]), path: fmt.Sprint(row["file"]), lang: fmt.Sprint(row["lang"]), exports: map[string]string{}}
		if rules.IsTestFile(f.path) {
			continue
		}
		f.entry = row["entry"] != nil && row["entry"] != ""
		f.decls, f.idents = toStrings(row["decls"]), toStrings(row["idents"])
		for _, s := range toStrings(row["declRefs"]) {
			if d, ok := graph.DecodeDeclRef(s); ok {
				f.declRefs = append(f.declRefs, d)
			}
		}
		for _, s := range toStrings(row["exports"]) {
			if e, ok := graph.DecodeExportName(s); ok {
				f.exports[e.Name] = e.Local
			}
		}
		files = append(files, f)
		byPath[f.path] = &files[len(files)-1]
		user := f.path
		if f.lang == string(graph.Go) {
			user = f.pkg
		}
		for _, s := range toStrings(row["uses"]) {
			u, ok := graph.DecodeUse(s)
			if !ok || u.Target == "" || u.Name == "" {
				continue
			}
			unit := strings.TrimSuffix(u.Target, "/")
			if unit == user || (f.lang == string(graph.Go) && unit == f.pkg) {
				continue // inside the unit
			}
			if u.Name == "*" {
				whole[unit] = true
				continue
			}
			name, _, _ := strings.Cut(u.Name, ".") // Python: sub.f of a package
			addUser(unit, name, user)
		}
	}
	// TypeScript: what a module exports under another name is its local
	for unit, names := range users {
		if f := byPath[unit]; f != nil {
			for name, us := range names {
				if local, ok := f.exports[name]; ok && local != name {
					names[local] = append(names[local], us...)
				}
			}
		}
	}
	var units []rules.CohesionUnit
	goPkgs := map[string][]*file{}
	for i := range files {
		f := &files[i]
		if f.lang == string(graph.Go) {
			goPkgs[f.pkg] = append(goPkgs[f.pkg], f)
			continue
		}
		if len(f.declRefs) == 0 || f.entry || whole[f.path] || users[f.path] == nil {
			continue
		}
		u := rules.CohesionUnit{Key: f.path, What: "module", Users: users[f.path]}
		at := map[string]int{}
		for i, d := range f.declRefs {
			u.Parts = append(u.Parts, rules.CohesionPart{ID: d.Name, Names: []string{d.Name}})
			at[d.Name] = i
		}
		for i, d := range f.declRefs {
			for _, ref := range d.Refs {
				if j, ok := at[ref]; ok {
					u.Links = append(u.Links, [2]int{i, j})
				}
			}
		}
		units = append(units, u)
	}
	for pkg, fs := range goPkgs {
		if whole[pkg] || users[pkg] == nil || slices.ContainsFunc(fs, func(f *file) bool { return f.entry }) {
			continue
		}
		u := rules.CohesionUnit{Key: pkg, What: "package", Users: users[pkg]}
		declaredIn := map[string]int{}
		for i, f := range fs {
			u.Parts = append(u.Parts, rules.CohesionPart{ID: f.path[strings.LastIndexByte(f.path, '/')+1:], Names: f.decls})
			for _, d := range f.decls {
				declaredIn[d] = i
			}
		}
		for i, f := range fs {
			for _, id := range f.idents {
				if j, ok := declaredIn[id]; ok && j != i {
					u.Links = append(u.Links, [2]int{i, j})
				}
			}
		}
		units = append(units, u)
	}
	out := rules.LowCohesion(units)
	for i := range out {
		out[i].Severity = string(cfg.Severity(rules.RuleLowCohesion))
	}
	return out, nil
}
