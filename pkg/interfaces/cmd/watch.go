package cmd

import (
	"context"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/nonchan7720/depgraph/pkg/infrastructure/config"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/neo4jprovider"
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
rules (a change of its project key needs a restart).

Without --neo4j / NEO4J_URI, watch starts Neo4j itself (container provider:
docker, podman or nerdctl; 10-20 s) and owns it: when watch exits, for any
reason, an ephemeral store is removed with its data; with --persist /
store.persist only the data persists (named volume): the container is
stopped and the next watch / scan / check starts it again
(depgraph store rm deletes it).`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runWatch(cmd, o)
		},
	}
	fs := c.Flags()
	addNeo4jFlags(fs, o)
	addPersistFlag(fs, o)
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
	a, err := newApp(ctx, o, neo4jprovider.Start, reportWriter(o), out)
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

	b := watchBatch{
		root: o.root, svc: a.svc, out: out,
		isImportConfig: a.resolver.IsConfigFile,
		supports:       a.resolver.Supports,
		importConfigChanged: func(rel string) {
			a.resolver.ConfigChanged(o.root, rel)
		},
		rescan: a.fullScan,
	}
	watched := func(rel string) bool {
		return watchedConfig(rel) || a.resolver.Supports(rel) || a.resolver.IsConfigFile(rel)
	}
	return watch.Run(ctx, o.root, watched, o.debounce, func(changed []string) {
		sort.Strings(changed)
		_, _ = fmt.Fprintf(out, "\n%s changed %d file(s)\n", time.Now().Format("15:04:05"), len(changed))
		b.handle(ctx, changed)
	})
}

// watchedConfig reports whether rel is a .depgraph.yml: the root's or one
// of a directory below it (a component of a monorepo).
func watchedConfig(rel string) bool { return path.Base(filepath.ToSlash(rel)) == config.FileName }

// printWarnings prints the settings of f that are not applied.
func printWarnings(out io.Writer, f config.File) {
	for _, w := range f.Warnings {
		_, _ = fmt.Fprintf(out, "  warning: %s\n", w)
	}
}

// watchBatch applies one debounced batch of changed paths.
type watchBatch struct {
	root string
	svc  analysis.Service
	out  io.Writer
	// Import resolution: files it depends on (go.mod, tsconfig.json, ...),
	// the source files to re-analyse, dropping the cached configuration and
	// a full rescan. nil: no file is an import config, every file is a source.
	isImportConfig      func(rel string) bool
	supports            func(rel string) bool
	importConfigChanged func(rel string)
	rescan              func(ctx context.Context) error
}

// handle reloads the rules when .depgraph.yml changed (an invalid file keeps
// the previous rules). When a file import resolution depends on changed, the
// cached configuration is dropped and the repository rescanned (every import
// may resolve differently); otherwise source files go to the service, which
// evaluates. A batch with only the settings file is evaluated here.
func (b watchBatch) handle(ctx context.Context, changed []string) {
	files := make([]string, 0, len(changed))
	reloaded := false
	var settings []string
	for _, rel := range changed {
		switch {
		case watchedConfig(rel):
			settings = append(settings, rel)
		case config.IsModuleManifest(path.Base(filepath.ToSlash(rel))):
			// a module added or removed: module-cycle's units change, and
			// import resolution too (it stays in files)
			settings = append(settings, rel)
			files = append(files, rel)
		default:
			files = append(files, rel)
		}
	}
	if len(settings) > 0 {
		// the root's file and the nested ones make one configuration
		f, err := config.Load(b.root)
		if err != nil {
			_, _ = fmt.Fprintf(b.out, "  %v (keeping the previous rules)\n", err)
		} else {
			b.svc.SetConfig(f.Rules)
			reloaded = true
			printWarnings(b.out, f)
			_, _ = fmt.Fprintf(b.out, "  %s: rules reloaded\n", strings.Join(settings, ", "))
		}
	}
	isConfig, supports := b.isImportConfig, b.supports
	if isConfig == nil {
		isConfig = func(string) bool { return false }
	}
	if supports == nil {
		supports = func(string) bool { return true }
	}
	importConfig, sources := splitChanges(files, isConfig, supports)
	var err error
	switch {
	case len(importConfig) > 0 && b.rescan != nil:
		for _, rel := range importConfig {
			if b.importConfigChanged != nil {
				b.importConfigChanged(rel)
			}
		}
		_, _ = fmt.Fprintf(b.out, "  %s changed: rescanning\n", strings.Join(importConfig, ", "))
		if err = b.rescan(ctx); err == nil {
			err = b.svc.Evaluate(ctx)
		}
	case len(sources) > 0:
		err = b.svc.HandleChanges(ctx, sources)
	case reloaded:
		err = b.svc.Evaluate(ctx)
	}
	if err != nil {
		_, _ = fmt.Fprintf(b.out, "  error: %v\n", err)
	}
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
