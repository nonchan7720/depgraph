package cmd

import (
	"fmt"
	"io"
	"strconv"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/nonchan7720/depgraph/pkg/infrastructure/config"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/neo4jprovider/container"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/workspace"
)

// newStoreCommand manages the Neo4j containers and data volumes depgraph
// created (container provider). Only objects labelled depgraph.managed=1
// are listed or touched.
func newStoreCommand(o *options) *cobra.Command {
	c := &cobra.Command{
		Use:   "store",
		Short: "List, stop and delete the Neo4j containers and data volumes depgraph manages",
		Long: `The container provider runs Neo4j in containers labelled depgraph.managed=1
and keeps persistent data in labelled volumes (one per project key). These
commands list and remove exactly those; nothing else is touched. An external
Neo4j (--neo4j / NEO4J_URI) is never managed.`,
		Args: cobra.NoArgs,
	}
	c.PersistentFlags().StringVar(&o.containerCLI, "container-cli", env("DEPGRAPH_CONTAINER_CLI", ""), "container CLI: docker, podman, nerdctl or a path (default: store.containerCli in .depgraph.yml, else the first that answers; $DEPGRAPH_CONTAINER_CLI)")

	provider := func(cmd *cobra.Command) (*container.Provider, error) {
		settings, err := config.Load(o.root)
		if err != nil {
			return nil, err
		}
		p := o.containerProvider(o.storeChoice(settings.Store).cli)
		if err := p.Available(cmd.Context()); err != nil {
			return nil, err
		}
		return p, nil
	}

	ls := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List managed Neo4j containers and data volumes",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := provider(cmd)
			if err != nil {
				return err
			}
			insts, vols, err := p.List(cmd.Context())
			if err != nil {
				return err
			}
			printStore(cmd.OutOrStdout(), insts, vols)
			return nil
		},
	}

	down := &cobra.Command{
		Use:   "down",
		Short: "Stop the Neo4j of --root (ephemeral: removed with its data; persistent: stopped, data kept)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := provider(cmd)
			if err != nil {
				return err
			}
			insts, err := p.Down(cmd.Context(), o.root)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if len(insts) == 0 {
				_, _ = fmt.Fprintf(out, "no managed Neo4j for %s\n", o.root)
			}
			for _, i := range insts {
				if i.Persist {
					_, _ = fmt.Fprintf(out, "stopped %s (data kept in volume %s; delete with `depgraph store rm`)\n", i.Name, i.Volume)
				} else {
					_, _ = fmt.Fprintf(out, "removed %s (ephemeral)\n", i.Name)
				}
			}
			return nil
		},
	}

	var all bool
	rm := &cobra.Command{
		Use:   "rm [project-key...]",
		Short: "Delete Neo4j containers and data volumes: of --root's project, of the given project keys, or --all",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := provider(cmd)
			if err != nil {
				return err
			}
			sel := container.Selection{All: all, Projects: args}
			if !all && len(args) == 0 {
				id, err := workspace.Identify(cmd.Context(), o.root, o.project)
				if err != nil {
					return err
				}
				sel.Root, sel.Projects = o.root, []string{id.Key}
			}
			cs, vs, err := p.Remove(cmd.Context(), sel)
			out := cmd.OutOrStdout()
			for _, n := range cs {
				_, _ = fmt.Fprintf(out, "removed container %s\n", n)
			}
			for _, n := range vs {
				_, _ = fmt.Fprintf(out, "removed volume %s\n", n)
			}
			if err == nil && len(cs)+len(vs) == 0 {
				_, _ = fmt.Fprintln(out, "nothing to remove (see `depgraph store ls`)")
			}
			return err
		},
	}
	rm.Flags().BoolVar(&all, "all", false, "every managed container and volume")

	c.AddCommand(ls, down, rm)
	return c
}

// newContainerWatchdogCommand is the hidden child depgraph starts for every
// container it starts (see container.SpawnWatchdog): it waits for depgraph to
// go away, however that happens, and then stops / removes that container.
func newContainerWatchdogCommand() *cobra.Command {
	return &cobra.Command{
		Use:                container.WatchdogCommand,
		Hidden:             true,
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		PersistentPreRunE:  func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, args []string) error {
			return container.WatchdogMain(args, cmd.InOrStdin(), container.ExecRunner{})
		},
	}
}

func printStore(w io.Writer, insts []container.Instance, vols []container.Volume) {
	if len(insts) == 0 && len(vols) == 0 {
		_, _ = fmt.Fprintln(w, "no managed Neo4j containers or volumes")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if len(insts) > 0 {
		_, _ = fmt.Fprintln(tw, "CONTAINER\tPROJECT\tMODE\tSTATE\tBOLT\tROOT")
		for _, i := range insts {
			mode, bolt := "ephemeral", "-"
			if i.Persist {
				mode = "persistent"
			}
			if i.Running() && i.Port > 0 {
				bolt = "127.0.0.1:" + strconv.Itoa(i.Port)
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", i.Name, i.Project, mode, i.State, bolt, i.RootPath)
		}
	}
	if len(vols) > 0 {
		if len(insts) > 0 {
			_, _ = fmt.Fprintln(tw, "\t\t\t\t\t")
		}
		_, _ = fmt.Fprintln(tw, "VOLUME\tPROJECT\tROOT")
		for _, v := range vols {
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", v.Name, v.Project, v.RootPath)
		}
	}
	_ = tw.Flush()
}
