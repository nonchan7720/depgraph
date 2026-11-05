// Package project models a repository registered in the graph store. One
// Neo4j database holds many projects; every Package / File / Symbol node
// carries the key of the project it belongs to.
//
// The key is decided by BaseKey (--project, then .depgraph.yml, then the
// directory name of the root, or of the main worktree for a linked git
// worktree) plus a worktree suffix (Key), so the same repository
// gets the same key wherever it is checked out. Reconcile decides whether a
// root that reuses a stored key is the same project (moved) or a collision.
package project

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"
)

// Project is one registered repository root.
type Project struct {
	Key        string    // unique; Base, or Base@Worktree for a linked git worktree
	Name       string    // display name (root's directory name)
	Root       string    // absolute root of the last scan
	Origin     string    // normalized git remote.origin.url ("" when unknown)
	Base       string    // key without the worktree suffix
	Worktree   string    // linked worktree name ("" for the main worktree / no git)
	LastScanAt time.Time // zero until the first full scan finished
	Files      int64     // number of stored files (filled by Repository.List)
	// Commit and Dirty are what the graph of a checkout nothing watches was
	// last brought up to date with (by depgraph gate): the git HEAD and the
	// files that then differed from it. Only files that changed between
	// Commit and the current HEAD, Dirty, and what differs from HEAD now can
	// differ from the graph. Empty when unknown.
	Commit string
	Dirty  []string
}

// BaseKey returns the project key before the worktree suffix: flag (--project),
// else cfg (.depgraph.yml "project"), else the last element of root (for a
// linked worktree, pass MainRoot so the base matches the main worktree).
func BaseKey(flag, cfg, root string) string {
	if k := strings.TrimSpace(flag); k != "" {
		return k
	}
	if k := strings.TrimSpace(cfg); k != "" {
		return k
	}
	return filepath.Base(filepath.Clean(root))
}

// Key joins a base key and a linked worktree name ("" keeps base).
func Key(base, worktree string) string {
	if worktree == "" {
		return base
	}
	return base + "@" + worktree
}

// WorktreeName returns the linked worktree name for root from the output of
// `git -C root rev-parse --git-dir` and `--git-common-dir` (either may be
// relative to root), or "" for a main worktree or when git is unavailable.
// A linked worktree's git dir is <common-dir>/worktrees/<name>; requiring the
// "worktrees" parent keeps a path spelled differently (symlinks) on a main
// worktree from looking linked.
func WorktreeName(root, gitDir, commonDir string) string {
	gitDir, commonDir = strings.TrimSpace(gitDir), strings.TrimSpace(commonDir)
	if gitDir == "" || commonDir == "" {
		return ""
	}
	abs := func(p string) string {
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		return filepath.Clean(p)
	}
	g, c := abs(gitDir), abs(commonDir)
	if g == c || filepath.Base(filepath.Dir(g)) != "worktrees" {
		return ""
	}
	return filepath.Base(g)
}

// MainRoot returns the main worktree directory of a repository from the
// output of `git rev-parse --git-common-dir` (relative to root): the parent of
// a ".git" directory, or a bare repository's path without ".git". Without git
// it returns root. A linked worktree uses its directory name for the default
// base key, so all worktrees of a repository share a base.
func MainRoot(root, commonDir string) string {
	commonDir = strings.TrimSpace(commonDir)
	if commonDir == "" {
		return root
	}
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(root, commonDir)
	}
	commonDir = filepath.Clean(commonDir)
	if filepath.Base(commonDir) == ".git" {
		return filepath.Dir(commonDir)
	}
	return strings.TrimSuffix(commonDir, ".git")
}

// NormalizeOrigin turns the spellings of one remote (scp-like ssh, ssh://,
// https with credentials, trailing .git or /) into host/path, so clones made
// with different URLs compare equal. Local paths stay paths.
func NormalizeOrigin(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	trim := func(p string) string {
		p = strings.TrimRight(p, "/")
		p = strings.TrimSuffix(p, ".git")
		return strings.TrimRight(p, "/")
	}
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return trim(s)
		}
		if u.Scheme == "file" {
			return trim(u.Path)
		}
		return strings.ToLower(u.Hostname()) + "/" + strings.TrimLeft(trim(u.Path), "/")
	}
	// scp-like: [user@]host:path (a local path has no ':' before the first '/')
	if i := strings.Index(s, ":"); i > 0 && !strings.Contains(s[:i], "/") {
		host := s[:i]
		if at := strings.LastIndex(host, "@"); at >= 0 {
			host = host[at+1:]
		}
		return strings.ToLower(host) + "/" + strings.TrimLeft(trim(s[i+1:]), "/")
	}
	return trim(s)
}

// ConflictError reports a key already used by another, still existing root.
type ConflictError struct {
	Key      string
	Existing Project
	Root     string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("project %q is already used by %s (origin %q, worktree %q); "+
		"%s looks like a different repository. Give it another key with --project <key> "+
		"or `project: <key>` in %s",
		e.Key, e.Existing.Root, e.Existing.Origin, e.Existing.Worktree, e.Root,
		filepath.Join(e.Root, ".depgraph.yml"))
}

// Reconcile decides what to store for incoming given the project stored under
// the same key (nil when none). The same root, or a move (same worktree and
// either the same non-empty origin or a previous root that no longer exists),
// returns incoming with the stored LastScanAt, Commit and Dirty kept. Anything else is a
// *ConflictError: two live checkouts must not overwrite each other's data.
func Reconcile(existing *Project, incoming Project, exists func(root string) bool) (Project, error) {
	if existing == nil {
		return incoming, nil
	}
	out := incoming
	out.LastScanAt = existing.LastScanAt
	out.Commit, out.Dirty = existing.Commit, existing.Dirty
	if existing.Root == incoming.Root {
		return out, nil
	}
	sameWorktree := existing.Worktree == incoming.Worktree
	sameOrigin := incoming.Origin != "" && existing.Origin == incoming.Origin
	if sameWorktree && (sameOrigin || !exists(existing.Root)) {
		return out, nil
	}
	return Project{}, &ConflictError{Key: incoming.Key, Existing: *existing, Root: incoming.Root}
}

// Stale returns the projects whose root no longer exists (or was never set).
func Stale(ps []Project, exists func(root string) bool) []Project {
	var out []Project
	for _, p := range ps {
		if p.Root == "" || !exists(p.Root) {
			out = append(out, p)
		}
	}
	return out
}
