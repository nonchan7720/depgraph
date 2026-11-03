package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/nonchan7720/depgraph/pkg/infrastructure/config"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/neo4jprovider"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/neo4jprovider/container"
	graphrepo "github.com/nonchan7720/depgraph/pkg/infrastructure/repository/graph"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/workspace"
)

// localPlanned is the reason the local provider (a Neo4j installation run as
// a child process) is listed but not available yet.
const localPlanned = "planned, not implemented yet (will run a Neo4j installation from NEO4J_HOME or PATH)"

// storeChoice is how Neo4j is provided for a root: flags, then environment
// (flag defaults), then the store: section of .depgraph.yml.
type storeChoice struct {
	provider string // "" lets Select choose
	persist  bool
	cli      string
}

func (o *options) storeChoice(s config.StoreSettings) storeChoice {
	c := storeChoice{provider: o.neo4jProvider, cli: o.containerCLI}
	if c.provider == "" {
		c.provider = s.Provider
	}
	if c.cli == "" {
		c.cli = s.ContainerCLI
	}
	switch {
	case o.persistSet:
		c.persist = o.persist
	case s.Persist != nil:
		c.persist = *s.Persist
	}
	return c
}

// containerProvider returns the container provider for the chosen CLI.
func (o *options) containerProvider(cli string) *container.Provider {
	cfg := o.containerConfig
	cfg.CLI = cli
	return container.New(cfg)
}

// providers lists the Neo4j providers in selection order.
func (o *options) providers(c storeChoice) []neo4jprovider.Provider {
	ext := neo4jprovider.NewExternal(o.neo4jURI, o.neo4jUser, o.neo4jPass, o.neo4jDB)
	if c.provider == neo4jprovider.External {
		ext = ext.WithDefaultURI()
	}
	return []neo4jprovider.Provider{
		ext,
		o.containerProvider(c.cli),
		neo4jprovider.Planned(neo4jprovider.Local, localPlanned),
	}
}

// boltReady reports whether Neo4j at c accepts connections.
func boltReady(ctx context.Context, c neo4jprovider.Conn) error {
	st, err := graphrepo.New(ctx, c.URI, c.User, c.Password, c.Database)
	if err != nil {
		return err
	}
	return st.Close(ctx)
}

// openStore connects to the Neo4j of o.root, provided as the policy allows
// (see neo4jprovider.Policy). release closes the connection and gives the
// provider's resources back (an ephemeral container this process started is
// stopped); it works after ctx was cancelled.
func openStore(ctx context.Context, o *options, policy neo4jprovider.Policy, out io.Writer) (*graphrepo.Store, neo4jprovider.Conn, func(context.Context), error) {
	settings, err := config.Load(o.root)
	if err != nil {
		return nil, neo4jprovider.Conn{}, nil, err
	}
	choice := o.storeChoice(settings.Store)
	p, err := neo4jprovider.Select(ctx, choice.provider, o.providers(choice))
	if err != nil {
		return nil, neo4jprovider.Conn{}, nil, err
	}
	opts := neo4jprovider.StoreRequest{Root: o.root, Persist: choice.persist, Policy: policy, Ready: boltReady, Log: out}
	if p.Name() != neo4jprovider.External {
		id, err := workspace.Identify(ctx, o.root, o.project)
		if err != nil {
			return nil, neo4jprovider.Conn{}, nil, err
		}
		opts.Project = id.Key
	}
	conn, err := p.Provide(ctx, opts)
	if errors.Is(err, neo4jprovider.ErrNotRunning) {
		return nil, neo4jprovider.Conn{}, nil, fmt.Errorf("no running Neo4j for %s: start one with `depgraph watch` (or keep one with --persist / store.persist in %s)", o.root, config.FileName)
	}
	if err != nil {
		return nil, neo4jprovider.Conn{}, nil, err
	}
	giveBack := func(ctx context.Context) {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if conn.Started && !conn.Persistent {
			_, _ = fmt.Fprintf(out, "stopping Neo4j %s\n", conn.Describe)
		}
		if err := conn.Release(rctx); err != nil {
			_, _ = fmt.Fprintf(out, "Neo4j %s: %v\n", conn.Describe, err)
		}
	}
	st, err := graphrepo.New(ctx, conn.URI, conn.User, conn.Password, conn.Database)
	if err != nil {
		giveBack(ctx)
		return nil, neo4jprovider.Conn{}, nil, err
	}
	release := func(ctx context.Context) {
		_ = st.Close(context.WithoutCancel(ctx))
		giveBack(ctx)
	}
	return st, conn, release, nil
}

// hookStore connects to the Neo4j already serving root, read-only: nothing
// is started and nothing is printed (the hook reports errors only with
// --debug).
func hookStore(ctx context.Context, o *options, root string) (*graphrepo.Store, error) {
	settings, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	choice := o.storeChoice(settings.Store)
	p, err := neo4jprovider.Select(ctx, choice.provider, o.providers(choice))
	if err != nil {
		return nil, err
	}
	c, ok, err := p.Discover(ctx, root)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("no running Neo4j for %s (%s)", root, p.Name())
	}
	return graphrepo.New(ctx, c.URI, c.User, c.Password, c.Database)
}
