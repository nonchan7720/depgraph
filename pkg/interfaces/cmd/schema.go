package cmd

import (
	"github.com/spf13/cobra"

	"github.com/nonchan7720/depgraph/pkg/infrastructure/config"
)

func newSchemaCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "schema",
		Short: "Print the JSON Schema of .depgraph.yml",
		Long: `Print the JSON Schema (draft-07) of .depgraph.yml, for editors
(yaml-language-server and others). The same schema is published at
` + config.SchemaID + `.`,
		Args:              cobra.NoArgs,
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil }, // no root to prepare
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := cmd.OutOrStdout().Write(config.Schema())
			return err
		},
	}
}
