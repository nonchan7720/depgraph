package ui

import (
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
	"github.com/nonchan7720/depgraph/pkg/domain/view"
)

func pkgs(keys ...string) []view.PackageNode {
	out := make([]view.PackageNode, 0, len(keys))
	for _, k := range keys {
		out = append(out, view.PackageNode{Key: k, Symbols: 1})
	}
	return out
}

func nodeByID(v View, id string) (ViewNode, bool) {
	for _, n := range v.Nodes {
		if n.ID == id {
			return n, true
		}
	}
	return ViewNode{}, false
}

func edgeByID(v View, id string) (ViewEdge, bool) {
	for _, e := range v.Edges {
		if e.ID == id {
			return e, true
		}
	}
	return ViewEdge{}, false
}

func sampleOverview() *view.Overview {
	return &view.Overview{
		Packages: pkgs(
			"cmd/app",
			"pkg/domain/user",
			"pkg/domain/site",
			"pkg/infra/ent/user",
			"pkg/infra/ent/site",
			"pkg/infra/mysql",
			"pkg/infra",
			"pkg/util",
		),
		Edges: []view.Edge{
			{From: "cmd/app", To: "pkg/infra/mysql"},
			{From: "pkg/infra/mysql", To: "pkg/domain/user"},
			{From: "pkg/infra/ent/user", To: "pkg/domain/user"},
			{From: "pkg/infra/ent/site", To: "pkg/domain/site"},
			{From: "pkg/infra/ent/user", To: "pkg/infra/ent/site"}, // inside one group
			{From: "pkg/domain/user", To: "pkg/util"},
		},
	}
}

func TestAutoDepth(t *testing.T) {
	cases := []struct {
		keys []string
		want int
	}{
		// depth 1 gives only {cmd, pkg}; depth 2 gives cmd/app, pkg/domain, pkg/infra, pkg/util
		{[]string{"cmd/app", "pkg/domain/user", "pkg/infra/ent/user", "pkg/util"}, 2},
		{[]string{"a", "b", "c", "d", "e/f"}, 1},
		{[]string{"only/one/deep"}, 3},
		{nil, 1},
	}
	for _, c := range cases {
		if got := AutoDepth(c.keys); got != c.want {
			t.Errorf("AutoDepth(%v) = %d, want %d", c.keys, got, c.want)
		}
	}
}

func TestBuildViewCollapsesDirectories(t *testing.T) {
	v := BuildView(sampleOverview(), nil, ViewOptions{Depth: 2})

	// single-member buckets stay plain package nodes
	if n, ok := nodeByID(v, "cmd/app"); !ok || n.Group {
		t.Errorf("cmd/app should be a plain package node: %+v ok=%v", n, ok)
	}
	if n, ok := nodeByID(v, "pkg/util"); !ok || n.Group {
		t.Errorf("pkg/util should be a plain package node: %+v ok=%v", n, ok)
	}
	// multi-member buckets collapse into a group; the package equal to the prefix joins it
	infra, ok := nodeByID(v, "g:pkg/infra")
	if !ok || !infra.Group || infra.Members != 4 {
		t.Fatalf("g:pkg/infra should be a collapsed group of 4: %+v ok=%v", infra, ok)
	}
	if infra.Label != "pkg/infra" {
		t.Errorf("top-level group label = %q", infra.Label)
	}
	if _, ok := nodeByID(v, "pkg/infra/mysql"); ok {
		t.Error("members of a collapsed group must not be shown")
	}
	if len(v.Nodes) != 4 {
		t.Errorf("want 4 nodes (cmd/app, pkg/util, 2 groups), got %d: %+v", len(v.Nodes), v.Nodes)
	}
	if v.NodeOf["pkg/infra/ent/site"] != "g:pkg/infra" {
		t.Errorf("NodeOf maps package to its visible node, got %q", v.NodeOf["pkg/infra/ent/site"])
	}

	// edges are aggregated between visible nodes, self loops dropped
	e, ok := edgeByID(v, "g:pkg/infra->g:pkg/domain")
	if !ok || e.Weight != 3 {
		t.Errorf("infra->domain should aggregate 3 package edges: %+v ok=%v", e, ok)
	}
	if _, ok := edgeByID(v, "g:pkg/infra->g:pkg/infra"); ok {
		t.Error("edges inside one group must be dropped")
	}
	if e, ok := edgeByID(v, "cmd/app->g:pkg/infra"); !ok || e.Weight != 1 {
		t.Errorf("cmd/app->infra: %+v ok=%v", e, ok)
	}
}

func TestBuildViewExpandedGroup(t *testing.T) {
	v := BuildView(sampleOverview(), nil, ViewOptions{Depth: 2, Expanded: []string{"pkg/infra"}})

	box, ok := nodeByID(v, "g:pkg/infra")
	if !ok || !box.Group || !box.Expanded {
		t.Fatalf("expanded group should be a compound box: %+v ok=%v", box, ok)
	}
	ent, ok := nodeByID(v, "g:pkg/infra/ent")
	if !ok || ent.Parent != "g:pkg/infra" || ent.Members != 2 || ent.Label != "ent" {
		t.Errorf("pkg/infra/ent should be a collapsed sub-group inside the box: %+v ok=%v", ent, ok)
	}
	mysql, ok := nodeByID(v, "pkg/infra/mysql")
	if !ok || mysql.Parent != "g:pkg/infra" || mysql.Label != "mysql" {
		t.Errorf("pkg/infra/mysql should be a child with a relative label: %+v ok=%v", mysql, ok)
	}
	self, ok := nodeByID(v, "pkg/infra")
	if !ok || self.Parent != "g:pkg/infra" || self.Group {
		t.Errorf("the package equal to the prefix is a child of its own box: %+v ok=%v", self, ok)
	}
	if e, ok := edgeByID(v, "g:pkg/infra/ent->g:pkg/domain"); !ok || e.Weight != 2 {
		t.Errorf("ent->domain should aggregate 2 edges: %+v ok=%v", e, ok)
	}
	// edges never attach to an expanded box itself
	for _, e := range v.Edges {
		if e.From == "g:pkg/infra" || e.To == "g:pkg/infra" {
			t.Errorf("edge attached to expanded box: %+v", e)
		}
	}
}

func TestBuildViewFindingsPropagate(t *testing.T) {
	findings := []rules.Finding{
		{Rule: "layer-violation", Data: map[string]any{"from": "pkg/infra/mysql", "to": "pkg/domain/user"}},
		{Rule: "god-package", Data: map[string]any{"pkg": "pkg/util"}},
		{Rule: "import-cycle", Data: map[string]any{"packages": []any{"cmd/app", "pkg/infra/mysql", "cmd/app"}}},
	}
	v := BuildView(sampleOverview(), findings, ViewOptions{Depth: 2})

	for _, id := range []string{"g:pkg/infra", "g:pkg/domain", "cmd/app"} {
		if n, _ := nodeByID(v, id); !n.Bad {
			t.Errorf("%s should be marked bad", id)
		}
	}
	if n, _ := nodeByID(v, "pkg/util"); !n.God || n.Bad {
		t.Errorf("pkg/util should be god only: %+v", n)
	}
	if e, _ := edgeByID(v, "g:pkg/infra->g:pkg/domain"); !e.Bad {
		t.Error("aggregated edge containing a violation should be bad")
	}
	if e, _ := edgeByID(v, "cmd/app->g:pkg/infra"); !e.Bad {
		t.Error("cycle edge cmd/app->pkg/infra/mysql should be bad")
	}
}

func TestBuildViewExternalAndQuery(t *testing.T) {
	ov := sampleOverview()
	ov.Packages = append(ov.Packages, view.PackageNode{Key: "ext:fmt", External: true})
	ov.Edges = append(ov.Edges, view.Edge{From: "pkg/util", To: "ext:fmt"})

	v := BuildView(ov, nil, ViewOptions{Depth: 2})
	if _, ok := nodeByID(v, "ext:fmt"); ok {
		t.Error("external packages are hidden by default")
	}
	v = BuildView(ov, nil, ViewOptions{Depth: 2, External: true})
	if n, ok := nodeByID(v, "ext:fmt"); !ok || !n.External || n.Label != "fmt" {
		t.Errorf("external node: %+v ok=%v", n, ok)
	}

	v = BuildView(ov, nil, ViewOptions{Depth: 2, Query: "ent/"})
	g, ok := nodeByID(v, "g:pkg/infra")
	if !ok || g.Members != 2 {
		t.Errorf("query keeps only matching packages: %+v ok=%v", g, ok)
	}
	if len(v.Nodes) != 1 {
		t.Errorf("only the infra group should remain, got %+v", v.Nodes)
	}
}

func TestParseViewOptions(t *testing.T) {
	o := parseViewOptions(map[string][]string{
		"depth": {"3"}, "expand": {"pkg/a, pkg/b,,"}, "ext": {"1"}, "q": {"ent"},
	})
	if o.Depth != 3 || !o.External || o.Query != "ent" || len(o.Expanded) != 2 || o.Expanded[1] != "pkg/b" {
		t.Errorf("parseViewOptions = %+v", o)
	}
	if o := parseViewOptions(nil); o.Depth != 0 || o.External || len(o.Expanded) != 0 {
		t.Errorf("empty query = %+v", o)
	}
}

func TestBuildViewFocus(t *testing.T) {
	v := BuildView(sampleOverview(), nil, ViewOptions{Depth: 2, Focus: "pkg/infra"})
	if v.Focus != "pkg/infra" {
		t.Errorf("Focus echoed = %q", v.Focus)
	}

	// inside the focus: next level, labels relative to the focus
	ent, ok := nodeByID(v, "g:pkg/infra/ent")
	if !ok || !ent.Group || ent.Members != 2 || ent.Label != "ent" || ent.Context {
		t.Errorf("ent should be a folded group inside the focus: %+v ok=%v", ent, ok)
	}
	mysql, ok := nodeByID(v, "pkg/infra/mysql")
	if !ok || mysql.Label != "mysql" || mysql.Context {
		t.Errorf("mysql: %+v ok=%v", mysql, ok)
	}
	self, ok := nodeByID(v, "pkg/infra")
	if !ok || self.Label != "infra" {
		t.Errorf("the package equal to the focus is labelled by its base name: %+v ok=%v", self, ok)
	}

	// outside packages connected to the focus appear as folded context nodes
	dom, ok := nodeByID(v, "g:pkg/domain")
	if !ok || !dom.Context || dom.Members != 2 || dom.Label != "pkg/domain" {
		t.Errorf("pkg/domain should be a context group: %+v ok=%v", dom, ok)
	}
	app, ok := nodeByID(v, "cmd/app")
	if !ok || !app.Context {
		t.Errorf("cmd/app should be a context node: %+v ok=%v", app, ok)
	}
	if _, ok := nodeByID(v, "pkg/util"); ok {
		t.Error("packages not connected to the focus must be hidden")
	}

	if e, ok := edgeByID(v, "g:pkg/infra/ent->g:pkg/domain"); !ok || e.Weight != 2 {
		t.Errorf("ent->domain: %+v ok=%v", e, ok)
	}
	if e, ok := edgeByID(v, "cmd/app->pkg/infra/mysql"); !ok || e.Weight != 1 {
		t.Errorf("cmd/app->mysql: %+v ok=%v", e, ok)
	}
	for _, e := range v.Edges {
		f, _ := nodeByID(v, e.From)
		to, _ := nodeByID(v, e.To)
		if f.Context && to.Context {
			t.Errorf("edges between two context nodes must be dropped: %+v", e)
		}
	}
	if len(v.Nodes) != 5 {
		t.Errorf("want 5 nodes (ent, mysql, infra, domain, cmd/app), got %+v", v.Nodes)
	}
}

func TestBuildViewFocusOnLeafPackage(t *testing.T) {
	// focusing a prefix with a single package still works and shows its neighbours
	v := BuildView(sampleOverview(), nil, ViewOptions{Depth: 2, Focus: "pkg/util"})
	if n, ok := nodeByID(v, "pkg/util"); !ok || n.Context {
		t.Errorf("pkg/util should be shown inside the focus: %+v ok=%v", n, ok)
	}
	if n, ok := nodeByID(v, "pkg/domain/user"); !ok || !n.Context {
		t.Errorf("its importer should be a context node: %+v ok=%v", n, ok)
	}
}

func TestParseViewOptionsFocus(t *testing.T) {
	if o := parseViewOptions(map[string][]string{"focus": {"pkg/infra/"}}); o.Focus != "pkg/infra" {
		t.Errorf("focus = %q", o.Focus)
	}
}

func TestBuildViewDuplicateKeysDoNotRecurseForever(t *testing.T) {
	// The same package key can appear twice (e.g. left over from an older
	// schema). Opening its group used to recurse until the stack overflowed.
	ov := &view.Overview{
		Packages: []view.PackageNode{
			{Key: "handler", Files: 1, Symbols: 2},
			{Key: "handler", Files: 3, Symbols: 4},
			{Key: "repository", Files: 1, Symbols: 1},
		},
		Edges: []view.Edge{{From: "handler", To: "repository"}},
	}
	done := make(chan View, 1)
	go func() {
		done <- BuildView(ov, nil, ViewOptions{Depth: 1, Expanded: []string{"handler"}})
	}()
	select {
	case v := <-done:
		n, ok := nodeByID(v, "handler")
		if !ok || n.Group || n.Files != 4 || n.Symbols != 6 {
			t.Errorf("duplicates should merge into one package node: %+v ok=%v", n, ok)
		}
		if _, ok := nodeByID(v, "g:handler"); ok {
			t.Error("a merged duplicate must not become a group")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("BuildView did not return (infinite recursion)")
	}
}
