package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// gitRunner returns a function that runs git in a directory with a fixed
// identity and no global configuration.
func gitRunner(t *testing.T) func(dir string, args ...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	return func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t",
			"GIT_COMMITTER_EMAIL=t@e", "GIT_CONFIG_GLOBAL=/dev/null")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
}

func realpath(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestLocate(t *testing.T) {
	git := gitRunner(t)
	ctx := context.Background()
	base := realpath(t, t.TempDir())
	main := filepath.Join(base, "app")
	if err := os.MkdirAll(filepath.Join(main, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(main, "init", "-q")
	write(t, filepath.Join(main, "sub", "x.go"), "package sub\n")
	git(main, "add", ".")
	git(main, "commit", "-qm", "init")
	wt := filepath.Join(main, ".claude", "worktrees", "feat")
	git(main, "worktree", "add", "-q", "-b", "feat", wt)

	m := Locate(ctx, filepath.Join(main, "sub"))
	if m.Top != main || m.Main != main || m.Linked {
		t.Errorf("main subdir: %+v", m)
	}
	w := Locate(ctx, filepath.Join(wt, "sub"))
	if w.Top != wt || w.Main != main || !w.Linked {
		t.Errorf("worktree subdir: %+v", w)
	}
	if !m.SameRepository(w) || m.SameCheckout(w) || !w.SameCheckout(Locate(ctx, wt)) {
		t.Errorf("same repository / checkout: %+v %+v", m, w)
	}
	if n := Locate(ctx, base); n.Top != "" || n.SameRepository(Checkout{}) {
		t.Errorf("no git: %+v", n)
	}
}

func TestExportHeadAndChanges(t *testing.T) {
	git := gitRunner(t)
	ctx := context.Background()
	root := realpath(t, t.TempDir())
	git(root, "init", "-q")
	for rel, body := range map[string]string{"a.go": "package a\n", "d/b.go": "package d\n", "gone.go": "package g\n"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, filepath.Join(root, rel), body)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	// archive attributes do not apply: the export is the committed tree
	write(t, filepath.Join(root, ".gitattributes"), "gone.go export-ignore\na.go export-subst\n")
	write(t, filepath.Join(root, ".gitignore"), "gen/\n")
	if err := os.MkdirAll(filepath.Join(root, "gen"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "gen", "z.go"), "package gen\n")
	git(root, "add", ".")
	git(root, "commit", "-qm", "one")
	first := Head(ctx, root)
	if len(first) < 40 {
		t.Fatalf("Head = %q", first)
	}

	// the working tree differs from HEAD; the export does not
	write(t, filepath.Join(root, "a.go"), "package a // edited\n")
	write(t, filepath.Join(root, "new.go"), "package n\n")
	if err := os.Remove(filepath.Join(root, "gone.go")); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	ex, err := ExportHead(ctx, root, dst)
	if err != nil || ex.Commit != first || len(ex.Submodules) != 0 {
		t.Fatalf("ExportHead = %+v, %v", ex, err)
	}
	for rel, want := range map[string]string{"a.go": "package a\n", "d/b.go": "package d\n", "gone.go": "package g\n"} {
		if b, err := os.ReadFile(filepath.Join(dst, rel)); err != nil || string(b) != want {
			t.Errorf("%s = %q, %v", rel, b, err)
		}
	}
	if target, err := os.Readlink(filepath.Join(dst, "link")); err != nil || target != "/etc/passwd" {
		t.Errorf("symlink = %q, %v", target, err)
	}
	for _, rel := range []string{"new.go", "gen/z.go"} { // untracked / ignored
		if _, err := os.Lstat(filepath.Join(dst, rel)); err == nil {
			t.Errorf("%s exported", rel)
		}
	}
	if ign, err := Ignored(ctx, root); err != nil || !slices.Equal(ign, []string{"gen/z.go"}) {
		t.Errorf("Ignored = %q, %v", ign, err)
	}
	if err := CopyFile(root, dst, "gen/z.go"); err != nil {
		t.Fatal(err)
	}
	if FileHash(dst, "gen/z.go") != FileHash(root, "gen/z.go") || FileHash(root, "gen/z.go") == FileHash(root, "a.go") ||
		FileHash(root, "nope.go") != "-" {
		t.Error("FileHash / CopyFile")
	}

	dirty, err := Dirty(ctx, root)
	if want := []string{"a.go", "gone.go", "new.go"}; err != nil || !slices.Equal(dirty, want) { // not gen/ (ignored)
		t.Errorf("Dirty = %q, %v; want %q", dirty, err, want)
	}

	// a commit, then a rename: both paths change between the commits
	git(root, "add", "-A")
	git(root, "commit", "-qm", "two")
	git(root, "mv", "d/b.go", "d/c.go")
	git(root, "commit", "-qm", "three")
	changed, err := ChangedBetween(ctx, root, first, Head(ctx, root))
	if want := []string{"a.go", "d/b.go", "d/c.go", "gone.go", "new.go"}; err != nil || !slices.Equal(changed, want) {
		t.Errorf("ChangedBetween = %q, %v; want %q", changed, err, want)
	}
	if dirty, err := Dirty(ctx, root); err != nil || len(dirty) != 0 {
		t.Errorf("clean Dirty = %q, %v", dirty, err)
	}
	if _, err := ChangedBetween(ctx, root, "0000000000000000000000000000000000000000", Head(ctx, root)); err == nil {
		t.Error("unknown commit: error expected")
	}
}

// The checkout's own commits are those its HEAD reflog records as made here
// (commit, rebase pick, cherry-pick, ...); commits brought in by a merge or a
// fast-forward are not, and the base is the newest commit that is not own.
func TestOwnCommits(t *testing.T) {
	git := gitRunner(t)
	ctx := context.Background()
	main := realpath(t, t.TempDir())
	git(main, "init", "-q", "-b", "main")
	write(t, filepath.Join(main, "a.go"), "package a\n")
	git(main, "add", ".")
	git(main, "commit", "-qm", "init")
	start := Head(ctx, main)
	wt := filepath.Join(main, ".claude", "worktrees", "feat")
	git(main, "worktree", "add", "-q", "-b", "feat", wt)

	write(t, filepath.Join(wt, "own.go"), "package a // own\n")
	git(wt, "add", ".")
	git(wt, "commit", "-qm", "own")
	own1 := Head(ctx, wt)
	write(t, filepath.Join(main, "up.go"), "package a // upstream\n")
	git(main, "add", ".")
	git(main, "commit", "-qm", "upstream")
	up := Head(ctx, main)
	git(wt, "rebase", "-q", "main")
	rebased := Head(ctx, wt)

	own := OwnCommits(ctx, wt)
	if !own[rebased] || !own[own1] || own[up] || own[start] {
		t.Errorf("OwnCommits = %v (rebased %s, own1 %s, up %s, start %s)", own, rebased, own1, up, start)
	}
	if b := Base(ctx, wt, own); b != up {
		t.Errorf("Base = %s, want %s", b, up)
	}
	files, err := OwnFiles(ctx, wt, start, rebased, own)
	if err != nil || !slices.Equal(files, []string{"own.go"}) {
		t.Errorf("OwnFiles = %q, %v", files, err)
	}
	// going back over an own commit: still own
	git(wt, "reset", "-q", "--hard", up)
	if files, _ := OwnFiles(ctx, wt, rebased, up, own); !slices.Equal(files, []string{"own.go"}) {
		t.Errorf("OwnFiles back = %q", files)
	}
	// the main tree made all of its commits itself
	if b := Base(ctx, main, OwnCommits(ctx, main)); b != "" {
		t.Errorf("main Base = %q", b)
	}
}
