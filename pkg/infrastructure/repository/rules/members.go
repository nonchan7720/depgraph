package rules

import (
	"context"
	"fmt"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// evaluateMembers runs the rules over the members model (graph.TypeUse,
// graph.Construct): concrete-dependency and type-leak. Test files are left
// out on both sides: a test may hold a fake, and a test's method does not
// make a type implement anything.
func (r *Repository) evaluateMembers(ctx context.Context, cfg rules.Config, ignore []string) ([]rules.Finding, error) {
	concrete, leak := cfg.Enabled(rules.RuleConcreteDependency), cfg.Enabled(rules.RuleTypeLeak)
	if !concrete && !leak {
		return nil, nil
	}
	cfg, err := r.expandComponents(ctx, cfg)
	if err != nil {
		return nil, err
	}
	pairs, err := cfg.AbstractPairs()
	if err != nil || len(pairs) == 0 {
		return nil, err
	}
	uses, err := r.memberUses(ctx, ignore)
	if err != nil || len(uses) == 0 {
		return nil, err
	}
	types, err := r.typeIndex(ctx, ignore)
	if err != nil {
		return nil, err
	}
	var out []rules.Finding
	if concrete {
		for _, f := range rules.ConcreteDependencies(uses, types, pairs) {
			f.Severity = string(cfg.Severity(rules.RuleConcreteDependency))
			out = append(out, f)
		}
	}
	if leak {
		for _, f := range rules.TypeLeaks(uses, types, pairs) {
			f.Severity = string(cfg.Severity(rules.RuleTypeLeak))
			out = append(out, f)
		}
	}
	return out, nil
}

// memberUses are the stored members of the project's non-test files.
func (r *Repository) memberUses(ctx context.Context, ignore []string) ([]rules.MemberUse, error) {
	rows, err := r.query(ctx, `
		MATCH (p:Package {project: $project})-[:CONTAINS]->(f:File)
		WHERE (f.typeUses IS NOT NULL OR f.constructs IS NOT NULL) AND none(rx IN $ignore WHERE p.key =~ rx)
		RETURN p.key AS pkg, f.path AS file, f.typeUses AS uses, f.constructs AS constructs
	`, ignore, nil)
	if err != nil {
		return nil, fmt.Errorf("members: %w", err)
	}
	var out []rules.MemberUse
	for _, row := range rows {
		file, _ := row["file"].(string)
		if rules.IsTestFile(file) {
			continue
		}
		pkg, _ := row["pkg"].(string)
		for _, s := range toStrings(row["uses"]) {
			if u, ok := graph.DecodeTypeUse(s); ok {
				out = append(out, rules.MemberUse{File: file, Package: pkg, TypeUse: u})
			}
		}
		for _, s := range toStrings(row["constructs"]) {
			if c, ok := graph.DecodeConstruct(s); ok {
				out = append(out, rules.MemberUse{File: file, Package: pkg, Construct: true,
					Owner: c.Owner, Line: c.Line, Target: c.Target, Type: c.Type})
			}
		}
	}
	return out, nil
}

// typeIndex is rules.TypeIndex over the stored symbols.
type typeIndex struct {
	byRef      map[graph.TypeRef]*rules.TypeInfo
	interfaces map[string][]*rules.TypeInfo
	defaults   map[string]string // TypeScript file -> what it exports as default
}

func (t *typeIndex) Type(ref graph.TypeRef) (rules.TypeInfo, bool) {
	if ref.Type == "default" {
		if local, ok := t.defaults[ref.Target]; ok {
			ref.Type = local
		}
	}
	if i, ok := t.byRef[ref]; ok {
		return *i, true
	}
	return rules.TypeInfo{}, false
}

func (t *typeIndex) Interfaces(pkg string) []rules.TypeInfo {
	out := make([]rules.TypeInfo, 0, len(t.interfaces[pkg]))
	for _, i := range t.interfaces[pkg] {
		out = append(out, *i)
	}
	return out
}

// typeIndex reads the type declarations of the project's non-test files and
// the methods of Go types. A type is found by its file (TypeScript, Python)
// and by its package (Go; a Python package directory).
func (r *Repository) typeIndex(ctx context.Context, ignore []string) (*typeIndex, error) {
	rows, err := r.query(ctx, `
		MATCH (p:Package {project: $project})-[:CONTAINS]->(f:File)-[:DECLARES]->(s:Symbol)
		WHERE p.external = false AND none(rx IN $ignore WHERE p.key =~ rx)
		  AND (s.kind IN ['struct', 'class', 'interface', 'type'] OR s.receiver IS NOT NULL)
		RETURN p.key AS pkg, f.path AS file, f.language AS lang, s.name AS name, s.kind AS kind, s.receiver AS receiver,
		       s.methods AS methods, s.abstract AS abstract, s.data AS data, s.implements AS implements
	`, ignore, nil)
	if err != nil {
		return nil, fmt.Errorf("types: %w", err)
	}
	idx := &typeIndex{byRef: map[graph.TypeRef]*rules.TypeInfo{}, interfaces: map[string][]*rules.TypeInfo{}, defaults: map[string]string{}}
	type goMethod struct{ pkg, recv, name string }
	var methods []goMethod
	for _, row := range rows {
		file, _ := row["file"].(string)
		if rules.IsTestFile(file) {
			continue
		}
		pkg, _ := row["pkg"].(string)
		name, _ := row["name"].(string)
		if recv, _ := row["receiver"].(string); recv != "" {
			methods = append(methods, goMethod{pkg, recv, name})
			continue
		}
		info := &rules.TypeInfo{Package: pkg, Kind: fmt.Sprint(row["kind"]), Methods: toStrings(row["methods"])}
		info.Abstract, _ = row["abstract"].(bool)
		info.Data, _ = row["data"].(bool)
		for _, s := range toStrings(row["implements"]) {
			if ref, ok := graph.ParseTypeRef(s); ok {
				info.Implements = append(info.Implements, ref)
			}
		}
		target := file
		if row["lang"] == string(graph.Go) {
			target = pkg
		}
		info.Ref = graph.TypeRef{Target: target, Type: name}
		if _, dup := idx.byRef[info.Ref]; dup {
			continue // a Go build variant declares it again
		}
		idx.byRef[info.Ref] = info
		if row["lang"] == string(graph.Python) { // `from pkg import X` names the package
			if ref := (graph.TypeRef{Target: pkg, Type: name}); idx.byRef[ref] == nil {
				idx.byRef[ref] = info
			}
		}
		if info.Kind == "interface" || info.Abstract {
			idx.interfaces[pkg] = append(idx.interfaces[pkg], info)
		}
	}
	for _, m := range methods {
		if t := idx.byRef[graph.TypeRef{Target: m.pkg, Type: m.recv}]; t != nil {
			t.Methods = append(t.Methods, m.name)
		}
	}
	// TypeScript default exports, for "default" type references
	rows, err = r.query(ctx, `
		MATCH (:Package {project: $project})-[:CONTAINS]->(f:File)
		WHERE f.exports IS NOT NULL AND f.language IN ['typescript', 'javascript']
		RETURN f.path AS file, f.exports AS exports
	`, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("types: %w", err)
	}
	for _, row := range rows {
		for _, e := range toStrings(row["exports"]) {
			if ex, ok := graph.DecodeExportName(e); ok && ex.Name == "default" {
				idx.defaults[fmt.Sprint(row["file"])] = ex.Local
			}
		}
	}
	// a class that implements a default export
	for _, t := range idx.byRef {
		for i, ref := range t.Implements {
			if local, ok := idx.defaults[ref.Target]; ok && ref.Type == "default" {
				t.Implements[i].Type = local
			}
		}
	}
	return idx, nil
}
