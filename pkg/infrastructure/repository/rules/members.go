package rules

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// The rules about the code inside the layers (concrete-dependency,
// type-leak). Linking stores the types of the project and where its members
// name them:
//
//	(Type {key, pkg, name, kind, abstract, data, embeds, methods})
//	(Type)-[:IMPLEMENTS {exact}]->(Type)      what a class says it implements / extends
//	(Use {key, file, pkg, owner, member, role, line, exported, collection, construct, calls})
//	(Use)-[:OF]->(Type)                       a field, parameter, result or construction
//
// Test files are left out on both sides: a test may hold a fake, and a
// test's method does not make a type implement anything.

// linkMembers adds the types and their uses to the linked graph.
func (r *Repository) linkMembers(ctx context.Context, ignore []string, want *linked) error {
	uses, err := r.memberUses(ctx, ignore)
	if err != nil {
		return err
	}
	types, err := r.typeIndex(ctx, ignore)
	if err != nil {
		return err
	}
	id := func(t *rules.TypeInfo) nodeID { return nodeID{"Type", t.Ref.String()} }
	for _, t := range types.byRef {
		methods := t.Methods
		if methods == nil {
			methods = []string{}
		}
		want.node(id(t), map[string]any{"pkg": t.Package, "name": t.Ref.Type, "kind": t.Kind,
			"abstract": t.Abstract, "data": t.Data, "embeds": t.Embeds, "methods": methods})
	}
	for _, t := range types.byRef {
		for _, ref := range t.Implements {
			// exact: the reference is the declaration itself, which is what
			// makes t an implementation of it; otherwise it only names the
			// package the abstraction is in (a Python package re-exporting it)
			if i := types.byRef[ref]; i != nil && i.Ref == ref {
				want.edges[edgeID{"IMPLEMENTS", id(t), id(i)}] = map[string]any{"exact": true}
			} else if i != nil {
				want.edge(edgeID{"IMPLEMENTS", id(t), id(i)}, map[string]any{"exact": false})
			}
		}
	}
	for _, u := range uses {
		ref := u.TypeRef
		if ref.Type == "default" {
			if local, ok := types.defaults[ref.Target]; ok {
				ref.Type = local
			}
		}
		t := types.byRef[ref]
		if t == nil {
			continue
		}
		calls := u.Calls
		if calls == nil {
			calls = []string{}
		}
		use := nodeID{"Use", strings.Join([]string{u.File, fmt.Sprint(u.Line), u.Owner, u.Member, u.Role, fmt.Sprint(u.Construct), t.Ref.String()}, "|")}
		want.node(use, map[string]any{"file": u.File, "pkg": u.Package, "owner": u.Owner, "member": u.Member, "role": u.Role,
			"line": u.Line, "exported": u.Exported, "collection": u.Collection, "construct": u.Construct, "calls": calls})
		want.edge(edgeID{"OF", use, id(t)}, nil)
	}
	return nil
}

// inLayer is the Cypher condition "the in-repo package key (an expression)
// is in the layer set s": rules.LayerSet.Contains.
func inLayer(key, s string) string {
	return fmt.Sprintf(`(NOT any(rx IN %[2]s.notRx WHERE %[1]s =~ rx) AND any(rx IN %[2]s.rx WHERE %[1]s =~ rx))`, key, s)
}

// isConcrete is the Cypher condition "type node t is an implementation": a
// struct or class with behaviour (a method that is not one values implement
// for the standard library: $dataMethods) that is neither abstract nor data.
const isConcrete = `(t.kind IN ['struct', 'class'] AND NOT t.abstract AND NOT t.data AND any(m IN t.methods WHERE NOT m IN $dataMethods))`

// usesByPair starts the queries of both rules: the uses the condition
// where accepts, grouped by the package that has the member (from), the
// package of the type (to) and the type, with the first abstract pair that
// applies to from -> to (rules.LayerPair.Reports).
func usesByPair(where string) string {
	return `
		MATCH (u:Use {project: $project})-[:OF]->(t:Type)
		WHERE u.pkg <> t.pkg AND ` + where + `
		WITH u.pkg AS from, t.pkg AS to, t.name AS type, u, t ORDER BY u.file, u.line
		WITH from, to, type, head(collect(t)) AS t, collect(u) AS uses
		UNWIND range(0, size($pairs) - 1) AS i
		WITH from, to, type, t, uses, i, $pairs[i] AS pr
		WHERE NOT any(rx IN pr.exceptRx WHERE from =~ rx OR to =~ rx)
		  AND ` + inLayer("from", "pr.fromSet") + ` AND ` + inLayer("to", "pr.toSet") + `
		  AND NOT ` + inLayer("from", "pr.toSet") + ` AND NOT ` + inLayer("to", "pr.fromSet") + `
		WITH from, to, type, t, uses, min(i) AS i
		WITH from, to, type, t, uses, $pairs[i] AS pr
	`
}

// useMembers ends the queries: where each use is, as the findings show it.
const useMembers = `
	[u IN uses | u.file + ':' + toString(u.line) + ' ' + CASE
		WHEN u.construct THEN 'builds it in ' + u.owner
		WHEN u.role = 'result' THEN u.owner + ' result ' + u.member
		ELSE u.owner + '.' + u.member END] AS members,
	pr.from AS fromLayer, pr.to AS toLayer, pr.component AS component
`

// evaluateMembers runs concrete-dependency and type-leak.
func (r *Repository) evaluateMembers(ctx context.Context, cfg rules.Config) ([]rules.Finding, error) {
	concrete, leak := cfg.Enabled(rules.RuleConcreteDependency), cfg.Enabled(rules.RuleTypeLeak)
	if !concrete && !leak {
		return nil, nil
	}
	cfg, err := r.expandComponents(ctx, cfg)
	if err != nil {
		return nil, err
	}
	lp, err := cfg.AbstractPairs()
	if err != nil || len(lp) == 0 {
		return nil, err
	}
	pairs := make([]map[string]any, len(lp))
	for i, p := range lp {
		pairs[i] = p.Params()
	}
	params := map[string]any{"pairs": pairs, "dataMethods": rules.DataMethods()}
	var out []rules.Finding
	if concrete {
		// A struct / class holds (field, constructor parameter) or builds an
		// implementation of a layer it may use only through interfaces,
		// although the implementation satisfies an abstraction the holder
		// could hold instead: one that has every method the holder calls on
		// it (the smallest such, one of the holder's package first), declared
		// in the holder's package, the type's package or the pair's from
		// layer; without recorded calls, the fullest one of the holder's
		// package. Without such an abstraction nothing is reported: that the
		// code should have one is a design call, not a fact.
		rows, err := r.query(ctx, usesByPair(`(u.construct OR (NOT u.collection AND u.role IN ['field', 'ctor'])) AND `+isConcrete)+`
			WITH from, to, type, t, uses, pr, reduce(all = [], u IN uses | all + [c IN u.calls WHERE NOT c IN all]) AS calls
			CALL {
				WITH from, t, pr, calls
				MATCH (i:Type {project: $project})
				WHERE (i.kind = 'interface' OR i.abstract) AND i <> t AND NOT i.embeds
				  AND (i.pkg = from OR (size(calls) > 0 AND (i.pkg = t.pkg OR EXISTS {
				        MATCH (t)-[:IMPLEMENTS]->(j:Type) WHERE j.pkg = i.pkg AND `+inLayer("j.pkg", "pr.fromSet")+` })))
				  AND (EXISTS { (t)-[:IMPLEMENTS {exact: true}]->(i) }
				       OR (t.kind = 'struct' AND i.kind = 'interface' AND size(i.methods) > 0 AND all(m IN i.methods WHERE m IN t.methods)))
				  AND all(c IN calls WHERE c IN i.methods)
				WITH i, CASE WHEN size(calls) = 0 THEN -size(i.methods) ELSE size(i.methods) END AS fit,
				     CASE WHEN i.pkg = from THEN 0 ELSE 1 END AS foreign, i.pkg + '.' + i.name AS name
				ORDER BY fit, foreign, name
				RETURN head(collect(name)) AS port
			}
			WITH from, to, type, uses, pr, port
			WHERE port IS NOT NULL
			RETURN from, to, type, port, `+useMembers, nil, params)
		if err != nil {
			return nil, fmt.Errorf("concrete-dependency: %w", err)
		}
		for _, row := range rows {
			f := memberFinding(rules.RuleConcreteDependency, row)
			f.Severity = string(cfg.Severity(rules.RuleConcreteDependency))
			out = append(out, f)
		}
	}
	if leak {
		// The API of a package (parameters and results of exported functions
		// and methods, exported fields holding data) names a struct or class
		// of a layer it may use only through interfaces. A constructor's
		// parameters and a held implementation are concrete-dependency's.
		rows, err := r.query(ctx, usesByPair(`NOT u.construct AND u.exported AND u.role <> 'ctor'
			AND t.kind IN ['struct', 'class'] AND NOT t.abstract AND NOT (u.role = 'field' AND `+isConcrete+`)`)+`
			RETURN from, to, type, null AS port, `+useMembers, nil, params)
		if err != nil {
			return nil, fmt.Errorf("type-leak: %w", err)
		}
		for _, row := range rows {
			f := memberFinding(rules.RuleTypeLeak, row)
			f.Severity = string(cfg.Severity(rules.RuleTypeLeak))
			out = append(out, f)
		}
	}
	return out, nil
}

// memberFinding is the finding of a row of the queries above.
func memberFinding(rule string, row map[string]any) rules.Finding {
	return rules.MemberFinding(rule, str(row["from"]), str(row["to"]), str(row["type"]), slices.Compact(toStrings(row["members"])),
		rules.LayerPair{From: str(row["fromLayer"]), To: str(row["toLayer"]), Component: str(row["component"])}, str(row["port"]))
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
		       s.methods AS methods, s.abstract AS abstract, s.data AS data, s.implements AS implements, s.embeds AS embeds
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
		info.Embeds, _ = row["embeds"].(bool)
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
