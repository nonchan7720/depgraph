package cmd

import (
	"github.com/spf13/cobra"

	"github.com/nonchan7720/depgraph/pkg/infrastructure/neo4jprovider"
)

func newScanCommand(o *options) *cobra.Command {
	c := &cobra.Command{
		Use:   "scan",
		Short: "Full scan of --root into Neo4j, run rules, update CLAUDE.md",
		Long: `Full scan of --root into Neo4j, run rules, update CLAUDE.md.
Without --neo4j / NEO4J_URI the running depgraph Neo4j of the root is used;
when none runs, one is started for this scan (10-20 s; ephemeral unless
--persist / store.persist).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			a, err := newApp(ctx, o, neo4jprovider.Start, reportWriter(o), cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			defer a.close(ctx)
			if err := a.fullScan(ctx); err != nil {
				return err
			}
			return a.svc.Evaluate(ctx)
		},
	}
	addNeo4jFlags(c.Flags(), o)
	addPersistFlag(c.Flags(), o)
	addClaudeMDFlag(c.Flags(), o)
	addResetFlag(c.Flags(), o)
	addWorkersFlag(c.Flags(), o)
	return c
}
