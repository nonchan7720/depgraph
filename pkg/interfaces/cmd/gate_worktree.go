package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"github.com/nonchan7720/depgraph/pkg/domain/project"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
	"github.com/nonchan7720/depgraph/pkg/domain/view"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/config"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/neo4jprovider/container"
	graphrepo "github.com/nonchan7720/depgraph/pkg/infrastructure/repository/graph"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/repository/hooksession"
	projectrepo "github.com/nonchan7720/depgraph/pkg/infrastructure/repository/project"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver/treesitter"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/workspace"
)

// An agent that works in a linked git worktree (claude-hook-gate puts them
// under <project>/.claude/worktrees/<name>) runs the gate there while only
// the main tree is watched. The gate then checks the worktree as a project
// of its own (key <base>@<name>) in the main tree's Neo4j, and keeps that
// project's graph up to date itself:
//
//   - the graph is built from the worktree's HEAD (its exact committed tree)
//     and those findings become the session's baseline, so what the agent
//     changed before the first gate run still counts as added;
//   - every run then applies what git says may differ from the graph: files
//     changed between the recorded commit and HEAD, and dirty files whose
//     content differs from what was applied last (Project.Commit / Dirty).
//     Findings that arrive through commits the session did not make (a merge
//     or rebase of upstream work) are added to the baselines of the
//     project's sessions: they are not the agent's debt;
//   - work that can take long (the first build, a delta with a file import
//     resolution depends on, or a large delta) runs in a background child
//     (depgraph __gate-sync) under a lease on the project, so a large
//     repository is never stuck behind the gate's timeout. The gate waits for
//     it as long as its own --timeout allows, and passes (saying so) when it
//     is not done yet.
//
// Nothing of it reaches the main tree's project, and nothing of the main
// tree's reaches the worktree's agent.

// gateSyncCommand is the hidden command of the background sync child.
const gateSyncCommand = "__gate-sync"

const (
	// syncLease bounds a background sync; a lease older than this is taken over.
	syncLease = 30 * time.Minute
	// maxInlineSync is the largest delta the gate applies itself.
	maxInlineSync = 200
)

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

// worktreeGate is the gate's view of a linked worktree's project.
type worktreeGate struct {
	o        *options
	st       *graphrepo.Store
	projects *projectrepo.Projects
	session  string
	// isConfig reports a file import resolution depends on.
	isConfig func(rel string) bool
}

// syncPlan is what brings the graph of the worktree's project up to date.
type syncPlan struct {
	upstream []string // changed by commits the session did not make: applied first, their findings absorbed
	own      []string // changed by the session (or not attributable): applied by the gate check
	head     string
	dirty    []project.DirtyFile // to record once everything is applied
}

// heavy reports whether p is too much for the gate's own deadline.
func (p syncPlan) heavy(isConfig func(string) bool) bool {
	if len(p.upstream)+len(p.own) > maxInlineSync {
		return true
	}
	return slices.ContainsFunc(p.upstream, isConfig) || slices.ContainsFunc(p.own, isConfig)
}

// plan compares p's recorded sync state with the worktree; ok is false when
// the graph cannot be brought up to date from it (no recorded commit, or
// git no longer knows it): the graph must be rebuilt.
func (w *worktreeGate) plan(ctx context.Context, p project.Project) (syncPlan, bool, error) {
	root := w.o.root
	head := workspace.Head(ctx, root)
	if p.Commit == "" || head == "" {
		return syncPlan{}, false, nil
	}
	moved, err := workspace.ChangedBetween(ctx, root, p.Commit, head)
	if err != nil {
		return syncPlan{}, false, nil // e.g. the commit was rewritten away
	}
	dirtyNow, err := workspace.Dirty(ctx, root)
	if err != nil {
		return syncPlan{}, false, err
	}
	applied := map[string]string{} // path -> hash applied last time
	for _, d := range p.Dirty {
		applied[d.Path] = d.Hash
	}
	plan := syncPlan{head: head}
	own := map[string]bool{}
	for _, rel := range dirtyNow {
		h := workspace.FileHash(root, rel)
		plan.dirty = append(plan.dirty, project.DirtyFile{Path: rel, Hash: h})
		if applied[rel] != h {
			own[rel] = true
		}
	}
	for rel, h := range applied { // no longer dirty: committed as applied, or reverted
		if !slices.Contains(dirtyNow, rel) && workspace.FileHash(root, rel) != h {
			own[rel] = true
		}
	}
	for _, rel := range moved {
		if own[rel] || slices.Contains(dirtyNow, rel) {
			continue
		}
		if h, ok := applied[rel]; ok && workspace.FileHash(root, rel) == h {
			continue // the session's own change, committed as applied
		}
		plan.upstream = append(plan.upstream, rel)
	}
	for rel := range own {
		plan.own = append(plan.own, rel)
	}
	slices.Sort(plan.own)
	return plan, true, nil
}

// prepare makes sure the worktree's project p has a graph and returns the
// app over it and the plan that brings it up to date, with the upstream
// part already applied and absorbed. live: the store was found for the
// worktree itself (its own watch keeps the graph current).
func (w *worktreeGate) prepare(ctx context.Context, p project.Project, stored, live bool) (*app, syncPlan, error) {
	if stored && !p.LastScanAt.IsZero() && p.Commit == "" && live {
		p.Commit = workspace.Head(ctx, w.o.root)
	}
	build := !stored || p.LastScanAt.IsZero() || p.Commit == ""
	var plan syncPlan
	if !build {
		var ok bool
		var err error
		if plan, ok, err = w.plan(ctx, p); err != nil {
			return nil, syncPlan{}, err
		}
		build = !ok || plan.heavy(w.isConfig)
	}
	if build {
		if err := w.background(ctx, p); err != nil {
			return nil, syncPlan{}, err
		}
		q, err := w.projects.Get(ctx, p.Key)
		if err != nil || q == nil || q.LastScanAt.IsZero() {
			return nil, syncPlan{}, fmt.Errorf("%w: the graph of %s was not built (%v)", errGateSkip, w.o.root, err)
		}
		p = *q
		ok := false
		if plan, ok, err = w.plan(ctx, p); err != nil || !ok {
			return nil, syncPlan{}, fmt.Errorf("%w: cannot compare %s with its graph (%v)", errGateSkip, w.o.root, err)
		}
	}
	a, err := buildApp(ctx, w.st, w.o, nil, io.Discard)
	if err != nil {
		return nil, syncPlan{}, err
	}
	a.project.Commit = p.Commit
	if len(plan.upstream) > 0 {
		if err := absorbUpstream(ctx, a, hooksession.New(w.st, a.project.Key), plan.upstream); err != nil {
			a.resolver.Close()
			return nil, syncPlan{}, err
		}
	}
	return a, plan, nil
}

// absorbUpstream applies files changed by commits the sessions did not make
// and adds the findings they bring to every session's baseline.
func absorbUpstream(ctx context.Context, a *app, sessions *hooksession.Sessions, files []string) error {
	before, err := evaluateNow(ctx, a)
	if err != nil {
		return err
	}
	var sources []string
	for _, rel := range files {
		if !resolver.ShouldSkipPath(rel) {
			sources = append(sources, rel)
		}
	}
	if err := a.svc.HandleChanges(ctx, sources); err != nil {
		return err
	}
	after, err := evaluateNow(ctx, a)
	if err != nil {
		return err
	}
	added, _ := rules.Diff(before, after)
	return sessions.Absorb(ctx, added, time.Now())
}

// evaluateNow evaluates the rules on a's graph and returns the findings.
func evaluateNow(ctx context.Context, a *app) ([]rules.Finding, error) {
	var got []rules.Finding
	a.svc.SetNotifier(notifyFunc(func(_ view.Stats, fs []rules.Finding) { got = fs }))
	defer a.svc.SetNotifier(nil)
	if err := a.svc.Evaluate(ctx); err != nil {
		return nil, err
	}
	return got, nil
}

// background runs the sync of p in a detached child (depgraph __gate-sync)
// unless another process holds p's lease, and waits for it until ctx is
// about to expire. When it is still running then, the error wraps
// errGateSkip.
func (w *worktreeGate) background(ctx context.Context, p project.Project) error {
	held, err := w.projects.SyncHeld(ctx, p.Key, time.Now())
	if err != nil {
		return err
	}
	var exited chan error
	logPath := filepath.Join(os.TempDir(), "depgraph-gate-sync-"+workspace.ShortHash(p.Key)+".log")
	if !held {
		if exited, err = w.spawn(logPath); err != nil {
			return err
		}
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(time.Minute)
	}
	wait := time.NewTimer(time.Until(deadline.Add(-time.Second)))
	defer wait.Stop()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-exited:
			if err != nil {
				b, _ := os.ReadFile(logPath)
				return fmt.Errorf("building the graph of %s: %v: %s", w.o.root, err, bytes.TrimSpace(b))
			}
			return nil
		case <-tick.C:
			if exited != nil {
				continue
			}
			if held, err := w.projects.SyncHeld(ctx, p.Key, time.Now()); err == nil && !held {
				return nil
			}
		case <-wait.C:
			return fmt.Errorf("%w: the graph of %s is being built in the background; the gate checks once it is done", errGateSkip, w.o.root)
		case <-ctx.Done():
			return fmt.Errorf("%w: %v", errGateSkip, ctx.Err())
		}
	}
}

// spawn starts the sync child, detached so that it outlives this process
// (claude-hook-gate kills the gate at its timeout), and returns a channel
// that receives its exit.
func (w *worktreeGate) spawn(logPath string) (chan error, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	log, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, w.o.syncArgs(w.session)...)
	cmd.Stdout, cmd.Stderr = log, log
	cmd.Env = append(os.Environ(), "NEO4J_PASSWORD="+w.o.neo4jPass) // never on the command line
	cmd.SysProcAttr = container.DetachedProcAttr()
	if err := cmd.Start(); err != nil {
		_ = log.Close()
		return nil, err
	}
	exited := make(chan error, 1)
	go func() {
		exited <- cmd.Wait()
		_ = log.Close()
	}()
	return exited, nil
}

// syncArgs is the argument list of the sync child for o's root.
func (o *options) syncArgs(session string) []string {
	args := []string{gateSyncCommand, "--root", o.root, "--session", session,
		"--neo4j", o.neo4jURI, "--user", o.neo4jUser, "--db", o.neo4jDB,
		"--neo4j-provider", o.neo4jProvider, "--container-cli", o.containerCLI,
		"--ignore-file", o.ignoreFile}
	if o.project != "" {
		args = append(args, "--project", o.project)
	}
	if o.exclude != "" {
		args = append(args, "--exclude", o.exclude)
	}
	return args
}

// newGateSyncCommand is the background sync child of the gate.
func newGateSyncCommand(o *options) *cobra.Command {
	var session string
	c := &cobra.Command{
		Use:    gateSyncCommand,
		Short:  "internal: build or bring up to date the graph of a linked worktree for depgraph gate",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := context.WithTimeout(cmd.Context(), syncLease)
			defer cancel()
			return runGateSync(ctx, o, session)
		},
	}
	addNeo4jFlags(c.Flags(), o)
	c.Flags().StringVar(&session, "session", "", "the session whose baseline the build records")
	return c
}

// runGateSync rebuilds the graph of the linked worktree o.root from its HEAD
// under the project's lease: the findings the old graph lacked (they came
// with commits) are absorbed into the sessions' baselines, HEAD's findings
// become session's baseline (unless it has one), then the dirty files are
// applied and the sync state recorded. Projects of removed worktrees of the
// same main tree are deleted.
func runGateSync(ctx context.Context, o *options, session string) error {
	co := workspace.Locate(ctx, o.root)
	if !co.Linked {
		return fmt.Errorf("%s is not a linked git worktree", o.root)
	}
	st, _, err := hookStoreFor(ctx, o, o.root, co.Main)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close(context.WithoutCancel(ctx)) }()
	if err := st.EnsureSchema(ctx); err != nil {
		return err
	}
	projects := projectrepo.New(st)
	p, _, err := resolveProject(ctx, projects, o.root, o.project)
	if err != nil {
		return err
	}
	now := time.Now()
	if ok, err := projects.ClaimSync(ctx, p, now, now.Add(syncLease)); err != nil || !ok {
		return err // another sync runs
	}
	defer func() { _ = projects.ReleaseSync(context.WithoutCancel(ctx), p.Key) }()
	sessions := hooksession.New(st, p.Key)
	settings := settingsOrDefault(o.root)

	var before []rules.Finding // the old graph's findings
	if !p.LastScanAt.IsZero() {
		a := assembleApp(st, o, settings, p, o.root, nil, io.Discard)
		before, err = evaluateNow(ctx, a)
		a.resolver.Close()
		if err != nil {
			return err
		}
	}
	head, err := buildFromHead(ctx, st, o, p, settings)
	if err != nil {
		return err
	}
	if before != nil {
		added, _ := rules.Diff(before, head.findings)
		if err := sessions.Absorb(ctx, added, now); err != nil {
			return err
		}
	}
	if session != "" {
		if err := sessions.PutBaseline(ctx, session, head.findings, now); err != nil {
			return err
		}
	}
	// then the session's changes so far, as the gate would apply them
	a := assembleApp(st, o, settings, head.project, o.root, nil, io.Discard)
	defer a.resolver.Close()
	w := &worktreeGate{o: o, st: st, projects: projects, isConfig: a.resolver.IsConfigFile}
	plan, ok, err := w.plan(ctx, head.project)
	if err != nil || !ok {
		return fmt.Errorf("cannot compare %s with HEAD %s: %v", o.root, head.project.Commit, err)
	}
	if err := applyFiles(ctx, a, slices.Concat(plan.upstream, plan.own)); err != nil {
		return err
	}
	a.project.Commit, a.project.Dirty = plan.head, plan.dirty
	if err := projects.Upsert(ctx, a.project); err != nil {
		return err
	}
	pruneGoneWorktrees(ctx, projects, a.project, co.Main)
	return nil
}

// applyFiles applies root-relative files to a's graph as watch does: a file
// import resolution depends on means a full scan.
func applyFiles(ctx context.Context, a *app, files []string) error {
	var rels []string
	for _, rel := range files {
		if !resolver.ShouldSkipPath(rel) && !slices.Contains(rels, rel) {
			rels = append(rels, rel)
		}
	}
	config, sources := splitChanges(rels, a.resolver.IsConfigFile, a.resolver.Supports)
	switch {
	case len(config) > 0:
		return a.fullScan(ctx)
	case len(sources) > 0:
		return a.svc.HandleChanges(ctx, sources)
	}
	return nil
}

// settingsOrDefault is root's .depgraph.yml, or the defaults when it is
// invalid (the command reports that on its own).
func settingsOrDefault(root string) config.File {
	s, err := config.Load(root)
	if err != nil {
		return config.File{}
	}
	return s
}

// headBuild is the graph of a worktree's HEAD.
type headBuild struct {
	project  project.Project // as stored: Commit = HEAD, no Dirty
	findings []rules.Finding
}

// buildFromHead replaces p's graph with the worktree's HEAD: the committed
// tree exported to a temporary directory, plus what is in the checkout but
// not in git and not the session's work (ignored files such as generated
// code, and submodules), scanned and evaluated with settings (the
// checkout's .depgraph.yml). HookSession nodes are kept.
func buildFromHead(ctx context.Context, st *graphrepo.Store, o *options, p project.Project, settings config.File) (headBuild, error) {
	tmp, err := os.MkdirTemp("", "depgraph-head-")
	if err != nil {
		return headBuild{}, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	ex, err := workspace.ExportHead(ctx, o.root, tmp)
	if err != nil {
		return headBuild{}, err
	}
	r := treesitter.New()
	keep := func(rel string) bool {
		return !resolver.ShouldSkipPath(rel) && (r.Supports(rel) || r.IsConfigFile(rel))
	}
	err = copyOutsideGit(ctx, o.root, tmp, ex.Submodules, keep)
	r.Close()
	if err != nil {
		return headBuild{}, err
	}
	head := *o
	head.reset = false // FullScan replaces every file; Clear would drop the sessions
	p.LastScanAt, p.Commit, p.Dirty = time.Time{}, ex.Commit, nil
	a := assembleApp(st, &head, settings, p, tmp, nil, io.Discard)
	defer a.resolver.Close()
	if err := a.fullScan(ctx); err != nil {
		return headBuild{}, err
	}
	findings, err := evaluateNow(ctx, a)
	if err != nil {
		return headBuild{}, err
	}
	return headBuild{project: a.project, findings: findings}, nil
}

// copyOutsideGit copies into dst the files of root that a scan of root sees
// but the commit does not hold: ignored files and the submodules' files.
func copyOutsideGit(ctx context.Context, root, dst string, submodules []string, keep func(string) bool) error {
	ignored, err := workspace.Ignored(ctx, root)
	if err != nil {
		return err
	}
	for _, rel := range ignored {
		if keep(rel) {
			if err := workspace.CopyFile(root, dst, rel); err != nil {
				return err
			}
		}
	}
	for _, sub := range submodules {
		base := filepath.Join(root, filepath.FromSlash(sub))
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				return nil
			}
			rel = filepath.ToSlash(rel)
			if d.IsDir() {
				if path != base && resolver.ShouldSkipDir(rel, d.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if keep(rel) {
				return workspace.CopyFile(root, dst, rel)
			}
			return nil
		})
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// pruneGoneWorktrees removes the projects of p's other worktrees of the same
// main tree (same base and origin, root under main) whose directory no longer
// exists: the agent's worktree was removed. Failures are ignored: this is
// housekeeping, not the check.
func pruneGoneWorktrees(ctx context.Context, repo project.Repository, p project.Project, main string) {
	all, err := repo.List(ctx)
	if err != nil {
		return
	}
	for _, q := range project.Stale(all, workspace.Exists) {
		rel, err := filepath.Rel(main, q.Root)
		if q.Key == p.Key || q.Base != p.Base || q.Worktree == "" || q.Origin != p.Origin ||
			err != nil || !filepath.IsLocal(rel) {
			continue
		}
		_ = repo.Delete(ctx, q.Key)
	}
}

// gateFiles turns the gate's file arguments into root-relative slash paths;
// paths outside root are dropped.
func gateFiles(root string, files []string) []string {
	var out []string
	for _, f := range files {
		if filepath.IsAbs(f) {
			rel, err := filepath.Rel(root, f)
			if err != nil {
				continue
			}
			f = rel
		}
		f = filepath.Clean(f)
		if !filepath.IsLocal(f) {
			continue
		}
		out = append(out, filepath.ToSlash(f))
	}
	return out
}

// evalSymlinks resolves p's symlinks (p itself when that fails), for
// comparing it with workspace.Locate's paths.
func evalSymlinks(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}
