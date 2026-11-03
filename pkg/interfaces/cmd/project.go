package cmd

import (
	"context"
	"path/filepath"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/project"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/config"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/workspace"
	"github.com/nonchan7720/depgraph/pkg/interfaces/handler/hook"
)

// resolveProject decides which project root belongs to, with the rules every
// command shares (scan / watch / check / serve / hook): the key from
// --project, .depgraph.yml or the directory name plus the worktree suffix,
// then project.Reconcile against what is stored under that key. stored
// reports whether the key is already registered. Nothing is written.
func resolveProject(ctx context.Context, repo project.Repository, root, flagKey string) (p project.Project, stored bool, err error) {
	incoming, err := workspace.Identify(ctx, root, flagKey)
	if err != nil {
		return project.Project{}, false, err
	}
	existing, err := repo.Get(ctx, incoming.Key)
	if err != nil {
		return project.Project{}, false, err
	}
	p, err = project.Reconcile(existing, incoming, workspace.Exists)
	return p, existing != nil, err
}

// statsReader is what the hook reads from a project's graph.
type statsReader interface {
	Stats(ctx context.Context) (graph.Stats, error)
}

// projectData is the hook.ProjectData of a multi-project database: root's
// project is resolved like scan does, and only a registered project whose
// root matches (or was moved) has data. Read-only: a moved root is not
// written back, the next scan does that.
type projectData struct {
	projects project.Repository
	flagKey  string
	// scoped returns the graph / rules repositories bound to a project key.
	scoped func(key string) (statsReader, rules.Repository)
	// sessions returns the hook session state of a project (nil: none kept).
	sessions func(key string) hook.SessionStore
	// config returns the rule configuration of a root (nil: the defaults).
	config func(root string) (rules.Config, error)
}

// loadRules returns the rule configuration of root's .depgraph.yml.
func loadRules(root string) (rules.Config, error) {
	f, err := config.Load(root)
	return f.Rules, err
}

var _ hook.ProjectData = projectData{}

func (d projectData) Load(ctx context.Context, root string) (hook.Snapshot, bool, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return hook.Snapshot{}, false, err
	}
	p, stored, err := resolveProject(ctx, d.projects, abs, d.flagKey)
	if err != nil || !stored {
		return hook.Snapshot{}, false, err
	}
	cfg := rules.DefaultConfig()
	if d.config != nil {
		if cfg, err = d.config(abs); err != nil {
			return hook.Snapshot{}, false, err
		}
	}
	g, r := d.scoped(p.Key)
	findings, err := r.Evaluate(ctx, cfg)
	if err != nil {
		return hook.Snapshot{}, false, err
	}
	stats, err := g.Stats(ctx)
	if err != nil {
		return hook.Snapshot{}, false, err
	}
	snap := hook.Snapshot{Stats: stats, Findings: findings}
	if d.sessions != nil {
		snap.Sessions = d.sessions(p.Key)
	}
	return snap, true, nil
}
