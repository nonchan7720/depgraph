package workspace

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/nonchan7720/depgraph/pkg/domain/project"
)

// Checkout is where a directory sits in git: the working tree that contains
// it and the repository that working tree belongs to.
type Checkout struct {
	Top       string // root of the working tree containing the directory ("" without git)
	CommonDir string // the repository's common git directory, shared by all of its worktrees
	Main      string // root of the main worktree
	Linked    bool   // Top is a linked worktree (git worktree add), not the main one
}

// Locate returns the checkout that contains dir (zero without git). Paths
// are absolute with symlinks resolved, so checkouts can be compared.
func Locate(ctx context.Context, dir string) Checkout {
	lines := strings.Split(git(ctx, dir, "rev-parse", "--show-toplevel", "--git-dir", "--git-common-dir"), "\n")
	if len(lines) != 3 || lines[0] == "" {
		return Checkout{}
	}
	abs := func(p string) string {
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return filepath.Clean(p)
	}
	top := abs(lines[0])
	common := abs(lines[2])
	return Checkout{
		Top:       top,
		CommonDir: common,
		Main:      abs(project.MainRoot(top, common)),
		Linked:    project.WorktreeName(top, abs(lines[1]), common) != "",
	}
}

// SameRepository reports whether c and o are checkouts of one repository.
func (c Checkout) SameRepository(o Checkout) bool {
	return c.CommonDir != "" && c.CommonDir == o.CommonDir
}

// SameCheckout reports whether c and o are the same working tree.
func (c Checkout) SameCheckout(o Checkout) bool { return c.Top != "" && c.Top == o.Top }

// Head returns the commit checked out at root ("" without git or commits).
func Head(ctx context.Context, root string) string {
	return git(ctx, root, "rev-parse", "--verify", "-q", "HEAD^{commit}")
}

// gitLong runs git like git does, without its short timeout (for commands
// whose time grows with the repository), and returns stdout.
func gitLong(ctx context.Context, root string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(errOut.String()))
	}
	return out.Bytes(), nil
}

// paths splits NUL-separated git output into sorted, unique slash paths;
// each entry is passed through entry first ("" drops it).
func paths(b []byte, entry func(string) string) []string {
	var out []string
	for e := range strings.SplitSeq(string(b), "\x00") {
		if p := entry(e); p != "" {
			out = append(out, filepath.ToSlash(p))
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// Dirty returns the root-relative files whose content differs from HEAD:
// modified, added, deleted and untracked ones (ignored files are not listed;
// a rename lists both paths).
func Dirty(ctx context.Context, root string) ([]string, error) {
	b, err := gitLong(ctx, root, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--no-renames")
	if err != nil {
		return nil, err
	}
	return paths(b, func(e string) string {
		if len(e) <= 3 { // "XY path"
			return ""
		}
		return e[3:]
	}), nil
}

// ChangedBetween returns the root-relative files that differ between the
// commits from and to (a rename lists both paths).
func ChangedBetween(ctx context.Context, root, from, to string) ([]string, error) {
	if from == to {
		return nil, nil
	}
	b, err := gitLong(ctx, root, "diff", "--name-only", "-z", "--no-renames", from, to, "--")
	if err != nil {
		return nil, err
	}
	return paths(b, func(e string) string { return e }), nil
}

// ExportHead writes the files of root's HEAD commit (as committed, whatever
// the working tree holds) under dst and returns the commit. Only regular
// files are written: symlinks and submodules are left out, and no entry can
// leave dst.
func ExportHead(ctx context.Context, root, dst string) (string, error) {
	commit := Head(ctx, root)
	if commit == "" {
		return "", fmt.Errorf("%s has no commit to export", root)
	}
	cmd := exec.CommandContext(ctx, "git", "-C", root, "archive", "--format=tar", commit)
	var errOut bytes.Buffer
	cmd.Stderr = &errOut
	r, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	xerr := untar(r, dst)
	_, _ = io.Copy(io.Discard, r)
	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("git archive: %w: %s", err, strings.TrimSpace(errOut.String()))
	}
	return commit, xerr
}

func untar(r io.Reader, dst string) error {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg {
			continue // directories are created on demand; links are never followed
		}
		rel := filepath.FromSlash(h.Name)
		if !filepath.IsLocal(rel) {
			continue
		}
		p := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, tr)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
}
