package cmd

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nonchan7720/depgraph/pkg/infrastructure/watch"
	"github.com/nonchan7720/depgraph/pkg/interfaces/handler/ui"
)

func newWatchCommand(o *options) *cobra.Command {
	c := &cobra.Command{
		Use:   "watch",
		Short: "Scan, then watch files and re-analyze incrementally (tree-sitter, then LSP)",
		Long: `Scan --root, then watch it for changes. Each change is re-parsed with
tree-sitter, rules are evaluated and the UI / CLAUDE.md are refreshed; then
language servers refine the same files with CALLS / REFERENCES edges in the
background (disable with --no-lsp).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWatch(cmd, o)
		},
	}
	fs := c.Flags()
	addNeo4jFlags(fs, o)
	addClaudeMDFlag(fs, o)
	addResetFlag(fs, o)
	addWorkersFlag(fs, o)
	addAddrFlag(fs, o)
	fs.DurationVar(&o.debounce, "debounce", 400*time.Millisecond, "coalesce changes within this window")
	fs.BoolVar(&o.noUI, "no-ui", false, "do not start the browser UI")
	fs.BoolVar(&o.noLSP, "no-lsp", false, "do not refine changed files with language servers (CALLS / REFERENCES edges)")
	return c
}

func runWatch(cmd *cobra.Command, o *options) error {
	ctx := cmd.Context()
	out := cmd.ErrOrStderr()
	a, err := newApp(ctx, o, reportWriter(o), out)
	if err != nil {
		return err
	}
	defer a.close(ctx)

	if !o.noUI {
		srv := ui.New(a.graph, o.root)
		a.svc.SetNotifier(srv)
		go func() {
			if err := srv.ListenAndServe(ctx, o.addr); err != nil {
				_, _ = fmt.Fprintf(out, "ui: %v\n", err)
			}
		}()
		_, _ = fmt.Fprintf(out, "ui: http://%s\n", o.addr)
	}
	if err := a.fullScan(ctx); err != nil {
		return err
	}
	if err := a.svc.Evaluate(ctx); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "watching %s (ctrl-c to stop)\n", o.root)

	// Stage 1 (every change): tree-sitter result is upserted, rules evaluated
	// and the UI refreshed. Stage 2 (background): language servers refine the
	// same files. The initial full scan above never involves LSP.
	if !o.noLSP {
		m := newLSPManager(o, a.resolver, out)
		wait := a.svc.StartRefinement(ctx, m)
		defer func() {
			// ctx is already cancelled on ctrl-c: wait for the refiner, then
			// stop the servers with a fresh, bounded context.
			wait()
			sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			m.Close(sctx)
		}()
	}

	watched := func(rel string) bool { return a.resolver.Supports(rel) || a.resolver.IsConfigFile(rel) }
	return watch.Run(ctx, o.root, watched, o.debounce, func(changed []string) {
		config, sources := splitChanges(changed, a.resolver.IsConfigFile, a.resolver.Supports)
		_, _ = fmt.Fprintf(out, "\n%s changed %d file(s)\n", time.Now().Format("15:04:05"), len(changed))
		if len(config) == 0 {
			if err := a.svc.HandleChanges(ctx, sources); err != nil {
				_, _ = fmt.Fprintf(out, "  error: %v\n", err)
			}
			return
		}
		// go.mod / tsconfig.json / pyproject.toml ... can change how every
		// import resolves: drop the cached configuration and rescan.
		for _, rel := range config {
			a.resolver.ConfigChanged(o.root, rel)
		}
		_, _ = fmt.Fprintf(out, "  %s changed: rescanning\n", strings.Join(config, ", "))
		if err := a.fullScan(ctx); err != nil {
			_, _ = fmt.Fprintf(out, "  error: %v\n", err)
			return
		}
		if err := a.svc.Evaluate(ctx); err != nil {
			_, _ = fmt.Fprintf(out, "  error: %v\n", err)
		}
	})
}

// splitChanges sorts a batch of changed paths into the files import
// resolution depends on and the source files to re-analyse (a file can be
// both, e.g. __init__.py).
func splitChanges(changed []string, isConfig, supports func(rel string) bool) (config, sources []string) {
	sort.Strings(changed)
	for _, rel := range changed {
		if isConfig(rel) {
			config = append(config, rel)
		}
		if supports(rel) {
			sources = append(sources, rel)
		}
	}
	return config, sources
}
