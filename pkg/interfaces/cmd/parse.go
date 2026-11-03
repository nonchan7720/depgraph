package cmd

import (
	"encoding/json"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver/treesitter"
)

func newParseCommand(o *options) *cobra.Command {
	c := &cobra.Command{
		Use:   "parse [file...]",
		Short: "Debug: print the tree-sitter parse as JSON, one FileGraph per line (no Neo4j)",
		Long: `Print the tree-sitter parse of --root (or only the given files) as JSON,
one FileGraph per line. Neo4j is not needed.`,
		RunE: func(cmd *cobra.Command, files []string) error {
			ctx := cmd.Context()
			r := treesitter.New()
			defer r.Close()
			enc := json.NewEncoder(cmd.OutOrStdout())
			if len(files) == 0 {
				return resolver.ScanRepoWorkers(o.workers)(ctx, r, o.root, func(fg *graph.FileGraph) error { return enc.Encode(fg) })
			}
			for _, f := range files {
				abs, err := filepath.Abs(f)
				if err != nil {
					return err
				}
				rel, err := filepath.Rel(o.root, abs)
				if err != nil {
					return err
				}
				fg, err := r.ResolveFile(ctx, o.root, filepath.ToSlash(rel))
				if err != nil {
					return err
				}
				if err := enc.Encode(fg); err != nil {
					return err
				}
			}
			return nil
		},
	}
	addWorkersFlag(c.Flags(), o)
	return c
}
