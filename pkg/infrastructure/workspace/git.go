package workspace

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
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

// Export is what ExportHead wrote.
type Export struct {
	Commit string
	// Submodules are the root-relative paths of the submodules at HEAD; their
	// files are not part of the commit (copy them from the checkout if needed).
	Submodules []string
}

// ExportHead is ExportCommit of root's HEAD.
func ExportHead(ctx context.Context, root, dst string) (Export, error) {
	return ExportCommit(ctx, root, Head(ctx, root), dst)
}

// ExportCommit writes the tree of commit under dst exactly as committed
// (blob contents, no export-ignore / export-subst, whatever the working
// tree holds): regular files, then symlinks with their committed target. No
// entry can leave dst, and nothing is written through a symlink.
func ExportCommit(ctx context.Context, root, commit, dst string) (Export, error) {
	if commit == "" {
		return Export{}, fmt.Errorf("%s has no commit to export", root)
	}
	b, err := gitLong(ctx, root, "ls-tree", "-r", "-z", "--full-tree", commit)
	if err != nil {
		return Export{}, err
	}
	type entry struct{ mode, sha, path string }
	var blobs []entry
	ex := Export{Commit: commit}
	for e := range strings.SplitSeq(string(b), "\x00") {
		meta, path, ok := strings.Cut(e, "\t") // "<mode> <type> <sha>\t<path>"
		f := strings.Fields(meta)
		if !ok || len(f) != 3 || !filepath.IsLocal(filepath.FromSlash(path)) {
			continue
		}
		switch {
		case f[1] == "commit":
			ex.Submodules = append(ex.Submodules, path)
		case f[1] == "blob" && (f[0] == "100644" || f[0] == "100755" || f[0] == "120000"):
			blobs = append(blobs, entry{f[0], f[2], path})
		}
	}
	// regular files first: a symlink created later can never be written through
	slices.SortStableFunc(blobs, func(a, b entry) int {
		return strings.Compare(boolKey(a.mode == "120000"), boolKey(b.mode == "120000"))
	})
	var shas strings.Builder
	for _, e := range blobs {
		shas.WriteString(e.sha + "\n")
	}
	cmd := exec.CommandContext(ctx, "git", "-C", root, "cat-file", "--batch")
	cmd.Stdin = strings.NewReader(shas.String())
	var errOut bytes.Buffer
	cmd.Stderr = &errOut
	out, err := cmd.StdoutPipe()
	if err != nil {
		return Export{}, err
	}
	if err := cmd.Start(); err != nil {
		return Export{}, err
	}
	r := bufio.NewReader(out)
	var werr error
	for _, e := range blobs {
		body, err := readBatchBlob(r)
		if err != nil {
			werr = err
			break
		}
		if werr = writeEntry(dst, e.path, e.mode == "120000", body); werr != nil {
			break
		}
	}
	_, _ = io.Copy(io.Discard, r)
	if err := cmd.Wait(); err != nil {
		return Export{}, fmt.Errorf("git cat-file: %w: %s", err, strings.TrimSpace(errOut.String()))
	}
	return ex, werr
}

func boolKey(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// readBatchBlob reads one "<sha> <type> <size>\n<content>\n" record of
// git cat-file --batch.
func readBatchBlob(r *bufio.Reader) ([]byte, error) {
	head, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	f := strings.Fields(head)
	if len(f) != 3 {
		return nil, fmt.Errorf("git cat-file: unexpected %q", strings.TrimSpace(head))
	}
	n, err := strconv.Atoi(f[2])
	if err != nil {
		return nil, err
	}
	body := make([]byte, n+1) // content and the trailing newline
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body[:n], nil
}

// writeEntry writes body as dst/rel, or a symlink to body when link. A path
// whose parent is a symlink (or anything but a directory) is not written.
func writeEntry(dst, rel string, link bool, body []byte) error {
	p := filepath.Join(dst, filepath.FromSlash(rel))
	if err := mkdirNoLinks(dst, filepath.Dir(p)); err != nil {
		return nil // e.g. under a path that is not a directory: skip it
	}
	if link {
		return os.Symlink(string(body), p)
	}
	return os.WriteFile(p, body, 0o644)
}

// mkdirNoLinks creates dir (inside base) without following a symlink on the way.
func mkdirNoLinks(base, dir string) error {
	rel, err := filepath.Rel(base, dir)
	if err != nil || !filepath.IsLocal(rel) && rel != "." {
		return fmt.Errorf("%s is outside %s", dir, base)
	}
	cur := base
	for part := range strings.SplitSeq(rel, string(filepath.Separator)) {
		if part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		st, err := os.Lstat(cur)
		switch {
		case errors.Is(err, os.ErrNotExist):
			if err := os.Mkdir(cur, 0o755); err != nil {
				return err
			}
		case err != nil:
			return err
		case !st.IsDir():
			return fmt.Errorf("%s is not a directory", cur)
		}
	}
	return nil
}

// Ignored returns the root-relative files present in root that git ignores.
func Ignored(ctx context.Context, root string) ([]string, error) {
	b, err := gitLong(ctx, root, "ls-files", "-z", "--others", "--ignored", "--exclude-standard")
	if err != nil {
		return nil, err
	}
	return paths(b, func(e string) string { return e }), nil
}

// CopyFile copies root/rel to dst/rel (a symlink as a symlink); a file that
// is gone is not an error.
func CopyFile(root, dst, rel string) error {
	src := filepath.Join(root, filepath.FromSlash(rel))
	st, err := os.Lstat(src)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return writeEntry(dst, rel, true, []byte(target))
	}
	if !st.Mode().IsRegular() {
		return nil
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return writeEntry(dst, rel, false, b)
}

// FileHash identifies the content of root/rel ("-" when it does not exist),
// to tell whether a file changed since it was last applied.
func FileHash(root, rel string) string {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return "-"
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16])
}

// ShortHash is a short stable file-name-safe identifier of s.
func ShortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

// ownReflog are the HEAD reflog actions that make a commit in the checkout
// (as opposed to moving to one made elsewhere: merge, checkout, reset, a
// fast-forward pull).
var ownReflog = []string{"commit", "cherry-pick", "revert", "am",
	"rebase (pick)", "rebase (reword)", "rebase (edit)", "rebase (squash)", "rebase (fixup)",
	"rebase -i (pick)", "rebase -i (reword)", "rebase -i (edit)", "rebase -i (squash)", "rebase -i (fixup)",
	"pull --rebase (pick)"}

// OwnCommits returns the commits root's HEAD reflog records as made in this
// checkout (commit, amend, rebase picks, cherry-pick, revert, am, and merge
// commits made here; a fast-forward brings none).
func OwnCommits(ctx context.Context, root string) map[string]bool {
	b, err := gitLong(ctx, root, "reflog", "show", "--format=%H%x09%gs", "HEAD", "--")
	own := map[string]bool{}
	if err != nil {
		return own
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		sha, subject, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		if strings.Contains(subject, "Fast-forward") {
			continue
		}
		if (strings.HasPrefix(subject, "merge ") || strings.HasPrefix(subject, "pull")) && strings.Contains(subject, ": Merge made by") {
			own[sha] = true // a merge commit made here
			continue
		}
		for _, a := range ownReflog {
			if strings.HasPrefix(subject, a+":") || strings.HasPrefix(subject, a+" (") {
				own[sha] = true
				break
			}
		}
	}
	return own
}

// Base returns the newest commit on HEAD's first-parent history that is not
// in own ("" when every commit is): what the checkout started from, before
// its own work.
func Base(ctx context.Context, root string, own map[string]bool) string {
	b, err := gitLong(ctx, root, "rev-list", "--first-parent", "--max-count=10000", "HEAD", "--")
	if err != nil {
		return ""
	}
	for c := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
		if c != "" && !own[c] {
			return c
		}
	}
	return ""
}

// OwnFiles returns the root-relative files changed by the commits of own
// that lie between from and to, on either side (a reset back over an own
// commit undoes own work), each with the newest such commit. Merge commits
// are not counted, not even their conflict resolution: a conflicted file
// holds the merged branch's changes too, and blaming the checkout for those
// is worse than missing debt added in the resolution itself.
func OwnFiles(ctx context.Context, root, from, to string, own map[string]bool) (map[string]string, error) {
	out := map[string]string{}
	if from == to {
		return out, nil
	}
	b, err := gitLong(ctx, root, "log", "--no-merges", "--no-renames", "--format=%x01%H", "--name-only", "-z",
		from+"..."+to, "--")
	if err != nil {
		return nil, err
	}
	cur := "" // the commit whose files follow, when own
	for e := range strings.SplitSeq(string(b), "\x00") {
		e = strings.TrimLeft(e, "\n")
		if sha, ok := strings.CutPrefix(e, "\x01"); ok {
			sha, rest, _ := strings.Cut(sha, "\n")
			cur = strings.TrimSpace(sha)
			if !own[cur] {
				cur = ""
			}
			e = rest
		}
		if cur != "" && e != "" {
			p := filepath.ToSlash(e)
			if _, seen := out[p]; !seen { // git log lists the newest first
				out[p] = cur
			}
		}
	}
	return out, nil
}

// SameContent reports whether rel has the same content in commits a and b
// (both without it counts as the same).
func SameContent(ctx context.Context, root, a, b, rel string) bool {
	blob := func(c string) string { return git(ctx, root, "rev-parse", "-q", "--verify", c+":"+rel) }
	return blob(a) == blob(b)
}

// InProgress names the git operation that is stopped half way in root
// (merge, cherry-pick, revert, rebase), or "": while one is, the working
// tree mixes the operation's incoming changes with the checkout's own.
func InProgress(ctx context.Context, root string) string {
	for _, op := range []struct{ path, name string }{
		{"MERGE_HEAD", "merge"}, {"CHERRY_PICK_HEAD", "cherry-pick"}, {"REVERT_HEAD", "revert"},
		{"rebase-merge", "rebase"}, {"rebase-apply", "rebase"},
	} {
		p := git(ctx, root, "rev-parse", "--git-path", op.path)
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		if _, err := os.Stat(p); err == nil {
			return op.name
		}
	}
	return ""
}
