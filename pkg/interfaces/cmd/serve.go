package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/nonchan7720/depgraph/pkg/infrastructure/neo4jprovider"
	"github.com/nonchan7720/depgraph/pkg/interfaces/handler/ui"
)

func newServeCommand(o *options) *cobra.Command {
	c := &cobra.Command{
		Use:   "serve",
		Short: "Serve the browser UI over what is already in Neo4j (does not touch CLAUDE.md)",
		Long: `Serve the browser UI over what is already in Neo4j (does not touch CLAUDE.md).
Without --neo4j / NEO4J_URI the running depgraph Neo4j of the root is used; a
stopped persistent one is started, an ephemeral one is never started (it
would be empty).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			out := cmd.ErrOrStderr()
			a, err := newApp(ctx, o, neo4jprovider.StartIfPersistent, nil, out) // serve never writes CLAUDE.md
			if err != nil {
				return err
			}
			defer a.close(ctx)
			srv := ui.New(a.graph, o.root)
			a.svc.SetNotifier(srv)
			if err := a.svc.Evaluate(ctx); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(out, "ui: http://%s (ctrl-c to stop)\n", o.addr)
			return srv.ListenAndServe(ctx, o.addr)
		},
	}
	addNeo4jFlags(c.Flags(), o)
	addPersistFlag(c.Flags(), o)
	addAddrFlag(c.Flags(), o)
	return c
}
