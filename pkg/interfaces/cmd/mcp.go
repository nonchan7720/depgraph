package cmd

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/nonchan7720/depgraph/pkg/infrastructure/neo4jprovider"
	"github.com/nonchan7720/depgraph/pkg/interfaces/handler/mcpapp"
	"github.com/nonchan7720/depgraph/pkg/interfaces/handler/ui"
	"github.com/nonchan7720/depgraph/pkg/version"
)

func newMCPCommand(o *options) *cobra.Command {
	c := &cobra.Command{
		Use:   "mcp",
		Short: "Serve the browser UI as an MCP App over stdio (does not touch CLAUDE.md)",
		Long: `Run an MCP server on stdin/stdout whose show_dependency_graph tool opens the
browser UI inside an MCP Apps host (the graph is rendered in the conversation).
It reads what is already in Neo4j, like serve: run scan or watch to fill it.
Each show re-runs the rules. Logs go to stderr; stdout carries the protocol.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			out := cmd.ErrOrStderr()                                            // stdout is the MCP transport
			a, err := newApp(ctx, o, neo4jprovider.StartIfPersistent, nil, out) // never writes CLAUDE.md
			if err != nil {
				return err
			}
			defer a.close(ctx)
			srv := ui.New(a.graph, o.root)
			a.svc.SetNotifier(srv)
			if err := a.svc.Evaluate(ctx); err != nil {
				return err
			}
			src := mcpSource{Server: srv, refresh: a.svc.Evaluate}
			return mcpapp.New(src, version.Version).Run(ctx, &mcp.StdioTransport{})
		},
	}
	addNeo4jFlags(c.Flags(), o)
	addPersistFlag(c.Flags(), o)
	return c
}

// mcpSource is the UI server plus a re-run of the rules (which notifies it).
type mcpSource struct {
	*ui.Server
	refresh func(context.Context) error
}

func (s mcpSource) Refresh(ctx context.Context) error { return s.refresh(ctx) }
