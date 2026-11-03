package ui

import (
	"path"
	"sort"
	"strings"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// ViewOptions controls how the package graph is folded for display.
type ViewOptions struct {
	// Depth is the number of path segments shown at the top level
	// (packages sharing those segments are folded into one group). <= 0 means AutoDepth.
	Depth int
	// Expanded lists group prefixes (e.g. "pkg/infrastructure") that are opened
	// into a box showing their next level.
	Expanded []string
	// External shows packages outside the repository.
	External bool
	// Query keeps only packages whose key contains it (case-insensitive).
	Query string
	// Focus drills into one directory prefix: its contents are shown one level
	// deeper, and only the outside packages connected to it remain, as
	// context nodes folded at Depth.
	Focus string
}

// ViewNode is one visible node: a package, a collapsed group, or an expanded group box.
type ViewNode struct {
	ID       string `json:"id"`     // package key, or "g:<prefix>" for groups
	Key      string `json:"key"`    // package key or group prefix
	Label    string `json:"label"`  // relative to Parent
	Parent   string `json:"parent"` // id of the enclosing expanded group, "" at top level
	Group    bool   `json:"group"`
	Expanded bool   `json:"expanded"`
	External bool   `json:"external"`
	Members  int    `json:"members"` // packages inside (1 for a package)
	Files    int64  `json:"files"`
	Symbols  int64  `json:"symbols"`
	Context  bool   `json:"context"` // outside the focus, shown only for its connections
	Bad      bool   `json:"bad"`     // contains a package in a cycle / layer violation
	God      bool   `json:"god"`     // contains a god package
}

// ViewEdge aggregates package imports between two visible nodes.
type ViewEdge struct {
	ID     string `json:"id"`
	From   string `json:"from"`
	To     string `json:"to"`
	Weight int    `json:"weight"` // number of package-level imports folded into this edge
	Bad    bool   `json:"bad"`
}

// View is the folded graph sent to the browser.
type View struct {
	Depth  int               `json:"depth"`
	Focus  string            `json:"focus"`
	Nodes  []ViewNode        `json:"nodes"`
	Edges  []ViewEdge        `json:"edges"`
	NodeOf map[string]string `json:"nodeOf"` // package key -> visible node id
}

const groupPrefix = "g:"

func segments(key string) []string { return strings.Split(key, "/") }

// bucket returns the prefix of key at the given depth (the key itself when shorter).
func bucket(key string, depth int) string {
	s := segments(key)
	if len(s) <= depth {
		return key
	}
	return strings.Join(s[:depth], "/")
}

// AutoDepth picks the smallest depth at which the top level splits into at
// least 4 entries, so a repository with everything under "pkg/" or "src/"
// starts one level deeper.
func AutoDepth(keys []string) int {
	maxLen := 1
	for _, k := range keys {
		if n := len(segments(k)); n > maxLen {
			maxLen = n
		}
	}
	for d := 1; d <= maxLen; d++ {
		seen := map[string]struct{}{}
		for _, k := range keys {
			seen[bucket(k, d)] = struct{}{}
		}
		if len(seen) >= 4 {
			return d
		}
	}
	return maxLen
}

// violationSets extracts the packages / package edges involved in findings.
func violationSets(findings []rules.Finding) (badEdges, badPkgs, godPkgs map[string]bool) {
	badEdges, badPkgs, godPkgs = map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, f := range findings {
		switch f.Rule {
		case "import-cycle":
			ks := stringList(f.Data["packages"])
			for i := 0; i+1 < len(ks); i++ {
				badEdges[ks[i]+"->"+ks[i+1]] = true
				badPkgs[ks[i]] = true
			}
		case "layer-violation":
			from, _ := f.Data["from"].(string)
			to, _ := f.Data["to"].(string)
			badEdges[from+"->"+to] = true
			badPkgs[from], badPkgs[to] = true, true
		case "god-package":
			if p, ok := f.Data["pkg"].(string); ok {
				godPkgs[p] = true
			}
		}
	}
	return badEdges, badPkgs, godPkgs
}

func stringList(v any) []string {
	switch xs := v.(type) {
	case []string:
		return xs
	case []any:
		out := make([]string, 0, len(xs))
		for _, x := range xs {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// mergeDuplicates collapses packages that share a key (counts are summed).
// A key can appear twice when the store holds stale data; left as is, the
// folding below would recurse forever on the identical bucket.
func mergeDuplicates(pkgs []graph.PackageNode) []graph.PackageNode {
	out := make([]graph.PackageNode, 0, len(pkgs))
	idx := map[string]int{}
	for _, p := range pkgs {
		if i, ok := idx[p.Key]; ok {
			out[i].Files += p.Files
			out[i].Symbols += p.Symbols
			out[i].FanIn += p.FanIn
			out[i].FanOut += p.FanOut
			continue
		}
		idx[p.Key] = len(out)
		out = append(out, p)
	}
	return out
}

// BuildView folds the package graph by directory for display.
func BuildView(ov *graph.Overview, findings []rules.Finding, opt ViewOptions) View {
	q := strings.ToLower(strings.TrimSpace(opt.Query))
	var internal, external []graph.PackageNode
	for _, p := range mergeDuplicates(ov.Packages) {
		if q != "" && !strings.Contains(strings.ToLower(p.Key), q) {
			continue
		}
		if p.External {
			if opt.External {
				external = append(external, p)
			}
			continue
		}
		internal = append(internal, p)
	}

	depth := opt.Depth
	if depth <= 0 {
		keys := make([]string, len(internal))
		for i, p := range internal {
			keys[i] = p.Key
		}
		depth = AutoDepth(keys)
	}
	expanded := map[string]bool{}
	for _, e := range opt.Expanded {
		expanded[e] = true
	}
	badEdges, badPkgs, godPkgs := violationSets(findings)

	v := View{Depth: depth, Nodes: []ViewNode{}, Edges: []ViewEdge{}, NodeOf: map[string]string{}}

	relLabel := func(key, parentKey string) string {
		if parentKey == "" {
			return key
		}
		if key == parentKey {
			return path.Base(key)
		}
		return strings.TrimPrefix(key, parentKey+"/")
	}
	pkgNode := func(p graph.PackageNode, parentID, parentKey string) ViewNode {
		return ViewNode{
			ID: p.Key, Key: p.Key, Label: relLabel(p.Key, parentKey), Parent: parentID,
			Members: 1, Files: p.Files, Symbols: p.Symbols,
			Bad: badPkgs[p.Key], God: godPkgs[p.Key],
		}
	}

	var fold func(members []graph.PackageNode, d int, parentID, parentKey string, context bool)
	fold = func(members []graph.PackageNode, d int, parentID, parentKey string, context bool) {
		buckets := map[string][]graph.PackageNode{}
		for _, p := range members {
			b := bucket(p.Key, d)
			buckets[b] = append(buckets[b], p)
		}
		for b, ms := range buckets {
			if len(ms) == 1 {
				n := pkgNode(ms[0], parentID, parentKey)
				n.Context = context
				v.Nodes = append(v.Nodes, n)
				v.NodeOf[ms[0].Key] = ms[0].Key
				continue
			}
			g := ViewNode{
				ID: groupPrefix + b, Key: b, Label: relLabel(b, parentKey), Parent: parentID,
				Group: true, Expanded: expanded[b] && !context, Members: len(ms), Context: context,
			}
			for _, p := range ms {
				g.Files += p.Files
				g.Symbols += p.Symbols
				g.Bad = g.Bad || badPkgs[p.Key]
				g.God = g.God || godPkgs[p.Key]
			}
			v.Nodes = append(v.Nodes, g)
			if g.Expanded {
				fold(ms, d+1, g.ID, b, false)
				continue
			}
			for _, p := range ms {
				v.NodeOf[p.Key] = g.ID
			}
		}
	}
	v.Focus = strings.Trim(opt.Focus, "/")
	if v.Focus == "" {
		fold(internal, depth, "", "", false)
	} else {
		inFocus := func(k string) bool { return k == v.Focus || strings.HasPrefix(k, v.Focus+"/") }
		var inside, outside []graph.PackageNode
		for _, p := range internal {
			if inFocus(p.Key) {
				inside = append(inside, p)
			} else {
				outside = append(outside, p)
			}
		}
		// keep only outside packages (internal or external) connected to the focus
		connected := map[string]bool{}
		for _, e := range ov.Edges {
			if inFocus(e.From) != inFocus(e.To) {
				connected[e.From], connected[e.To] = true, true
			}
		}
		var ctx []graph.PackageNode
		for _, p := range outside {
			if connected[p.Key] {
				ctx = append(ctx, p)
			}
		}
		var ext []graph.PackageNode
		for _, p := range external {
			if connected[p.Key] {
				ext = append(ext, p)
			}
		}
		external = ext
		fold(inside, len(segments(v.Focus))+1, "", v.Focus, false)
		fold(ctx, depth, "", "", true)
	}

	for _, p := range external {
		n := pkgNode(p, "", "")
		n.External = true
		n.Context = v.Focus != ""
		n.Label = strings.TrimPrefix(p.Key, "ext:")
		v.Nodes = append(v.Nodes, n)
		v.NodeOf[p.Key] = p.Key
	}

	isContext := map[string]bool{}
	for _, n := range v.Nodes {
		isContext[n.ID] = n.Context
	}
	edges := map[string]*ViewEdge{}
	for _, e := range ov.Edges {
		from, ok1 := v.NodeOf[e.From]
		to, ok2 := v.NodeOf[e.To]
		if !ok1 || !ok2 || from == to || (isContext[from] && isContext[to]) {
			continue
		}
		id := from + "->" + to
		ve := edges[id]
		if ve == nil {
			ve = &ViewEdge{ID: id, From: from, To: to}
			edges[id] = ve
		}
		ve.Weight++
		ve.Bad = ve.Bad || badEdges[e.From+"->"+e.To]
	}
	for _, e := range edges {
		v.Edges = append(v.Edges, *e)
	}

	sort.Slice(v.Nodes, func(i, j int) bool { return v.Nodes[i].ID < v.Nodes[j].ID })
	sort.Slice(v.Edges, func(i, j int) bool { return v.Edges[i].ID < v.Edges[j].ID })
	return v
}
