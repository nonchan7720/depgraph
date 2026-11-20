package rules

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// low-cohesion: a package (Go) or module (TypeScript / Python file) made of
// parts that nothing inside connects and that different code uses. Linking
// stores the parts of every unit, the references between them and who
// outside the unit uses their names:
//
//	(Part {key, unit, what, id, data, used})-[:LINKS]->(Part)
//
// used: for every name of the part that code outside the unit uses, the
// name and its users (partUsed).

// Separators of an entry of Part.used: the name, then its users.
const (
	partNameSep = "\x1f"
	partUserSep = "\x1e"
)

// linkUnits adds the parts of units and the links between them.
func linkUnits(units []rules.CohesionUnit, want *linked) {
	for _, u := range units {
		ids := make([]nodeID, len(u.Parts))
		for i, p := range u.Parts {
			used := []string{}
			for _, n := range p.Names {
				if users := u.Users[n]; len(users) > 0 {
					used = append(used, n+partNameSep+strings.Join(users, partUserSep))
				}
			}
			ids[i] = nodeID{"Part", fmt.Sprintf("%s|%d|%s", u.Key, i, p.ID)}
			want.node(ids[i], map[string]any{"unit": u.Key, "what": u.What, "id": p.ID, "data": p.Data, "used": used})
		}
		for _, l := range u.Links {
			if l[0] != l[1] && l[0] >= 0 && l[1] >= 0 && l[0] < len(ids) && l[1] < len(ids) {
				want.edge(edgeID{"LINKS", ids[l[0]], ids[l[1]]}, nil)
			}
		}
	}
}

// lowCohesion reports the units whose parts fall into two or more groups
// that nothing inside connects (LINKS), each with names used from outside,
// where no user uses two of the groups: more than one responsibility in one
// place. A unit with fewer than 3 names used from outside is too small to
// split, and one whose groups are all data (entities, DTOs used one per
// consumer) is normal architecture.
func (r *Repository) lowCohesion(ctx context.Context, cfg rules.Config) ([]rules.Finding, error) {
	rows, err := r.query(ctx, `
		MATCH (p:Part {project: $project})
		MATCH (p)-[:LINKS*0..]-(q:Part)
		WITH DISTINCT p, q
		WITH p, min(q.key) AS grp
		WITH p.unit AS unit, p.what AS what, grp, collect(p) AS parts
		WITH unit, what, all(x IN parts WHERE x.data) AS data,
		     COLLECT { UNWIND parts AS x RETURN x.id ORDER BY x.id } AS ids,
		     reduce(all = [], x IN parts | all + coalesce(x.used, [])) AS used
		WHERE size(used) > 0
		WITH unit, what, data, ids,
		     COLLECT { UNWIND used AS x WITH split(x, $nameSep)[0] AS name RETURN name ORDER BY name } AS names,
		     COLLECT { UNWIND used AS x UNWIND split(split(x, $nameSep)[1], $userSep) AS user
		               WITH DISTINCT user RETURN user ORDER BY user } AS users
		ORDER BY ids[0]
		WITH unit, what, collect({parts: ids, names: names, users: users, data: data}) AS groups
		WHERE size(groups) >= 2 AND any(g IN groups WHERE NOT g.data)
		  AND reduce(n = 0, g IN groups | n + size(g.names)) >= $minNames
		  AND reduce(n = 0, g IN groups | n + size(g.users)) =
		      COUNT { UNWIND groups AS g UNWIND g.users AS user RETURN DISTINCT user }
		RETURN unit, what, groups ORDER BY unit
	`, nil, map[string]any{"nameSep": partNameSep, "userSep": partUserSep, "minNames": rules.MinCohesionNames})
	if err != nil {
		return nil, fmt.Errorf("low cohesion: %w", err)
	}
	out := make([]rules.Finding, 0, len(rows))
	for _, row := range rows {
		list, _ := row["groups"].([]any)
		groups := make([]rules.CohesionGroup, 0, len(list))
		for _, x := range list {
			g, _ := x.(map[string]any)
			groups = append(groups, rules.CohesionGroup{Parts: toStrings(g["parts"]), Names: toStrings(g["names"]), Users: toStrings(g["users"])})
		}
		f := rules.LowCohesion(str(row["unit"]), str(row["what"]), groups)
		f.Severity = string(cfg.Severity(rules.RuleLowCohesion))
		out = append(out, f)
	}
	return out, nil
}

// linkCohesion builds the units of low-cohesion from the stored usage: Go
// packages (files linked when one uses a name another declares) and
// TypeScript / Python modules (declarations linked by their references),
// with the code outside each unit that uses its names. Test and generated
// files are neither parts nor users. A unit used whole (a Go dot import, a
// namespace used as a value, export *) or that is an entry point (it may
// have users outside the repository) is left out.
func (r *Repository) linkCohesion(ctx context.Context, ignore []string, want *linked) error {
	rows, err := r.query(ctx, `
		MATCH (p:Package {project: $project})-[:CONTAINS]->(f:File)
		WHERE p.external = false AND none(rx IN $ignore WHERE p.key =~ rx) AND coalesce(f.generated, false) = false
		  AND f.language IN ['go', 'typescript', 'javascript', 'python']
		OPTIONAL MATCH (f)-[:DECLARES]->(s:Symbol)
		WITH p, f, collect({name: s.name, kind: s.kind, receiver: s.receiver, methods: s.methods, data: s.data}) AS syms
		RETURN p.key AS pkg, f.path AS file, f.language AS lang, f.entry AS entry, f.decls AS decls, f.idents AS idents,
		       f.declRefs AS declRefs, f.uses AS uses, f.exports AS exports, syms
	`, ignore, nil)
	if err != nil {
		return fmt.Errorf("low cohesion: %w", err)
	}
	type file struct {
		pkg, path, lang string
		entry           bool
		decls, idents   []string
		declRefs        []graph.DeclRef
		exports         map[string]string // exported name -> local
		behaviour       bool              // Go: a function or method that is not a constructor or data method
		behaves         map[string]bool   // TS / Python: declarations that are functions or classes with methods
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
		f.behaves = map[string]bool{}
		syms, _ := row["syms"].([]any)
		for _, x := range syms {
			s, _ := x.(map[string]any)
			name, _ := s["name"].(string)
			kind, _ := s["kind"].(string)
			data, _ := s["data"].(bool)
			switch {
			case name == "":
			case kind == "method":
				f.behaviour = f.behaviour || !rules.IsDataMethod(name)
			case kind == "function":
				f.behaviour = f.behaviour || !strings.HasPrefix(name, "New")
				f.behaves[name] = true
			case kind == "class":
				f.behaves[name] = !data && len(toStrings(s["methods"])) > 0
			}
		}
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
			u.Parts = append(u.Parts, rules.CohesionPart{ID: d.Name, Names: []string{d.Name}, Data: !f.behaves[d.Name]})
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
			u.Parts = append(u.Parts, rules.CohesionPart{ID: f.path[strings.LastIndexByte(f.path, '/')+1:], Names: f.decls, Data: !f.behaviour})
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
	linkUnits(units, want)
	return nil
}
