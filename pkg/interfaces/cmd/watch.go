package cmd

import (
	"context"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/nonchan7720/depgraph/pkg/infrastructure/config"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/watch"
	"github.com/nonchan7720/depgraph/pkg/interfaces/handler/ui"
	"github.com/nonchan7720/depgraph/pkg/services/analysis"
)

func newWatchCommand(o *options) *cobra.Command {
	c := &cobra.Command{
		Use:   "watch",
		Short: "Scan, then watch files and re-analyze incrementally (tree-sitter, then LSP)",
		Long: `Scan --root, then watch it for changes. Each change is re-parsed with
tree-sitter, rules are evaluated and the UI / CLAUDE.md are refreshed; then
language servers refine the same files with CALLS / REFERENCES edges in the
background (disable with --no-lsp). A change of .depgraph.yml reloads the
rules (a change of its project key needs a restart).`,
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

	b := watchBatch{root: o.root, svc: a.svc, out: out}
	supports := func(rel string) bool { return rel == config.FileName || a.resolver.Supports(rel) }
	return watch.Run(ctx, o.root, supports, o.debounce, func(changed []string) {
		sort.Strings(changed)
		_, _ = fmt.Fprintf(out, "\n%s changed %d file(s)\n", time.Now().Format("15:04:05"), len(changed))
		b.handle(ctx, changed)
	})
}

// watchBatch applies one debounced batch of changed paths.
type watchBatch struct {
	root string
	svc  analysis.Service
	out  io.Writer
}

// handle reloads the rules when .depgraph.yml changed (an invalid file keeps
// the previous rules) and hands source files to the service, which evaluates.
// A batch with only the settings file is evaluated here.
func (b watchBatch) handle(ctx context.Context, changed []string) {
	files := make([]string, 0, len(changed))
	reloaded := false
	for _, rel := range changed {
		if rel != config.FileName {
			files = append(files, rel)
			continue
		}
		f, err := config.Load(b.root)
		if err != nil {
			_, _ = fmt.Fprintf(b.out, "  %v (keeping the previous rules)\n", err)
			continue
		}
		b.svc.SetConfig(f.Rules)
		reloaded = true
		_, _ = fmt.Fprintf(b.out, "  %s: rules reloaded\n", config.FileName)
	}
	var err error
	switch {
	case len(files) > 0:
		err = b.svc.HandleChanges(ctx, files)
	case reloaded:
		err = b.svc.Evaluate(ctx)
	}
	if err != nil {
		_, _ = fmt.Fprintf(b.out, "  error: %v\n", err)
	}
}
