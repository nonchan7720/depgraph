package cmd

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/nonchan7720/depgraph/pkg/version"
)

// versionTemplate is what `depgraph --version` prints.
func versionTemplate() string {
	return fmt.Sprintf("depgraph {{.Version}} (commit %s, built %s)\n", version.Commit, version.Date)
}

func newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version, commit, build date and Go toolchain",
		Args:  cobra.NoArgs,
		// no repository is involved: skip the root's --root / ignore-file setup
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "depgraph %s\ncommit: %s\nbuilt:  %s\ngo:     %s %s/%s\n",
				version.Version, version.Commit, version.Date, runtime.Version(), runtime.GOOS, runtime.GOARCH)
			return err
		},
	}
}
