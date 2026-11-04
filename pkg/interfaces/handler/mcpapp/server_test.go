package mcpapp

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
	"github.com/nonchan7720/depgraph/pkg/domain/view"
	"github.com/nonchan7720/depgraph/pkg/interfaces/handler/ui"
)

type fakeSource struct {
	opts      ui.ViewOptions
	refreshes int
	findings  []rules.Finding
	pkgErr    error
}

func (f *fakeSource) Graph(_ context.Context, o ui.ViewOptions) (*ui.GraphResponse, error) {
	f.opts = o
	return &ui.GraphResponse{
		Root:     "/repo",
		Stats:    view.Stats{Packages: 3, Files: 7, Symbols: 42},
		View:     ui.View{Depth: 2, Nodes: []ui.ViewNode{{ID: "pkg/a", Key: "pkg/a"}}},
		Findings: f.findings,
	}, nil
}

func (f *fakeSource) Package(_ context.Context, key string) (*view.PackageDetail, error) {
	if f.pkgErr != nil {
		return nil, f.pkgErr
	}
	return &view.PackageDetail{Key: key, Imports: []string{"pkg/b"}}, nil
}

func (f *fakeSource) Refresh(context.Context) error { f.refreshes++; return nil }

var builtApp = fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>depgraph</title><script>/*inline*/</script>")}}

// connect starts the server over in-memory transports and returns a client session.
func connect(t *testing.T, src Source, web fstest.MapFS) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	srv := newServer(src, "test", web)
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func tools(t *testing.T, cs *mcp.ClientSession) map[string]*mcp.Tool {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*mcp.Tool{}
	for _, tl := range res.Tools {
		out[tl.Name] = tl
	}
	return out
}

// toolUI returns the tool's _meta.ui as plain JSON values.
func toolUI(t *testing.T, tl *mcp.Tool) map[string]any {
	t.Helper()
	b, err := json.Marshal(tl.Meta)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	u, _ := m["ui"].(map[string]any)
	return u
}

func TestShowToolLinksTheUIResource(t *testing.T) {
	ts := tools(t, connect(t, &fakeSource{}, builtApp))
	show, ok := ts[ShowTool]
	if !ok {
		t.Fatalf("tools = %v, want %s", ts, ShowTool)
	}
	m := toolUI(t, show)
	if m["resourceUri"] != ResourceURI {
		t.Fatalf("_meta.ui = %v, want resourceUri %s", m, ResourceURI)
	}
	// the model and the view both call it (the view's refresh button re-runs the rules)
	if got := m["visibility"]; !reflect.DeepEqual(got, []any{"model", "app"}) {
		t.Fatalf("visibility = %v", got)
	}
	// legacy hosts read the flat key
	if show.Meta["ui/resourceUri"] != ResourceURI {
		t.Fatalf("_meta = %v, want ui/resourceUri", show.Meta)
	}
}

func TestDataToolsAreAppOnly(t *testing.T) {
	ts := tools(t, connect(t, &fakeSource{}, builtApp))
	for _, name := range []string{GraphTool, PackageTool} {
		tl, ok := ts[name]
		if !ok {
			t.Fatalf("missing tool %s", name)
		}
		if got := toolUI(t, tl)["visibility"]; !reflect.DeepEqual(got, []any{"app"}) {
			t.Fatalf("%s visibility = %v, want [app]", name, got)
		}
		if tl.Annotations == nil || !tl.Annotations.ReadOnlyHint {
			t.Fatalf("%s should be annotated read-only", name)
		}
	}
}

func TestResourceServesTheBuiltApp(t *testing.T) {
	cs := connect(t, &fakeSource{}, builtApp)
	ctx := context.Background()
	list, err := cs.ListResources(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Resources) != 1 || list.Resources[0].URI != ResourceURI || list.Resources[0].MIMEType != MIMEType {
		t.Fatalf("resources = %+v", list.Resources)
	}
	res, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: ResourceURI})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Contents) != 1 {
		t.Fatalf("contents = %+v", res.Contents)
	}
	c := res.Contents[0]
	if c.MIMEType != MIMEType || !strings.Contains(c.Text, "<title>depgraph</title>") {
		t.Fatalf("content = %s %q", c.MIMEType, c.Text)
	}
}

func TestResourceNotBuilt(t *testing.T) {
	cs := connect(t, &fakeSource{}, fstest.MapFS{".gitkeep": {}})
	_, err := cs.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: ResourceURI})
	if err == nil || !strings.Contains(err.Error(), "mise run build") {
		t.Fatalf("err = %v, want the build guidance", err)
	}
}

func TestShowRefreshesAndSummarises(t *testing.T) {
	src := &fakeSource{findings: []rules.Finding{
		{Rule: "import-cycle", Severity: "error", Message: "cycle a -> b -> a"},
		{Rule: "unused-export", Severity: "warn", Message: "pkg/a.Foo is never used"},
		{Rule: "unused-export", Severity: "warn", Message: "pkg/a.Bar is never used"},
	}}
	cs := connect(t, src, builtApp)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: ShowTool})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("isError: %+v", res.Content)
	}
	if src.refreshes != 1 {
		t.Fatalf("refreshes = %d, want 1 (the rules are re-run on every show)", src.refreshes)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	for _, want := range []string{"/repo", "3 packages", "7 files", "3 findings", "import-cycle: 1", "unused-export: 2", "cycle a -> b -> a"} {
		if !strings.Contains(text, want) {
			t.Fatalf("summary %q lacks %q", text, want)
		}
	}
}

func TestGraphToolPassesViewOptions(t *testing.T) {
	src := &fakeSource{}
	cs := connect(t, src, builtApp)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: GraphTool, Arguments: map[string]any{
		"depth": 3, "expand": []string{"pkg/a", "pkg/b"}, "ext": true, "q": "repo", "focus": "/pkg/",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("isError: %+v", res.Content)
	}
	want := ui.ViewOptions{Depth: 3, Expanded: []string{"pkg/a", "pkg/b"}, External: true, Query: "repo", Focus: "pkg"}
	if !reflect.DeepEqual(src.opts, want) {
		t.Fatalf("options = %+v, want %+v", src.opts, want)
	}
	// the view reads structuredContent: the same JSON as GET /api/graph
	b, _ := json.Marshal(res.StructuredContent)
	var got ui.GraphResponse
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Root != "/repo" || got.Stats.Packages != 3 || len(got.View.Nodes) != 1 {
		t.Fatalf("structuredContent = %s", b)
	}
}

func TestPackageTool(t *testing.T) {
	cs := connect(t, &fakeSource{}, builtApp)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: PackageTool, Arguments: map[string]any{"key": "pkg/a"}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(b), `"key":"pkg/a"`) || !strings.Contains(string(b), `"pkg/b"`) {
		t.Fatalf("structuredContent = %s", b)
	}
}

func TestPackageToolErrors(t *testing.T) {
	cs := connect(t, &fakeSource{pkgErr: errors.New("boom")}, builtApp)
	for name, args := range map[string]map[string]any{"no key": {}, "source error": {"key": "pkg/a"}} {
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: PackageTool, Arguments: args})
		if err == nil && !res.IsError {
			t.Fatalf("%s: want an error result", name)
		}
	}
}

func TestEmbeddedAppHasPlaceholder(t *testing.T) {
	if _, err := embeddedApp().Open(".gitkeep"); err != nil {
		t.Fatalf("dist/.gitkeep missing from embed: %v", err)
	}
}
