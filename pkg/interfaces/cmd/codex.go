package cmd

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/nonchan7720/depgraph/pkg/infrastructure/codexhooks"
)

func newCodexCommand() *cobra.Command {
	c := &cobra.Command{
		Use:   "codex",
		Short: "Codex plugin helpers",
		Args:  cobra.NoArgs,
		// no repository is scanned: skip the root's ignore-file setup
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
	}
	c.AddCommand(newCodexInstallHooksCommand())
	return c
}

func newCodexInstallHooksCommand() *cobra.Command {
	var pluginRoot, to string
	var repo, printOnly, remove bool
	c := &cobra.Command{
		Use:   "install-hooks",
		Short: "Copy the depgraph plugin's Codex hooks into a hooks.json Codex reads",
		Long: `Copies the plugin's Codex hooks (<plugin root>/codex/hooks.json, with
${PLUGIN_ROOT} replaced by the plugin root) into a hooks.json of a Codex config
layer, for Codex builds that install the plugin but do not run the hooks it
bundles. Other hooks in the file are kept; running it again replaces the
plugin's entries (also those of an older version) instead of adding more.
Codex asks to trust the hooks again (/hooks) after every change.

Target, one of:
  (default)  $CODEX_HOME/hooks.json (~/.codex/hooks.json)
  --repo     <--root>/.codex/hooks.json (runs only where Codex trusts the project)
  --to FILE  any hooks.json, e.g. /etc/codex/hooks.json with --print

The plugin's scripts/hook.sh stays quiet in the plugin layer while a copy is
installed, so the findings are never added twice.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if repo && to != "" {
				return errors.New("--repo and --to exclude each other")
			}
			if pluginRoot == "" {
				pluginRoot = firstNonEmpty(os.Getenv("PLUGIN_ROOT"), os.Getenv("CLAUDE_PLUGIN_ROOT"), ".")
			}
			target := to
			switch {
			case repo:
				root, err := cmd.Flags().GetString("root")
				if err != nil {
					return err
				}
				target = filepath.Join(root, ".codex", "hooks.json")
			case target == "" && !printOnly:
				home := os.Getenv("CODEX_HOME")
				if home == "" {
					userHome, err := os.UserHomeDir()
					if err != nil {
						return errors.New("neither CODEX_HOME nor HOME is set; pass --to <hooks.json>")
					}
					home = filepath.Join(userHome, ".codex")
				}
				target = filepath.Join(home, "hooks.json")
			}
			if target != "" {
				abs, err := filepath.Abs(target)
				if err != nil {
					return err
				}
				target = abs
			}
			if remove && printOnly && target == "" {
				return errors.New("--print --remove needs --to or --repo")
			}
			if err := codexhooks.Install(codexhooks.InstallOptions{Root: pluginRoot, Target: target, Print: printOnly, Remove: remove}, cmd.OutOrStdout()); err != nil {
				return err
			}
			if !printOnly {
				if remove {
					cmd.PrintErrf("depgraph: removed the depgraph hooks from %s\n", target)
				} else {
					cmd.PrintErrf("depgraph: installed the depgraph hooks into %s; trust them in Codex with /hooks\n", target)
				}
			}
			return nil
		},
	}
	c.Flags().StringVar(&pluginRoot, "plugin-root", "", "the plugin root (default: $PLUGIN_ROOT, $CLAUDE_PLUGIN_ROOT, else the current directory)")
	c.Flags().StringVar(&to, "to", "", "write into this hooks.json")
	c.Flags().BoolVar(&repo, "repo", false, "write into the repository's .codex/hooks.json (the repository is --root)")
	c.Flags().BoolVar(&printOnly, "print", false, "print the hooks.json instead of writing it (with --to / --repo: that file merged with the hooks)")
	c.Flags().BoolVar(&remove, "remove", false, "remove the plugin's hooks instead of adding them")
	return c
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
