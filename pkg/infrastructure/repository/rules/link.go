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
//	(Module {key})-[:IMPORTS {from, to}]->(Module)
//	(Symbol)-[:SIMILAR {score}]->(Symbol)                  (clones.go)
//	(Type), (Type)-[:IMPLEMENTS]->(Type), (Use)-[:OF]->(Type)  (members.go)
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
var keyProp = map[string]string{"Function": "key", "Package": "key", "File": "path", "Module": "key", "Type": "key", "Use": "key"}

// ownedLabels are the labels whose nodes exist only in the linked graph:
// linking creates and removes them. Nodes of the other labels are only
// connected.
var ownedLabels = []string{"Function", "Module", "Type", "Use"}

// edgeKinds are the relationships of the linked graph: type, label of the
// start node, label of the end node. Linking owns every relationship of
// these types between such nodes.
var edgeKinds = [][3]string{
	{"CALLS", "Function", "Function"},
	{"FORWARDS", "Function", "Function"},
	{"CALLS_INTO", "Function", "Package"},
	{"LOADS", "File", "File"},
	{"IMPORTS", "Module", "Module"},
	{"IMPLEMENTS", "Type", "Type"},
	{"OF", "Use", "Type"},
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
	settings := append([]string{fmt.Sprint(cfg.Enabled(rules.RuleDuplicateCode), clones)}, units...)
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
	if err := r.linkCalls(ctx, ignore, want); err != nil {
		return err
	}
	if err := r.linkLoads(ctx, ignore, want); err != nil {
		return err
	}
	if err := r.linkModules(ctx, units, ignore, want); err != nil {
		return err
	}
	if err := r.linkMembers(ctx, ignore, want); err != nil {
		return err
	}
	if err := r.storeLinked(ctx, want); err != nil {
		return fmt.Errorf("link: %w", err)
	}
	if cfg.Enabled(rules.RuleDuplicateCode) {
		if err := r.linkClones(ctx, clones, ignore, key); err != nil {
			return err
		}
	}
	err = r.write(ctx, `MERGE (s:LinkState {project: $project}) SET s.key = $key`, map[string]any{"project": r.project, "key": key})
	if err != nil {
		return fmt.Errorf("link: %w", err)
	}
	return nil
}

// linkVersion changes when linking stores something else for the same
// files: an older linked graph is then rebuilt.
const linkVersion = "3"

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
	// nodes
	for _, label := range ownedLabels {
		rows, err := r.q.Query(ctx, `MATCH (n:`+label+` {project: $project}) RETURN n.`+keyProp[label]+` AS key, properties(n) AS props`, p)
		if err != nil {
			return err
		}
		have := map[string]string{}
		for _, row := range rows {
			props, _ := row["props"].(map[string]any)
			delete(props, "project")
			delete(props, keyProp[label])
			have[fmt.Sprint(row["key"])] = signature(props)
		}
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
		match := `MATCH (a:` + from + ` {project: $project})-[e:` + typ + `]->(b:` + to + ` {project: $project})`
		rows, err := r.q.Query(ctx, match+` RETURN a.`+keyProp[from]+` AS from, b.`+keyProp[to]+` AS to, properties(e) AS props`, p)
		if err != nil {
			return err
		}
		have := map[[2]string]string{}
		for _, row := range rows {
			props, _ := row["props"].(map[string]any)
			have[[2]string{fmt.Sprint(row["from"]), fmt.Sprint(row["to"])}] = signature(props)
		}
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
