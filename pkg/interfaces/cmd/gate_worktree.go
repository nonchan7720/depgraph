package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/project"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
	"github.com/nonchan7720/depgraph/pkg/domain/view"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/config"
	graphrepo "github.com/nonchan7720/depgraph/pkg/infrastructure/repository/graph"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/repository/hooksession"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/workspace"
)

// An agent that works in a linked git worktree (claude-hook-gate puts them
// under <project>/.claude/worktrees/<name>) runs the gate there while only
// the main tree is watched. The gate then checks the worktree as a project
// of its own (key <base>@<name>) in the main tree's Neo4j: it builds that
// project from the worktree's HEAD the first time (so what the agent changed
// before the first gate run still counts as added), and from then on brings
// it up to date itself on every run, from git (see syncWorktree). Nothing of
// it reaches the main tree's project, and nothing of the main tree's
// reaches the worktree's agent.

// gateRoot is the root the gate checks when --root is not given: the working
// tree of cwd when it is another worktree of projectDir's repository
// (claude-hook-gate runs a worktree's rules there while $CLAUDE_PROJECT_DIR
// stays the main tree), else projectDir, else cwd.
func gateRoot(ctx context.Context, projectDir, cwd string) string {
	if projectDir == "" {
		return cwd
	}
	here := workspace.Locate(ctx, cwd)
	if here.Top == "" {
		return projectDir
	}
	session := workspace.Locate(ctx, projectDir)
	if here.SameRepository(session) && !here.SameCheckout(session) {
		return here.Top
	}
	return projectDir
}

// syncState is what syncWorktree found: the files to apply to the graph and
// the state to record once they are applied.
type syncState struct {
	files []string
	head  string
	dirty []string
}

// syncWorktree returns what may differ between p's graph (built from
// p.Commit plus p.Dirty) and root as it is now: the files changed between
// p.Commit and HEAD, p.Dirty, and the files that differ from HEAD now. ok
// is false when that cannot be told (no recorded commit, or git no longer
// knows it).
func syncWorktree(ctx context.Context, root string, p project.Project) (syncState, bool, error) {
	head := workspace.Head(ctx, root)
	if p.Commit == "" || head == "" {
		return syncState{}, false, nil
	}
	moved, err := workspace.ChangedBetween(ctx, root, p.Commit, head)
	if err != nil {
		return syncState{}, false, nil // e.g. the commit was rewritten away
	}
	dirty, err := workspace.Dirty(ctx, root)
	if err != nil {
		return syncState{}, false, err
	}
	files := slices.Concat(moved, p.Dirty, dirty)
	slices.Sort(files)
	return syncState{files: slices.Compact(files), head: head, dirty: dirty}, true, nil
}

// bootstrapWorktree (re)builds p's graph from the HEAD of root (a linked
// worktree), records the HEAD as p's sync state and the resulting findings
// as session's baseline, and removes the projects of other worktrees of the
// same repository whose directory is gone. It returns the project as stored.
func bootstrapWorktree(ctx context.Context, st *graphrepo.Store, o *options, p project.Project, session string, now time.Time) (project.Project, error) {
	tmp, err := os.MkdirTemp("", "depgraph-head-")
	if err != nil {
		return project.Project{}, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	commit, err := workspace.ExportHead(ctx, o.root, tmp)
	if err != nil {
		return project.Project{}, err
	}
	settings, err := config.Load(tmp)
	if err != nil {
		return project.Project{}, err
	}
	head := *o
	head.reset = true // a stored graph is partial or unknown
	// LastScanAt stays zero until the scan finished: an interrupted
	// bootstrap is redone
	p.LastScanAt, p.Commit, p.Dirty = time.Time{}, commit, nil
	a := assembleApp(st, &head, settings, p, tmp, nil, io.Discard)
	defer a.resolver.Close()
	var findings []rules.Finding
	fresh := false
	a.svc.SetNotifier(notifyFunc(func(_ view.Stats, fs []rules.Finding) { findings, fresh = fs, true }))
	if err := a.fullScan(ctx); err != nil {
		return project.Project{}, err
	}
	if !fresh {
		if err := a.svc.Evaluate(ctx); err != nil {
			return project.Project{}, err
		}
	}
	if err := hooksession.New(st, p.Key).PutBaseline(ctx, session, findings, now); err != nil {
		return project.Project{}, err
	}
	pruneGoneWorktrees(ctx, a.projects, a.project)
	return a.project, nil
}

// pruneGoneWorktrees removes the projects of p's other worktrees whose
// directory no longer exists (the agent's worktree was removed). Failures
// are ignored: this is housekeeping, not the check.
func pruneGoneWorktrees(ctx context.Context, repo project.Repository, p project.Project) {
	all, err := repo.List(ctx)
	if err != nil {
		return
	}
	for _, q := range project.Stale(all, workspace.Exists) {
		if q.Key != p.Key && q.Base == p.Base && q.Worktree != "" {
			_ = repo.Delete(ctx, q.Key)
		}
	}
}

// gateFiles turns the gate's file arguments into root-relative slash paths;
// absolute paths outside root are dropped.
func gateFiles(root string, files []string) []string {
	var out []string
	for _, f := range files {
		if filepath.IsAbs(f) {
			rel, err := filepath.Rel(root, f)
			if err != nil || !filepath.IsLocal(rel) {
				continue
			}
			f = rel
		}
		out = append(out, filepath.ToSlash(filepath.Clean(f)))
	}
	return out
}

// describeWorktree is the skip reason when a worktree's project cannot be
// prepared.
func describeWorktree(root string, err error) error {
	return fmt.Errorf("worktree %s: %w", root, err)
}

// evalSymlinks resolves p's symlinks (p itself when that fails), for
// comparing it with workspace.Locate's paths.
func evalSymlinks(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}
