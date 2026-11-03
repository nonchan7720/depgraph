package graph

import (
	"reflect"
	"testing"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
)

// The usage of a file is stored on its File node and replaced (or removed)
// by the next UpsertFile of that file.
func TestUpsertFileStoresUsage(t *testing.T) {
	s, ctx := testStore(t)
	r, key := testRepo(t, s)
	fg := testFile("a", "a.go", "F")
	fg.Usage = &graph.Usage{
		Scope: ".", Entry: "main package", Generated: true,
		Uses:    []graph.Use{{Target: "b", Name: "G"}, {Target: "c/x.ts", Name: "*", ReExport: true, As: "*"}},
		Exports: []graph.ExportName{{Name: "default", Local: "F"}},
		Keep:    []string{"F"},
	}
	if err := r.UpsertFile(ctx, fg); err != nil {
		t.Fatal(err)
	}
	read := func() map[string]any {
		rows, err := s.Query(ctx, `
			MATCH (f:File {project: $p, path: 'a/a.go'})
			RETURN f.scope AS scope, f.entry AS entry, f.generated AS generated,
			       f.uses AS uses, f.exports AS exports, f.keep AS keep`, map[string]any{"p": key})
		if err != nil || len(rows) != 1 {
			t.Fatalf("rows=%v err=%v", rows, err)
		}
		return rows[0]
	}
	got := read()
	if got["scope"] != "." || got["entry"] != "main package" || got["generated"] != true {
		t.Errorf("props = %v", got)
	}
	var uses []graph.Use
	for _, v := range got["uses"].([]any) {
		u, ok := graph.DecodeUse(v.(string))
		if !ok {
			t.Fatalf("bad use %q", v)
		}
		uses = append(uses, u)
	}
	if !reflect.DeepEqual(uses, fg.Usage.Uses) {
		t.Errorf("uses = %+v", uses)
	}
	if !reflect.DeepEqual(got["exports"], []any{"default=F"}) || !reflect.DeepEqual(got["keep"], []any{"F"}) {
		t.Errorf("exports=%v keep=%v", got["exports"], got["keep"])
	}

	fg.Usage = nil
	if err := r.UpsertFile(ctx, fg); err != nil {
		t.Fatal(err)
	}
	for k, v := range read() {
		if v != nil {
			t.Errorf("%s = %v after an upsert without usage", k, v)
		}
	}
}
