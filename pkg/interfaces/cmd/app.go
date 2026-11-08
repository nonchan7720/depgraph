package cmd

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/project"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/claudemd"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/config"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/neo4jprovider"
	graphrepo "github.com/nonchan7720/depgraph/pkg/infrastructure/repository/graph"
	projectrepo "github.com/nonchan7720/depgraph/pkg/infrastructure/repository/project"
	rulesrepo "github.com/nonchan7720/depgraph/pkg/infrastructure/repository/rules"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver/lsp"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver/treesitter"
	"github.com/nonchan7720/depgraph/pkg/services/analysis"
)

// app is the wiring shared by the Neo4j-backed commands. The graph and rules
// repositories are bound to the project of --root; the store is shared.
type app struct {
	store    *graphrepo.Store
	graph    *graphrepo.Graph
	projects project.Repository
	project  project.Project
	resolver *treesitter.Resolver
	svc      analysis.Service
	// neo4j is the Neo4j in use; release gives it back (see openStore).
	neo4j   neo4jprovider.Conn
	release func(context.Context)
}

// newApp connects to the Neo4j of --root (provided as policy allows), ensures
// the schema (removing data of the pre-project layout), resolves the project
// of --root and builds the analysis service over that project's graph. A key
// collision with another live root is an error before anything is written.
// The caller must call close.
func newApp(ctx context.Context, o *options, policy neo4jprovider.Policy, report analysis.ReportWriter, out io.Writer) (*app, error) {
	st, conn, release, err := openStore(ctx, o, policy, out)
	if err != nil {
		return nil, err
	}
	a, err := buildApp(ctx, st, o, report, out)
	if err != nil {
		release(ctx)
		return nil, err
	}
	a.neo4j, a.release = conn, release
	return a, nil
}

func buildApp(ctx context.Context, st *graphrepo.Store, o *options, report analysis.ReportWriter, out io.Writer) (*app, error) {
	// .depgraph.yml first: an invalid file fails before anything is written.
	settings, err := config.Load(o.root)
	if err != nil {
		return nil, err
	}
	printWarnings(out, settings)
	if err := st.EnsureSchema(ctx); err != nil {
		return nil, err
	}
	projects := projectrepo.New(st)
	p, stored, err := resolveProject(ctx, projects, o.root, o.project)
	if err != nil {
		return nil, err
	}
	if !stored {
		_, _ = fmt.Fprintf(out, "project %s (new)\n", p.Key)
	} else {
		_, _ = fmt.Fprintf(out, "project %s\n", p.Key)
	}
	return assembleApp(st, o, settings, p, o.root, report, out), nil
}

// assembleApp builds the app of project p whose files are read from root
// (o.root, or another copy of the same tree); settings are root's.
func assembleApp(st *graphrepo.Store, o *options, settings config.File, p project.Project, root string, report analysis.ReportWriter, out io.Writer) *app {
	g := graphrepo.ForProject(st, p.Key)
	r := treesitter.New()
	svc := analysis.New(analysis.Deps{
		Repo:     g,
		Rules:    rulesrepo.New(st, p.Key).WithRoot(root),
		Resolver: r,
		Scan:     resolver.ScanRepoWorkers(o.workers),
		Report:   report,
		Out:      out,
		Config:   &settings.Rules,
	}, analysis.Options{Root: root, Reset: o.reset})
	return &app{store: st, graph: g, projects: projectrepo.New(st), project: p, resolver: r, svc: svc}
}

// fullScan registers the project (so its data never exists without a Project
// node, and a moved root is recorded), runs the full scan and records
// lastScanAt. Registration lives here, not in the analysis service: the
// project is decided before the service is built and the service stays
// unaware of projects.
func (a *app) fullScan(ctx context.Context) error {
	if err := a.projects.Upsert(ctx, a.project); err != nil {
		return err
	}
	if err := a.svc.FullScan(ctx); err != nil {
		return err
	}
	a.project.LastScanAt = time.Now()
	return a.projects.Upsert(ctx, a.project)
}

// close releases the Neo4j driver and the provided Neo4j (an ephemeral
// container this process started is stopped); it works after ctx was
// cancelled.
func (a *app) close(ctx context.Context) {
	a.resolver.Close()
	a.release(ctx)
}

// reportWriter returns the CLAUDE.md writer for --claude-md, or nil when disabled.
// A relative path is taken relative to --root.
func reportWriter(o *options) analysis.ReportWriter {
	if o.claudeMD == "" {
		return nil
	}
	p := o.claudeMD
	if !filepath.IsAbs(p) {
		p = filepath.Join(o.root, p)
	}
	return claudemd.Writer{Path: p}
}

// newLSPManager returns a manager whose servers start lazily per language on
// the first changed file; a missing binary is reported once and disabled.
func newLSPManager(o *options, r *treesitter.Resolver, out io.Writer) *lsp.Manager {
	warn := func(format string, args ...any) { _, _ = fmt.Fprintf(out, format+"\n", args...) }
	symbols := func(ctx context.Context, rel string) ([]graph.Symbol, error) {
		if resolver.ShouldSkipPath(rel) || !r.Supports(rel) {
			return nil, nil
		}
		fg, err := r.ResolveFile(ctx, o.root, rel)
		if err != nil {
			return nil, err
		}
		return fg.Symbols, nil
	}
	return lsp.NewManager(o.root, symbols, warn)
}
