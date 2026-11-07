package cmd

import (
	"github.com/spf13/cobra"

	"github.com/nonchan7720/depgraph/pkg/infrastructure/neo4jprovider"
)

func newCheckCommand(o *options) *cobra.Command {
	var failOn string
	c := &cobra.Command{
		Use:   "check",
		Short: "Run rules against what is already in Neo4j (updates CLAUDE.md)",
		Long: `Run rules against what is already in Neo4j (updates CLAUDE.md).
Without --neo4j / NEO4J_URI the running depgraph Neo4j of the root is used;
when none runs, one is started (10-20 s; an ephemeral one is empty, so run
scan or watch first, or use --persist / store.persist).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			floor, err := parseFailOn(failOn)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			report := newFailOnRecorder(o)
			a, err := newApp(ctx, o, neo4jprovider.Start, report, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			defer a.close(ctx)
			if err := a.svc.Evaluate(ctx); err != nil {
				return err
			}
			return report.result(floor)
		},
	}
	addNeo4jFlags(c.Flags(), o)
	addPersistFlag(c.Flags(), o)
	addClaudeMDFlag(c.Flags(), o)
	addFailOnFlag(c.Flags(), &failOn)
	return c
}
