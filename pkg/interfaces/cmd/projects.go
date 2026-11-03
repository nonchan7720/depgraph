package cmd

import (
	"context"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/nonchan7720/depgraph/pkg/domain/project"
	projectrepo "github.com/nonchan7720/depgraph/pkg/infrastructure/repository/project"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/workspace"
)

func newProjectsCommand(o *options) *cobra.Command {
	c := &cobra.Command{
		Use:   "projects",
		Short: "List and remove the projects stored in the shared Neo4j database",
		Args:  cobra.NoArgs,
	}
	addNeo4jFlags(c.PersistentFlags(), o)

	withRepo := func(cmd *cobra.Command, fn func(ctx context.Context, repo project.Repository) error) error {
		ctx := cmd.Context()
		st, err := connect(ctx, o)
		if err != nil {
			return err
		}
		defer func() { _ = st.Close(context.WithoutCancel(ctx)) }()
		return fn(ctx, projectrepo.New(st))
	}

	list := &cobra.Command{
		Use:   "list",
		Short: "List projects (key, base, worktree, root, last scan, files)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withRepo(cmd, func(ctx context.Context, repo project.Repository) error {
				ps, err := repo.List(ctx)
				if err != nil {
					return err
				}
				printProjects(cmd.OutOrStdout(), ps)
				return nil
			})
		},
	}

	rm := &cobra.Command{
		Use:   "rm <key>",
		Short: "Delete a project and all of its data",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withRepo(cmd, func(ctx context.Context, repo project.Repository) error {
				p, err := repo.Get(ctx, args[0])
				if err != nil {
					return err
				}
				if p == nil {
					return fmt.Errorf("no project %q (see `depgraph projects list`)", args[0])
				}
				if err := repo.Delete(ctx, p.Key); err != nil {
					return err
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "removed %s (%s)\n", p.Key, p.Root)
				return nil
			})
		},
	}

	var dryRun bool
	prune := &cobra.Command{
		Use:   "prune",
		Short: "Delete projects whose root no longer exists",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return withRepo(cmd, func(ctx context.Context, repo project.Repository) error {
				ps, err := repo.List(ctx)
				if err != nil {
					return err
				}
				stale := project.Stale(ps, workspace.Exists)
				out := cmd.OutOrStdout()
				if len(stale) == 0 {
					_, _ = fmt.Fprintln(out, "nothing to prune")
					return nil
				}
				if dryRun {
					_, _ = fmt.Fprintln(out, "would remove:")
					printProjects(out, stale)
					return nil
				}
				for _, p := range stale {
					if err := repo.Delete(ctx, p.Key); err != nil {
						return err
					}
					_, _ = fmt.Fprintf(out, "removed %s (%s)\n", p.Key, p.Root)
				}
				return nil
			})
		},
	}
	prune.Flags().BoolVar(&dryRun, "dry-run", false, "only list the projects that would be removed")

	c.AddCommand(list, rm, prune)
	return c
}

func printProjects(w io.Writer, ps []project.Project) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "KEY\tBASE\tWORKTREE\tROOT\tLAST SCAN\tFILES")
	for _, p := range ps {
		last := "-"
		if !p.LastScanAt.IsZero() {
			last = p.LastScanAt.Local().Format(time.DateTime)
		}
		wt := p.Worktree
		if wt == "" {
			wt = "-"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\n", p.Key, p.Base, wt, p.Root, last, p.Files)
	}
	_ = tw.Flush()
}
