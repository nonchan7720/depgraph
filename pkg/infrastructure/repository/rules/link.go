package rules

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// The linked graph.
//
// A file is stored with what it says itself (the names it calls, loads and
// uses, as properties of its File node), so that replacing one file never
// touches another. Before the rules run, those names are resolved across
// the project and stored as nodes and relationships (the linked graph), so
// that every rule is a Cypher query over the graph:
//
//	(Function {key, file, name, dir, lang, line, recv, ignore})
//	(Function)-[:CALLS {via, construct}]->(Function)
//	(Function)-[:FORWARDS]->(Function)
//	(Function)-[:CALLS_INTO]->(Package)
//	(File)-[:LOADS {eager}]->(File)
//	(Module {key})-[:MODULE_IMPORTS {from, to}]->(Module)
//	(Symbol)-[:SIMILAR {score}]->(Symbol)                  (clones.go)
//	(Type), (Type)-[:IMPLEMENTS]->(Type), (Use)-[:OF]->(Type)  (members.go)
//	(Part)-[:LINKS]->(Part)                                (cohesion.go)
//	(Root)-[:ENTRY]->(File), (File)-[:USES_FILE]->(File), ...  (unused.go)
//	(ExtImport {class, basis})                             (python.go)
//
// Linking resolves names; it decides nothing a rule reports. It is redone
// when the files or the configuration changed (LinkState.key) and writes
// only the difference to what is stored.

// Writer runs the statements that store the linked graph.
type Writer interface {
	Write(ctx context.Context, cypher string, params map[string]any) ([]map[string]any, error)
}

// nodeID is a node of the linked graph: its label and its key (the value
// of keyProp[label]).
type nodeID struct{ label, key string }

// edgeID is a relationship of the linked graph.
type edgeID struct {
	typ      string
	from, to nodeID
}

// keyProp is the property that identifies a node of a label in a project.
var keyProp = map[string]string{"Function": "key", "Package": "key", "File": "path", "Module": "key", "Type": "key", "Use": "key", "Part": "key", "Root": "key", "ExtImport": "key"}

// ownedLabels are the labels whose nodes exist only in the linked graph:
// linking creates and removes them. Nodes of the other labels are only
// connected.
var ownedLabels = []string{"Function", "Module", "Type", "Use", "Part", "Root", "ExtImport"}

// edgeKinds are the relationships of the linked graph: type, label of the
// start node, label of the end node. Linking owns every relationship of
// these types between such nodes; a type is of one kind.
var edgeKinds = [][3]string{
	{"CALLS", "Function", "Function"},
	{"FORWARDS", "Function", "Function"},
	{"CALLS_INTO", "Function", "Package"},
	{"LOADS", "File", "File"},
	{"MODULE_IMPORTS", "Module", "Module"},
	{"IMPLEMENTS", "Type", "Type"},
	{"OF", "Use", "Type"},
	{"LINKS", "Part", "Part"},
	{"ENTRY", "Root", "File"},
	{"USES_FILE", "File", "File"},
	{"GO_PKG", "File", "File"},
	{"USES_NAMES", "File", "File"},
	{"USES_GO", "File", "Package"},
	{"USES_ALL", "Root", "File"},
	{"USES_ALL_GO", "Root", "Package"},
	{"USES_GO_PKG", "Package", "Package"},
}

// linked is the linked graph of a project, as it should be stored.
type linked struct {
	nodes map[nodeID]map[string]any
	edges map[edgeID]map[string]any
}

func newLinked() *linked {
	return &linked{nodes: map[nodeID]map[string]any{}, edges: map[edgeID]map[string]any{}}
}

// node adds a node of an owned label (props without the project and key).
func (l *linked) node(id nodeID, props map[string]any) { l.nodes[id] = props }

// edge adds a relationship; one that is there already keeps its properties.
func (l *linked) edge(id edgeID, props map[string]any) {
	if _, ok := l.edges[id]; !ok {
		l.edges[id] = props
	}
}

// linkBatch is how many nodes or relationships one statement writes.
const linkBatch = 5000

// link brings the stored linked graph up to date with the files and cfg.
// It does nothing when neither changed since the last link.
func (r *Repository) link(ctx context.Context, cfg rules.Config, ignore []string) error {
	units, err := r.moduleUnits(ctx, cfg)
	if err != nil {
		return fmt.Errorf("link: %w", err)
	}
	clones := cfg.Clones.WithDefaults()
	ext, err := r.extImports(ctx, cfg, ignore)
	if err != nil {
		return fmt.Errorf("link: %w", err)
	}
	settings := append([]string{ext.signature(), fmt.Sprint(cfg.Enabled(rules.RuleDuplicateCode), clones,
		cfg.Enabled(rules.RuleUnreachableFile), cfg.Enabled(rules.RuleUnreachablePackage), cfg.UnusedEntries, cfg.UnusedIgnore)}, units...)
	key, err := r.linkKey(ctx, ignore, settings)
	if err != nil {
		return fmt.Errorf("link: %w", err)
	}
	rows, err := r.q.Query(ctx, `MATCH (s:LinkState {project: $project}) RETURN s.key AS key`, map[string]any{"project": r.project})
	if err != nil {
		return fmt.Errorf("link: %w", err)
	}
	if len(rows) == 1 && rows[0]["key"] == key {
		return nil
	}
	want := newLinked()
	types, err := r.typeIndex(ctx, ignore)
	if err != nil {
		return err
	}
	if err := r.linkCalls(ctx, ignore, types, want); err != nil {
		return err
	}
	if err := r.linkLoads(ctx, ignore, want); err != nil {
		return err
	}
	if err := r.linkModules(ctx, units, ignore, want); err != nil {
		return err
	}
	if err := r.linkMembers(ctx, ignore, types, want); err != nil {
		return err
	}
	if err := r.linkCohesion(ctx, ignore, want); err != nil {
		return err
	}
	ext.link(want)
	files, err := r.unusedFiles(ctx)
	if err != nil {
		return err
	}
	linkUnused(files, cfg, want)
	if err := r.storeLinked(ctx, want); err != nil {
		return fmt.Errorf("link: %w", err)
	}
	if cfg.Enabled(rules.RuleDuplicateCode) {
		if err := r.linkClones(ctx, clones, ignore, key); err != nil {
			return err
		}
	}
	if err := r.markUnreachable(ctx, cfg, ignore); err != nil {
		return err
	}
	if err := r.markCyclic(ctx, ignore); err != nil {
		return err
	}
	err = r.write(ctx, `MERGE (s:LinkState {project: $project}) SET s.key = $key`, map[string]any{"project": r.project, "key": key})
	if err != nil {
		return fmt.Errorf("link: %w", err)
	}
	return nil
}

// linkVersion changes when linking stores something else for the same
// files: an older linked graph is then rebuilt.
const linkVersion = "8"

// linkKey identifies what the linked graph is made from: the stored files
// (their number and the last update) and the settings linking reads.
func (r *Repository) linkKey(ctx context.Context, ignore, settings []string) (string, error) {
	rows, err := r.q.Query(ctx, `
		MATCH (f:File {project: $project})
		RETURN count(f) AS files, toString(max(f.updatedAt)) AS updated
	`, map[string]any{"project": r.project})
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s\x00%v\x00%v\x00%s", linkVersion, rows[0]["files"], rows[0]["updated"], strings.Join(ignore, "\x00"))
	_, _ = fmt.Fprintf(h, "\x01%s", strings.Join(settings, "\x00"))
	return hex.EncodeToString(h.Sum(nil)), nil
}

// storeLinked writes the difference between want and the stored linked
// graph: relationships and nodes that are no longer wanted are removed, new
// and changed ones written.
func (r *Repository) storeLinked(ctx context.Context, want *linked) error {
	p := map[string]any{"project": r.project}
	// what is stored: one query for the nodes, one for the relationships
	done := timing("read the linked nodes")
	rows, err := r.q.Query(ctx, `MATCH (n:`+strings.Join(ownedLabels, "|")+` {project: $project})
		RETURN labels(n)[0] AS label, n.key AS key, properties(n) AS props`, p)
	done()
	if err != nil {
		return err
	}
	haveNodes := map[string]map[string]string{} // label -> key -> signature
	for _, row := range rows {
		label := str(row["label"])
		props, _ := row["props"].(map[string]any)
		delete(props, "project")
		delete(props, keyProp[label])
		if haveNodes[label] == nil {
			haveNodes[label] = map[string]string{}
		}
		haveNodes[label][str(row["key"])] = signature(props)
	}
	var types, starts []string
	kindOf := map[string][3]string{}
	for _, k := range edgeKinds {
		types = append(types, k[0])
		kindOf[k[0]] = k
		if !slices.Contains(starts, k[1]) {
			starts = append(starts, k[1])
		}
	}
	done = timing("read the linked relationships")
	rows, err = r.q.Query(ctx, `MATCH (a:`+strings.Join(starts, "|")+` {project: $project})-[e:`+strings.Join(types, "|")+`]->(b)
		RETURN type(e) AS type, labels(a)[0] AS fromLabel, labels(b)[0] AS toLabel,
		       coalesce(a.path, a.key) AS from, coalesce(b.path, b.key) AS to, properties(e) AS props`, p)
	done()
	if err != nil {
		return err
	}
	haveEdges := map[string]map[[2]string]string{} // type -> (from, to) -> signature
	for _, row := range rows {
		typ := str(row["type"])
		if k := kindOf[typ]; k[1] != str(row["fromLabel"]) || k[2] != str(row["toLabel"]) {
			continue // not of the linked graph (CALLS between symbols is the language servers')
		}
		props, _ := row["props"].(map[string]any)
		if haveEdges[typ] == nil {
			haveEdges[typ] = map[[2]string]string{}
		}
		haveEdges[typ][[2]string{str(row["from"]), str(row["to"])}] = signature(props)
	}
	// nodes
	for _, label := range ownedLabels {
		have := haveNodes[label]
		var write []map[string]any
		for id, props := range want.nodes {
			if id.label != label {
				continue
			}
			if sig, ok := have[id.key]; !ok || sig != signature(props) {
				all := map[string]any{"project": r.project, keyProp[label]: id.key}
				maps.Copy(all, props)
				write = append(write, map[string]any{"key": id.key, "props": all})
			}
			delete(have, id.key)
		}
		gone := make([]string, 0, len(have))
		for k := range have {
			gone = append(gone, k)
		}
		for chunk := range slices.Chunk(gone, linkBatch) {
			if err := r.write(ctx, `UNWIND $keys AS k MATCH (n:`+label+` {project: $project, `+keyProp[label]+`: k}) DETACH DELETE n`,
				map[string]any{"project": r.project, "keys": chunk}); err != nil {
				return err
			}
		}
		for chunk := range slices.Chunk(write, linkBatch) {
			if err := r.write(ctx, `UNWIND $rows AS row MERGE (n:`+label+` {project: $project, `+keyProp[label]+`: row.key}) SET n = row.props`,
				map[string]any{"project": r.project, "rows": chunk}); err != nil {
				return err
			}
		}
	}
	// relationships
	for _, k := range edgeKinds {
		typ, from, to := k[0], k[1], k[2]
		have := haveEdges[typ]
		var write, gone []map[string]any
		for id, props := range want.edges {
			if id.typ != typ {
				continue
			}
			if id.from.label != from || id.to.label != to {
				return fmt.Errorf("relationship %s between %s and %s is not of the linked graph", typ, id.from.label, id.to.label)
			}
			pair := [2]string{id.from.key, id.to.key}
			if sig, ok := have[pair]; !ok || sig != signature(props) {
				if props == nil {
					props = map[string]any{}
				}
				write = append(write, map[string]any{"from": id.from.key, "to": id.to.key, "props": props})
			}
			delete(have, pair)
		}
		for pair := range have {
			gone = append(gone, map[string]any{"from": pair[0], "to": pair[1]})
		}
		ends := `MATCH (a:` + from + ` {project: $project, ` + keyProp[from] + `: row.from}) MATCH (b:` + to + ` {project: $project, ` + keyProp[to] + `: row.to})`
		for chunk := range slices.Chunk(gone, linkBatch) {
			if err := r.write(ctx, `UNWIND $rows AS row `+ends+` MATCH (a)-[e:`+typ+`]->(b) DELETE e`,
				map[string]any{"project": r.project, "rows": chunk}); err != nil {
				return err
			}
		}
		for chunk := range slices.Chunk(write, linkBatch) {
			if err := r.write(ctx, `UNWIND $rows AS row `+ends+` MERGE (a)-[e:`+typ+`]->(b) SET e = row.props`,
				map[string]any{"project": r.project, "rows": chunk}); err != nil {
				return err
			}
		}
	}
	return nil
}

// signature is props as one string, the same for the properties linking
// writes and for those read back from the store (integers come back as
// int64, lists as []any; an empty value is not stored).
func signature(props map[string]any) string {
	keys := make([]string, 0, len(props))
	for k, v := range props {
		if !emptyProp(v) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		switch v := props[k].(type) {
		case []string:
			b.WriteString(strings.Join(v, "\x1f"))
		case []any:
			for i, x := range v {
				if i > 0 {
					b.WriteByte('\x1f')
				}
				fmt.Fprint(&b, x)
			}
		default:
			fmt.Fprint(&b, v)
		}
		b.WriteByte('\x00')
	}
	return b.String()
}

// emptyProp reports whether v is a value the store does not keep.
func emptyProp(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case []string:
		return len(x) == 0
	case []any:
		return len(x) == 0
	}
	return false
}

// markCyclic marks the packages an import cycle can go through
// (Package.cyclic): the in-repo packages that are not ignored, without
// those that import no marked package or that no marked package imports,
// until none is left to remove. A cycle only goes through packages that
// both import and are imported inside it, so import-cycle searches the
// marked ones only: in a repository whose imports are mostly a tree (every
// Go repository) that is a small part of it.
func (r *Repository) markCyclic(ctx context.Context, ignore []string) error {
	p := map[string]any{"project": r.project, "ignore": ignore}
	if err := r.write(ctx, `
		MATCH (p:Package {project: $project})
		WITH p, (p.external = false AND none(rx IN $ignore WHERE p.key =~ rx)) AS inside
		WHERE inside OR p.cyclic IS NOT NULL
		SET p.cyclic = CASE WHEN inside THEN true END
	`, p); err != nil {
		return fmt.Errorf("link: %w", err)
	}
	for range maxTrims {
		done := timing("trim packages on no cycle")
		rows, err := r.q.Write(ctx, `
			MATCH (p:Package {project: $project})
			WHERE p.cyclic
			  AND (NOT EXISTS { MATCH (p)-[:IMPORTS]->(q:Package) WHERE q.cyclic AND q <> p }
			    OR NOT EXISTS { MATCH (q:Package)-[:IMPORTS]->(p) WHERE q.cyclic AND q <> p })
			REMOVE p.cyclic
			RETURN count(p) AS trimmed
		`, p)
		done()
		if err != nil {
			return fmt.Errorf("link: %w", err)
		}
		if len(rows) == 0 || rows[0]["trimmed"] == int64(0) {
			return nil
		}
	}
	// not trimmed to the end: the mark is still a superset of the packages
	// on cycles, which is all import-cycle needs
	return nil
}

// maxTrims bounds the rounds of markCyclic (a round removes at least the
// packages at the ends of the longest import chain).
const maxTrims = 200
