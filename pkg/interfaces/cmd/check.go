package cmd

import (
	"github.com/spf13/cobra"
)

func newCheckCommand(o *options) *cobra.Command {
	c := &cobra.Command{
		Use:   "check",
		Short: "Run rules against what is already in Neo4j (updates CLAUDE.md)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			a, err := newApp(ctx, o, reportWriter(o), cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			defer a.close(ctx)
			return a.svc.Evaluate(ctx)
		},
	}
	addNeo4jFlags(c.Flags(), o)
	addClaudeMDFlag(c.Flags(), o)
	return c
}
