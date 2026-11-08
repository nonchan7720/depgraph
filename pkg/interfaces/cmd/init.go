package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/config"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver/imports"
	"github.com/nonchan7720/depgraph/pkg/internal/safefile"
)

// stdinIsTerminal reports whether in is an interactive terminal (a person
// can answer); tests replace it.
var stdinIsTerminal = func(in any) bool {
	f, ok := in.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func newInitCommand(o *options) *cobra.Command {
	var dryRun bool
	c := &cobra.Command{
		Use:   "init",
		Short: "Write languages: settings to .depgraph.yml from the files that pin each part's version",
		Long: `Find, for every directory with Python files, the files that pin its Python
version (.python-version, .tool-versions, mise.toml, pyproject.toml's type
checker settings, pyrightconfig.json, mypy.ini, setup.cfg, a Dockerfile) and
add a languages.python setting naming that file to .depgraph.yml, the rest
of which is kept. depgraph never chooses between files that pin different
versions: on a terminal init asks, otherwise it leaves that directory out
and says so. Settings already present are kept. Nothing is executed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runInitCommand(cmd, o.root, dryRun)
		},
	}
	c.Flags().BoolVar(&dryRun, "dry-run", false, "print the resulting .depgraph.yml instead of writing it")
	return c
}

func runInitCommand(cmd *cobra.Command, root string, dryRun bool) error {
	out := cmd.OutOrStdout()
	settings, err := config.Load(root)
	if err != nil {
		return err
	}
	plan := imports.PlanPythonVersions(root, settings.Rules)
	entries := make([]config.LanguageEntry, 0, len(plan.Entries))
	for _, e := range plan.Entries {
		entries = append(entries, config.LanguageEntry{Dir: e.Dir, Lang: "python", VersionFile: e.VersionFile})
	}
	interactive := stdinIsTerminal(cmd.InOrStdin())
	in := bufio.NewReader(cmd.InOrStdin())
	for _, u := range plan.Undecided {
		desc := make([]string, len(u.Candidates))
		for i, c := range u.Candidates {
			desc[i] = c.File + " pins " + c.Version
		}
		_, _ = fmt.Fprintf(out, "%s: %s\n", u.Dir, strings.Join(desc, ", "))
		if !interactive {
			_, _ = fmt.Fprintf(out, "  left out: these files disagree and depgraph does not choose (run init on a terminal, or set languages.python of %s)\n", u.Dir)
			continue
		}
		file, err := choose(out, in, u)
		if err != nil {
			return err
		}
		if file == "" {
			_, _ = fmt.Fprintf(out, "  left out\n")
			continue
		}
		entries = append(entries, config.LanguageEntry{Dir: u.Dir, Lang: "python", VersionFile: file})
	}
	for _, n := range plan.Notes {
		_, _ = fmt.Fprintf(out, "note: %s\n", n)
	}
	if len(entries) == 0 {
		_, _ = fmt.Fprintln(out, "nothing to add")
		return nil
	}
	for _, e := range entries {
		_, _ = fmt.Fprintf(out, "languages.python of %s: versionFile %s\n", e.Dir, e.VersionFile)
		s := rules.LanguageSetting{Dir: e.Dir, Lang: "python", VersionFile: e.VersionFile}
		if _, err := imports.LoadPythonStdlib(root, s); err != nil {
			_, _ = fmt.Fprintf(out, "  %v: its imports stay unclassified until there is one\n", err)
		}
	}
	old, err := safefile.ReadManifest(root, config.FileName)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	updated, err := config.AddLanguageSettings(old, entries)
	if err != nil {
		return fmt.Errorf("%s: %w", config.FileName, err)
	}
	if dryRun {
		_, _ = fmt.Fprintf(out, "--- %s\n%s", config.FileName, updated)
		return nil
	}
	p := filepath.Join(root, config.FileName)
	if st, err := os.Lstat(p); err == nil && !st.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file (a symlink?): not written; add the settings above by hand", config.FileName)
	}
	if err := os.WriteFile(p, updated, 0o644); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "wrote %s\n", config.FileName)
	return nil
}

// choose asks which file of u pins its version ("" to leave it out).
func choose(out io.Writer, in *bufio.Reader, u imports.PlanUndecided) (string, error) {
	for {
		for i, c := range u.Candidates {
			_, _ = fmt.Fprintf(out, "  %d) %s (%s)\n", i+1, c.File, c.Version)
		}
		_, _ = fmt.Fprintf(out, "  which file pins the version %s runs? [1-%d, empty to leave it out] ", u.Dir, len(u.Candidates))
		line, err := in.ReadString('\n')
		answer := strings.TrimSpace(line)
		if answer == "" {
			if err != nil && !errors.Is(err, io.EOF) {
				return "", err
			}
			return "", nil
		}
		if n, convErr := strconv.Atoi(answer); convErr == nil && n >= 1 && n <= len(u.Candidates) {
			return u.Candidates[n-1].File, nil
		}
		if err != nil {
			return "", nil
		}
		_, _ = fmt.Fprintln(out, "  not one of the numbers")
	}
}
