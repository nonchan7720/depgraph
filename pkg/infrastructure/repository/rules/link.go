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
//	(Dependency {use, doubt})-[:PROVIDES]->(Package)       (dependencies.go)
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
var keyProp = map[string]string{"Function": "key", "Package": "key", "File": "path", "Module": "key", "Type": "key", "Use": "key", "Part": "key", "Root": "key", "ExtImport": "key", "Dependency": "key"}

// ownedLabels are the labels whose nodes exist only in the linked graph:
// linking creates and removes them. Nodes of the other labels are only
// connected.
var ownedLabels = []string{"Function", "Module", "Type", "Use", "Part", "Root", "ExtImport", "Dependency"}

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
	{"PROVIDES", "Dependency", "Package"},
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
	declared := r.dependencies(cfg)
	settings := append([]string{ext.signature(), declared.signature(), fmt.Sprint(cfg.Enabled(rules.RuleDuplicateCode), clones,
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
	if err := declared.link(ctx, r, want); err != nil {
		return err
	}
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
const linkVersion = "10"

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
// graph. The unit of comparison is a node with its outgoing relationships:
// every such node is stored with a digest of them (linkSig), so what is
// stored is read as one small row per node (the digest and how many
// relationships leave it), not relationship by relationship. A node whose
// digest and count are as wanted is left alone; any other is written again
// with its relationships; one that is no longer wanted is removed. The
// count catches a relationship lost with its end node (a file removed and
// stored again).
func (r *Repository) storeLinked(ctx context.Context, want *linked) error {
	type bundle struct {
		props map[string]any // of a node linking owns, nil for the others
		owned bool
		edges []edgeID
	}
	bundles := map[nodeID]*bundle{}
	at := func(id nodeID) *bundle {
		if bundles[id] == nil {
			bundles[id] = &bundle{}
		}
		return bundles[id]
	}
	for id, props := range want.nodes {
		b := at(id)
		b.props, b.owned = props, true
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
	for id := range want.edges {
		if k := kindOf[id.typ]; k[1] != id.from.label || k[2] != id.to.label {
			return fmt.Errorf("relationship %s between %s and %s is not of the linked graph", id.typ, id.from.label, id.to.label)
		}
		b := at(id.from)
		b.edges = append(b.edges, id)
	}
	digest := func(b *bundle) string {
		lines := make([]string, 0, len(b.edges))
		for _, e := range b.edges {
			lines = append(lines, e.typ+"\x00"+e.to.label+"\x00"+e.to.key+"\x00"+signature(want.edges[e]))
		}
		sort.Strings(lines)
		h := sha256.New()
		_, _ = h.Write([]byte(signature(b.props)))
		for _, l := range lines {
			_, _ = h.Write([]byte("\x01" + l))
		}
		return hex.EncodeToString(h.Sum(nil)[:12])
	}

	// what is stored
	labels := append(slices.Clone(ownedLabels), "File", "Package")
	done := timing("read the linked graph")
	rows, err := r.q.Query(ctx, `
		MATCH (n:`+strings.Join(labels, "|")+` {project: $project})
		WHERE n.linkSig IS NOT NULL OR NOT (n:File OR n:Package)
		RETURN labels(n)[0] AS label, coalesce(n.path, n.key) AS key, n.linkSig AS sig,
		       COUNT { (n)-[:`+strings.Join(types, "|")+`]->() } AS out
	`, map[string]any{"project": r.project})
	done()
	if err != nil {
		return err
	}
	stale := map[string][]string{} // label -> keys of nodes that are not wanted any more
	current := map[nodeID]bool{}
	for _, row := range rows {
		id := nodeID{str(row["label"]), str(row["key"])}
		b := bundles[id]
		switch {
		case b == nil:
			stale[id.label] = append(stale[id.label], id.key)
		case str(row["sig"]) == digest(b) && toInt(row["out"]) == len(b.edges):
			current[id] = true
		}
	}
	write := func(cypher string, rows []map[string]any) error {
		for chunk := range slices.Chunk(rows, linkBatch) {
			if err := r.write(ctx, cypher, map[string]any{"project": r.project, "rows": chunk}); err != nil {
				return err
			}
		}
		return nil
	}
	// 1. the nodes that changed (before the relationships that end at them)
	changed := map[string][]map[string]any{} // label -> rows {key, props | sig}
	for id, b := range bundles {
		if current[id] {
			continue
		}
		row := map[string]any{"key": id.key, "sig": digest(b)}
		if b.owned {
			all := map[string]any{"project": r.project, keyProp[id.label]: id.key, "linkSig": row["sig"]}
			maps.Copy(all, b.props)
			row["props"] = all
		}
		changed[id.label] = append(changed[id.label], row)
	}
	for _, label := range ownedLabels {
		if err := write(`UNWIND $rows AS row MERGE (n:`+label+` {project: $project, `+keyProp[label]+`: row.key}) SET n = row.props`, changed[label]); err != nil {
			return err
		}
	}
	// 2. what is not wanted any more: the nodes linking owns, and the
	// relationships of the others
	for label, keys := range stale {
		rows := make([]map[string]any, len(keys))
		for i, k := range keys {
			rows[i] = map[string]any{"key": k}
		}
		cypher := `UNWIND $rows AS row MATCH (n:` + label + ` {project: $project, ` + keyProp[label] + `: row.key}) DETACH DELETE n`
		if !slices.Contains(ownedLabels, label) {
			cypher = `UNWIND $rows AS row MATCH (n:` + label + ` {project: $project, ` + keyProp[label] + `: row.key})
				REMOVE n.linkSig WITH n MATCH (n)-[e:` + strings.Join(types, "|") + `]->() DELETE e`
		}
		if err := write(cypher, rows); err != nil {
			return err
		}
	}
	// 3. the relationships of the nodes that changed: all of them again
	for _, label := range starts {
		if err := write(`UNWIND $rows AS row MATCH (n:`+label+` {project: $project, `+keyProp[label]+`: row.key})
			SET n.linkSig = row.sig WITH n MATCH (n)-[e:`+strings.Join(types, "|")+`]->() DELETE e`, changed[label]); err != nil {
			return err
		}
	}
	for _, k := range edgeKinds {
		typ, from, to := k[0], k[1], k[2]
		var rows []map[string]any
		for id, props := range want.edges {
			if id.typ != typ || current[id.from] {
				continue
			}
			if props == nil {
				props = map[string]any{}
			}
			rows = append(rows, map[string]any{"from": id.from.key, "to": id.to.key, "props": props})
		}
		if err := write(`UNWIND $rows AS row
			MATCH (a:`+from+` {project: $project, `+keyProp[from]+`: row.from})
			MATCH (b:`+to+` {project: $project, `+keyProp[to]+`: row.to})
			MERGE (a)-[e:`+typ+`]->(b) SET e = row.props`, rows); err != nil {
			return err
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
