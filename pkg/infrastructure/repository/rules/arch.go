package rules

import (
	"context"
	"fmt"
	"sort"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// evaluateArch runs the rules about the structure inside the layers:
// concrete-dependency and large-package.
func (r *Repository) evaluateArch(ctx context.Context, cfg rules.Config, ignore []string) ([]rules.Finding, error) {
	var out []rules.Finding
	if cfg.Enabled(rules.RuleConcreteDependency) {
		fs, err := r.concreteDependencies(ctx, cfg, ignore)
		if err != nil {
			return nil, fmt.Errorf("concrete dependencies: %w", err)
		}
		out = append(out, fs...)
	}
	if cfg.Enabled(rules.RuleLargePackage) {
		fs, err := r.largePackages(ctx, cfg, ignore)
		if err != nil {
			return nil, fmt.Errorf("large packages: %w", err)
		}
		out = append(out, fs...)
	}
	return out, nil
}

// concreteDependencies joins the stored type dependencies (graph.TypeDep)
// with the types they name and reports those an abstract pair applies to.
// Test files are left out: a test may hold a concrete fake.
func (r *Repository) concreteDependencies(ctx context.Context, cfg rules.Config, ignore []string) ([]rules.Finding, error) {
	cfg, err := r.expandComponents(ctx, cfg)
	if err != nil {
		return nil, err
	}
	pairs, err := cfg.AbstractPairs()
	if err != nil || len(pairs) == 0 {
		return nil, err
	}
	rows, err := r.query(ctx, `
		MATCH (p:Package {project: $project})-[:CONTAINS]->(f:File)
		WHERE f.typeDeps IS NOT NULL AND none(rx IN $ignore WHERE p.key =~ rx)
		RETURN p.key AS pkg, f.path AS file, f.language AS lang, f.typeDeps AS deps
	`, ignore, nil)
	if err != nil {
		return nil, err
	}
	type target struct{ lang, target string }
	type held struct {
		target
		d rules.HeldDependency
	}
	var deps []held
	names := map[target]map[string]bool{}
	for _, row := range rows {
		file, _ := row["file"].(string)
		if rules.IsTestFile(file) {
			continue
		}
		pkg, _ := row["pkg"].(string)
		lang, _ := row["lang"].(string)
		for _, s := range toStrings(row["deps"]) {
			td, ok := graph.DecodeTypeDep(s)
			if !ok {
				continue
			}
			t := target{lang, td.Target}
			deps = append(deps, held{t, rules.HeldDependency{File: file, Package: pkg, Holder: td.Holder, Member: td.Member,
				Line: td.Line, Type: td.Type}})
			if names[t] == nil {
				names[t] = map[string]bool{}
			}
			names[t][td.Type] = true
		}
	}
	if len(deps) == 0 {
		return nil, nil
	}
	// a default import names what its file exports as default
	var defaults []string
	for t, ns := range names {
		if ns["default"] {
			defaults = append(defaults, t.target)
		}
	}
	if len(defaults) > 0 {
		rows, err := r.query(ctx, `
			UNWIND $files AS path
			MATCH (f:File {project: $project, path: path})
			RETURN f.path AS path, f.language AS lang, f.exports AS exports
		`, nil, map[string]any{"files": defaults})
		if err != nil {
			return nil, err
		}
		local := map[target]string{}
		for _, row := range rows {
			for _, e := range toStrings(row["exports"]) {
				if ex, ok := graph.DecodeExportName(e); ok && ex.Name == "default" {
					local[target{fmt.Sprint(row["lang"]), fmt.Sprint(row["path"])}] = ex.Local
				}
			}
		}
		for i := range deps {
			if deps[i].d.Type != "default" {
				continue
			}
			l := local[deps[i].target]
			deps[i].d.Type = l
			if l != "" {
				names[deps[i].target][l] = true
			}
		}
		for _, ns := range names {
			delete(ns, "default")
		}
	}
	params := make([]map[string]any, 0, len(names))
	for t, ns := range names {
		list := make([]string, 0, len(ns))
		for n := range ns {
			list = append(list, n)
		}
		sort.Strings(list)
		params = append(params, map[string]any{"lang": t.lang, "target": t.target, "names": list})
	}
	// a target is a Go package directory (or a Python package) or a file;
	// the methods of a Go type are the methods whose receiver it is, in any
	// file of its package. Two lookups by key, each an index seek.
	const match = `
		WHERE f.language = t.lang AND (s.name IN t.names OR s.receiver IN t.names)
		  AND none(rx IN $ignore WHERE p.key =~ rx)
		RETURN t.lang AS lang, t.target AS target, p.key AS pkg, f.path AS file, s.name AS name, s.kind AS kind,
		       s.abstract AS abstract, s.methods AS methods, s.receiver AS receiver`
	rows, err = r.query(ctx, `
		UNWIND $targets AS t
		MATCH (p:Package {project: $project, key: t.target})-[:CONTAINS]->(f:File)-[:DECLARES]->(s:Symbol)`+match+`
		UNION ALL
		UNWIND $targets AS t
		MATCH (f:File {project: $project, path: t.target})<-[:CONTAINS]-(p:Package)
		MATCH (f)-[:DECLARES]->(s:Symbol)`+match, ignore, map[string]any{"targets": params})
	if err != nil {
		return nil, err
	}
	type typeKey struct {
		target
		name string
	}
	type shape struct {
		pkg   string
		shape rules.TypeShape
		found bool
	}
	shapes := map[typeKey]*shape{}
	get := func(k typeKey) *shape {
		if shapes[k] == nil {
			shapes[k] = &shape{}
		}
		return shapes[k]
	}
	for _, row := range rows {
		if file, _ := row["file"].(string); rules.IsTestFile(file) {
			continue // a test's helpers and fakes are not the package's
		}
		t := target{fmt.Sprint(row["lang"]), fmt.Sprint(row["target"])}
		if recv, _ := row["receiver"].(string); recv != "" {
			if !dataMethods[fmt.Sprint(row["name"])] {
				get(typeKey{t, recv}).shape.Methods++
			}
			continue
		}
		kind, _ := row["kind"].(string)
		switch kind {
		case "struct", "class", "interface", "type":
		default:
			continue
		}
		sh := get(typeKey{t, fmt.Sprint(row["name"])})
		if sh.found {
			continue // a Go build variant declares it again
		}
		sh.found, sh.pkg = true, fmt.Sprint(row["pkg"])
		sh.shape.Kind = kind
		sh.shape.Abstract, _ = row["abstract"].(bool)
		sh.shape.Methods += toInt(row["methods"])
	}
	in := make([]rules.HeldDependency, 0, len(deps))
	for _, h := range deps {
		if h.d.Type == "" {
			continue // a default import of a file without a default class
		}
		sh := shapes[typeKey{h.target, h.d.Type}]
		if sh == nil || !sh.found {
			continue // not declared there (a re-export, an ignored package)
		}
		h.d.TargetPackage, h.d.Shape = sh.pkg, sh.shape
		in = append(in, h.d)
	}
	out := rules.ConcreteDependencies(in, pairs)
	for i := range out {
		out[i].Severity = string(cfg.Severity(rules.RuleConcreteDependency))
	}
	return out, nil
}

// dataMethods are the methods data types implement for the standard
// library (fmt.Stringer, error, the encoders, database/sql, sort, gorm's
// table name): a struct with only these is a value, not a service.
var dataMethods = map[string]bool{
	"String": true, "GoString": true, "Format": true, "Error": true, "Unwrap": true, "Is": true,
	"MarshalJSON": true, "UnmarshalJSON": true, "MarshalText": true, "UnmarshalText": true,
	"MarshalYAML": true, "UnmarshalYAML": true, "MarshalBinary": true, "UnmarshalBinary": true,
	"MarshalXML": true, "UnmarshalXML": true, "Scan": true, "Value": true, "TableName": true,
	"Len": true, "Less": true, "Swap": true, "Equal": true, "Clone": true, "Validate": true,
}

// largePackages sums the lines of every package's source files (tests and
// generated files left out) and reports those over their limit.
func (r *Repository) largePackages(ctx context.Context, cfg rules.Config, ignore []string) ([]rules.Finding, error) {
	rows, err := r.query(ctx, `
		MATCH (p:Package {project: $project})-[:CONTAINS]->(f:File)
		WHERE p.external = false AND none(rx IN $ignore WHERE p.key =~ rx) AND coalesce(f.generated, false) = false
		RETURN p.key AS pkg, f.path AS file, f.language AS lang, f.lines AS lines
	`, ignore, nil)
	if err != nil {
		return nil, err
	}
	sizes := map[string]*rules.PackageSize{}
	for _, row := range rows {
		file, _ := row["file"].(string)
		if rules.IsTestFile(file) {
			continue
		}
		pkg, _ := row["pkg"].(string)
		s := sizes[pkg]
		if s == nil {
			s = &rules.PackageSize{Key: pkg}
			sizes[pkg] = s
		}
		s.Lines += toInt(row["lines"])
		s.Files++
		s.Go = s.Go || row["lang"] == string(graph.Go)
	}
	list := make([]rules.PackageSize, 0, len(sizes))
	for _, s := range sizes {
		list = append(list, *s)
	}
	out, err := rules.LargePackages(list, cfg)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Severity = string(cfg.Severity(rules.RuleLargePackage))
	}
	return out, nil
}
