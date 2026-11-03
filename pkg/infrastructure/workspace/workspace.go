// Package workspace inspects a repository root on disk: its .depgraph.yml and
// its git metadata (origin, linked worktree), and derives the project identity.
package workspace

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/project"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/config"
)

// SettingsFile is the per-repository settings file, looked up directly under root.
const SettingsFile = config.FileName

// gitTimeout bounds each git call (the hook has a short overall timeout).
const gitTimeout = 2 * time.Second

// git runs `git -C root args...` and returns trimmed stdout, or "" when git is
// missing, root is not a repository or the value is unset.
func git(ctx context.Context, root string, args ...string) string {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}

// Identify returns the project identity of root (absolute): key from flagKey
// (--project), .depgraph.yml or the directory name (of the main worktree when
// root is a linked git worktree), suffixed with @<name> in a linked worktree,
// plus the normalized origin. LastScanAt is left zero.
func Identify(ctx context.Context, root, flagKey string) (project.Project, error) {
	cfg, err := config.Load(root)
	if err != nil {
		return project.Project{}, err
	}
	var gitDir, commonDir string
	if lines := strings.Split(git(ctx, root, "rev-parse", "--git-dir", "--git-common-dir"), "\n"); len(lines) == 2 {
		gitDir, commonDir = lines[0], lines[1]
	}
	wt := project.WorktreeName(root, gitDir, commonDir)
	dirForKey := root
	if wt != "" {
		dirForKey = project.MainRoot(root, commonDir)
	}
	base := project.BaseKey(flagKey, cfg.Project, dirForKey)
	return project.Project{
		Key:      project.Key(base, wt),
		Name:     filepath.Base(filepath.Clean(root)),
		Root:     root,
		Origin:   project.NormalizeOrigin(git(ctx, root, "config", "--get", "remote.origin.url")),
		Base:     base,
		Worktree: wt,
	}, nil
}

// Exists reports whether root is an existing directory.
func Exists(root string) bool {
	if root == "" {
		return false
	}
	st, err := os.Stat(root)
	return err == nil && st.IsDir()
}
