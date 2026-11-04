// Package mcpapp serves the browser UI as an MCP App (the MCP Apps extension,
// io.modelcontextprotocol/ui) over a stdio MCP server: a host that supports it
// renders the same Cytoscape + ELK graph inline in the conversation.
//
// The view runs in the host's sandboxed iframe with no network access, so it
// reads the graph through app-only tools (proxied by the host) instead of the
// HTTP API and the event stream of `depgraph serve`.
package mcpapp

import (
	"context"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
	"github.com/nonchan7720/depgraph/pkg/domain/view"
	"github.com/nonchan7720/depgraph/pkg/interfaces/handler/ui"
)

const (
	// ResourceURI is the UI resource the show tool links to.
	ResourceURI = "ui://depgraph/graph.html"
	// MIMEType marks an HTML resource as an MCP App.
	MIMEType = "text/html;profile=mcp-app"

	// ShowTool opens the graph view (model and app).
	ShowTool = "show_dependency_graph"
	// GraphTool answers the view's graph requests (app only).
	GraphTool = "graph_view"
	// PackageTool answers the view's package detail requests (app only).
	PackageTool = "package_detail"

	// summaryFindings is how many finding messages the show tool lists for the model.
	summaryFindings = 20
)

// Source is what the tools read: the UI server's view of the project graph,
// plus a re-run of the rules.
type Source interface {
	Graph(ctx context.Context, o ui.ViewOptions) (*ui.GraphResponse, error)
	Package(ctx context.Context, key string) (*view.PackageDetail, error)
	// Refresh re-runs the rules over what is in the store.
	Refresh(ctx context.Context) error
}

// New returns the MCP server over src, serving the embedded app.
func New(src Source, version string) *mcp.Server {
	return newServer(src, version, embeddedApp())
}

func uiMeta(m map[string]any) mcp.Meta { return mcp.Meta{"ui": m} }

func newServer(src Source, version string, web fs.FS) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "depgraph", Version: version}, nil)

	s.AddResource(&mcp.Resource{
		URI:         ResourceURI,
		Name:        "depgraph",
		Title:       "depgraph dependency graph",
		Description: "Interactive package dependency graph with the findings of the depgraph rules",
		MIMEType:    MIMEType,
	}, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		html, err := fs.ReadFile(web, "index.html")
		if err != nil {
			return nil, fmt.Errorf("%s", ui.NotBuiltMessage)
		}
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{
			URI:      ResourceURI,
			MIMEType: MIMEType,
			Text:     string(html),
			// everything is inlined: no network, no external resources
			Meta: uiMeta(map[string]any{"prefersBorder": true}),
		}}}, nil
	})

	show := uiMeta(map[string]any{"resourceUri": ResourceURI, "visibility": []string{"model", "app"}})
	show["ui/resourceUri"] = ResourceURI // hosts that predate _meta.ui
	mcp.AddTool(s, &mcp.Tool{
		Name:  ShowTool,
		Title: "Show dependency graph",
		Description: "Re-run the depgraph rules over the project's graph and show it as an interactive view " +
			"(packages, imports, cycles, layer violations, unused code). Returns a summary of the findings.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
		Meta:        show,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, summary, error) {
		if err := src.Refresh(ctx); err != nil {
			return nil, summary{}, err
		}
		g, err := src.Graph(ctx, ui.ViewOptions{})
		if err != nil {
			return nil, summary{}, err
		}
		sum := summarise(g)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: sum.text(g.Findings)}}}, sum, nil
	})

	// The data tools return `any`: no output schema is derived, so the view
	// gets the API JSON as is (a schema from the Go types would reject nil
	// slices and maps that the HTTP API sends as null).
	appOnly := func() mcp.Meta { return uiMeta(map[string]any{"visibility": []string{"app"}}) }
	mcp.AddTool(s, &mcp.Tool{
		Name:        GraphTool,
		Title:       "Graph view",
		Description: "The folded package graph and findings for the depgraph view (same JSON as GET /api/graph).",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
		Meta:        appOnly(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in graphInput) (*mcp.CallToolResult, any, error) {
		g, err := src.Graph(ctx, in.options())
		if err != nil {
			return nil, nil, err
		}
		// the view reads structuredContent; keep the text short for hosts that show it
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{
			Text: fmt.Sprintf("%d nodes, %d edges, %d findings", len(g.View.Nodes), len(g.View.Edges), len(g.Findings)),
		}}}, g, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        PackageTool,
		Title:       "Package detail",
		Description: "Files, symbols and import neighbours of one package (same JSON as GET /api/package).",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
		Meta:        appOnly(),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in packageInput) (*mcp.CallToolResult, any, error) {
		if in.Key == "" {
			return nil, nil, fmt.Errorf("key required")
		}
		d, err := src.Package(ctx, in.Key)
		if err != nil {
			return nil, nil, err
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{
			Text: fmt.Sprintf("%s: %d files, imports %d, imported by %d", d.Key, len(d.Files), len(d.Imports), len(d.ImportedBy)),
		}}}, d, nil
	})
	return s
}

// graphInput mirrors the query of GET /api/graph.
type graphInput struct {
	Depth  int      `json:"depth,omitempty" jsonschema:"path segments shown before folding into groups (0: automatic)"`
	Expand []string `json:"expand,omitempty" jsonschema:"group prefixes opened in place"`
	Ext    bool     `json:"ext,omitempty" jsonschema:"show packages outside the repository"`
	Q      string   `json:"q,omitempty" jsonschema:"keep only packages whose key contains this text"`
	Focus  string   `json:"focus,omitempty" jsonschema:"directory prefix to drill into"`
}

func (in graphInput) options() ui.ViewOptions {
	o := ui.ViewOptions{Depth: in.Depth, External: in.Ext, Query: in.Q, Focus: strings.Trim(in.Focus, "/")}
	for _, e := range in.Expand {
		if e = strings.TrimSpace(e); e != "" {
			o.Expanded = append(o.Expanded, e)
		}
	}
	return o
}

type packageInput struct {
	Key string `json:"key" jsonschema:"package key, as shown in the graph"`
}

// summary is the structured result of the show tool.
type summary struct {
	Root     string         `json:"root"`
	Packages int64          `json:"packages"`
	Files    int64          `json:"files"`
	Symbols  int64          `json:"symbols"`
	Findings int            `json:"findings"`
	ByRule   map[string]int `json:"byRule"`
}

func summarise(g *ui.GraphResponse) summary {
	s := summary{Root: g.Root, Packages: g.Stats.Packages, Files: g.Stats.Files, Symbols: g.Stats.Symbols,
		Findings: len(g.Findings), ByRule: map[string]int{}}
	for _, f := range g.Findings {
		s.ByRule[f.Rule]++
	}
	return s
}

func (s summary) text(findings []rules.Finding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "depgraph: %s\n%d packages, %d files, %d symbols\n%d findings\n", s.Root, s.Packages, s.Files, s.Symbols, s.Findings)
	rs := make([]string, 0, len(s.ByRule))
	for r := range s.ByRule {
		rs = append(rs, r)
	}
	sort.Strings(rs)
	for _, r := range rs {
		fmt.Fprintf(&b, "  %s: %d\n", r, s.ByRule[r])
	}
	for i, f := range findings {
		if i == summaryFindings {
			fmt.Fprintf(&b, "… %d more (see the graph view)\n", len(findings)-i)
			break
		}
		fmt.Fprintf(&b, "- [%s] %s: %s\n", f.Severity, f.Rule, f.Message)
	}
	return b.String()
}
