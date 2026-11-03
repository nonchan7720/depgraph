package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLoadSettings(t *testing.T) {
	dir := t.TempDir()
	c, err := LoadSettings(dir)
	if err != nil || c.Project != "" {
		t.Fatalf("missing file: %+v, %v", c, err)
	}
	write(t, filepath.Join(dir, SettingsFile), "# comment\nproject: my-key\nrules: {future: true}\n")
	c, err = LoadSettings(dir)
	if err != nil || c.Project != "my-key" {
		t.Fatalf("LoadSettings = %+v, %v", c, err)
	}
	write(t, filepath.Join(dir, SettingsFile), "project: [broken\n")
	if _, err := LoadSettings(dir); err == nil {
		t.Error("broken yaml must be an error")
	}
}

func TestIdentifyWithoutGit(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "plain-dir")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := Identify(ctx, root, "")
	if err != nil || p.Key != "plain-dir" || p.Base != "plain-dir" || p.Worktree != "" || p.Origin != "" || p.Root != root || p.Name != "plain-dir" {
		t.Fatalf("Identify = %+v, %v", p, err)
	}
	write(t, filepath.Join(root, SettingsFile), "project: from-yml\n")
	if p, _ = Identify(ctx, root, ""); p.Key != "from-yml" {
		t.Errorf("yml key = %q", p.Key)
	}
	if p, _ = Identify(ctx, root, "from-flag"); p.Key != "from-flag" {
		t.Errorf("flag key = %q", p.Key)
	}
}

func TestIdentifyGitWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	base := t.TempDir()
	main := filepath.Join(base, "app")
	wt := filepath.Join(base, "app-feat")
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e", "GIT_CONFIG_GLOBAL=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.Mkdir(main, 0o755); err != nil {
		t.Fatal(err)
	}
	git(main, "init", "-q")
	git(main, "remote", "add", "origin", "git@github.com:owner/app.git")
	write(t, filepath.Join(main, SettingsFile), "project: shared\n")
	git(main, "add", ".")
	git(main, "commit", "-qm", "init")
	git(main, "worktree", "add", "-q", "-b", "feat", wt)
	if err := os.MkdirAll(filepath.Join(main, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	p, err := Identify(ctx, main, "")
	if err != nil || p.Key != "shared" || p.Base != "shared" || p.Worktree != "" || p.Origin != "github.com/owner/app" {
		t.Fatalf("main: %+v, %v", p, err)
	}
	if p, _ = Identify(ctx, filepath.Join(main, "sub"), ""); p.Worktree != "" {
		t.Errorf("main subdir looks like a worktree: %+v", p)
	}
	p, err = Identify(ctx, wt, "")
	wantName := filepath.Base(wt) // git names the worktree after the directory
	if err != nil || p.Key != "shared@"+wantName || p.Base != "shared" || p.Worktree != wantName || p.Origin != "github.com/owner/app" {
		t.Fatalf("worktree: %+v, %v", p, err)
	}
	if p, _ = Identify(ctx, wt, "explicit"); p.Key != "explicit@"+wantName {
		t.Errorf("flag in worktree: %q", p.Key)
	}
	// without .depgraph.yml the worktree's base is the main worktree's directory
	// name, so both share a base
	if err := os.Remove(filepath.Join(wt, SettingsFile)); err != nil {
		t.Fatal(err)
	}
	if p, _ = Identify(ctx, wt, ""); p.Base != "app" || p.Key != "app@"+wantName || p.Name != wantName {
		t.Errorf("worktree default base: %+v", p)
	}
}

func TestExists(t *testing.T) {
	dir := t.TempDir()
	if !Exists(dir) || Exists(filepath.Join(dir, "nope")) || Exists("") {
		t.Error("Exists")
	}
	f := filepath.Join(dir, "f")
	write(t, f, "x")
	if Exists(f) {
		t.Error("a file is not a root")
	}
}

func write(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
