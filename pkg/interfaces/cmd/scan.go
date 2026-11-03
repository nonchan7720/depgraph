package cmd

import (
	"github.com/spf13/cobra"
)

func newScanCommand(o *options) *cobra.Command {
	c := &cobra.Command{
		Use:   "scan",
		Short: "Full scan of --root into Neo4j, run rules, update CLAUDE.md",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			a, err := newApp(ctx, o, reportWriter(o), cmd.ErrOrStderr())
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
	addClaudeMDFlag(c.Flags(), o)
	addResetFlag(c.Flags(), o)
	return c
}
