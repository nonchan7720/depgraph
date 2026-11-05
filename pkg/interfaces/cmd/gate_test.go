package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	graphrepo "github.com/nonchan7720/depgraph/pkg/infrastructure/repository/graph"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/repository/hooksession"
	projectrepo "github.com/nonchan7720/depgraph/pkg/infrastructure/repository/project"
)

func TestSplitGateFiles(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"a.go b/c.go", []string{"a.go", "b/c.go"}},
		{`'dir with space/x.go' y.go`, []string{"dir with space/x.go", "y.go"}},
		{`'it'\''s.go'`, []string{"it's.go"}},
		{`"d q.go" e\ f.go`, []string{"d q.go", "e f.go"}},
		{"  a.go\n b.go ", []string{"a.go", "b.go"}},
	}
	for _, tt := range tests {
		if got := splitGateFiles(tt.in); !slices.Equal(got, tt.want) {
			t.Errorf("splitGateFiles(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// The gate never fails because of the environment: no Neo4j, no session.
func TestGatePassesWithoutStore(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CLAUDE_SESSION_ID", "s1")
	t.Setenv("CLAUDE_GATE_FILES", "a.go")
	var out, errOut bytes.Buffer
	c := newRootCommand(&options{})
	c.SetOut(&out)
	c.SetErr(&errOut)
	c.SetArgs([]string{"gate", "--root", dir, "--neo4j", "bolt://127.0.0.1:1", "--timeout", "2s"})
	start := time.Now()
	if err := c.Execute(); err != nil {
		t.Fatalf("err = %v (must pass when Neo4j is down)", err)
	}
	if out.Len() != 0 || !strings.Contains(errOut.String(), "skipped") {
		t.Errorf("stdout=%q stderr=%q", out.String(), errOut.String())
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("took %s", d)
	}
}

func TestGateRejectsUnknownSeverity(t *testing.T) {
	c := newRootCommand(&options{})
	c.SetOut(&bytes.Buffer{})
	c.SetErr(&bytes.Buffer{})
	c.SetArgs([]string{"gate", "--root", t.TempDir(), "--fail-on", "fatal"})
	if err := c.Execute(); err == nil || !strings.Contains(err.Error(), "fail-on") {
		t.Fatalf("err = %v", err)
	}
}

// End to end on Neo4j: scan, session-start records the baseline, an edit
// that adds an import cycle fails the gate with the cycle, undoing it passes.
func TestGateEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	st, err := graphrepo.New(ctx, env("NEO4J_URI", "bolt://localhost:7687"), env("NEO4J_USER", "neo4j"),
		env("NEO4J_PASSWORD", "depgraph123"), env("NEO4J_DATABASE", "neo4j"))
	if err != nil {
		t.Skipf("neo4j not available: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	if os.Getenv("NEO4J_URI") == "" {
		t.Skip("NEO4J_URI not set: the commands would look for a container store")
	}

	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"go.mod":        "module example.com/m\n\ngo 1.22\n",
		"main.go":       "package main\n\nimport \"example.com/m/a\"\n\nfunc main() { a.A() }\n",
		"a/a.go":        "package a\n\nimport \"example.com/m/b\"\n\n// A calls B.\nfunc A() { b.B() }\n",
		"b/b.go":        "package b\n\n// B does nothing.\nfunc B() {}\n",
		".depgraph.yml": "rules:\n  duplicate-symbol: {enabled: false}\n",
	})
	key := fmt.Sprintf("zz_test_%d_gate", time.Now().UnixNano())
	t.Cleanup(func() { _ = projectrepo.New(st).Delete(context.Background(), key) })

	run := func(stdin string, args ...string) (string, string, error) {
		var out, errOut bytes.Buffer
		c := newRootCommand(&options{})
		c.SetOut(&out)
		c.SetErr(&errOut)
		c.SetIn(strings.NewReader(stdin))
		c.SetArgs(append(args, "--root", root, "--project", key))
		err := c.ExecuteContext(ctx)
		return out.String(), errOut.String(), err
	}
	if _, e, err := run("", "scan", "--claude-md", ""); err != nil {
		t.Fatalf("scan: %v\n%s", err, e)
	}
	if _, e, err := run(`{"session_id":"s1","hook_event_name":"SessionStart"}`, "hook", "session-start", "--timeout", "20s"); err != nil {
		t.Fatalf("session-start: %v\n%s", err, e)
	}

	t.Setenv("CLAUDE_SESSION_ID", "s1")
	// nothing changed yet: pass
	t.Setenv("CLAUDE_GATE_FILES", "")
	if out, e, err := run("", "gate"); err != nil || out != "" {
		t.Fatalf("clean gate: out=%q err=%v\n%s", out, err, e)
	}

	// b imports a: a -> b -> a
	writeTree(t, root, map[string]string{"b/b.go": "package b\n\nimport \"example.com/m/a\"\n\n// B calls A.\nfunc B() { a.A() }\n"})
	t.Setenv("CLAUDE_GATE_FILES", "b/b.go")
	out, e, err := run("", "gate")
	var gf *gateFailure
	if !errors.As(err, &gf) || gf.n != 1 {
		t.Fatalf("gate after the cycle: err=%v out=%q\n%s", err, out, e)
	}
	if !strings.Contains(out, "import-cycle") {
		t.Errorf("report lacks the cycle:\n%s", out)
	}
	// the stop check (no files) sees the same
	t.Setenv("CLAUDE_GATE_FILES", "")
	if _, _, err := run("", "gate"); !errors.As(err, &gf) {
		t.Errorf("stop check passed with the cycle: %v", err)
	}
	// --fail-on error still fails (a cycle is an error); the info floor too
	if _, _, err := run("", "gate", "--fail-on", "error"); !errors.As(err, &gf) {
		t.Errorf("--fail-on error passed: %v", err)
	}

	// undo
	writeTree(t, root, map[string]string{"b/b.go": "package b\n\n// B does nothing.\nfunc B() {}\n"})
	t.Setenv("CLAUDE_GATE_FILES", "b/b.go")
	if out, e, err := run("", "gate"); err != nil || out != "" {
		t.Fatalf("gate after undo: out=%q err=%v\n%s", out, err, e)
	}

	// another session has its own baseline: the debt it started with passes
	writeTree(t, root, map[string]string{"b/b.go": "package b\n\nimport \"example.com/m/a\"\n\n// B calls A.\nfunc B() { a.A() }\n"})
	if _, _, err := run("", "gate"); !errors.As(err, &gf) {
		t.Fatalf("s1 must fail: %v", err)
	}
	t.Setenv("CLAUDE_SESSION_ID", "s2")
	if _, e, err := run(`{"session_id":"s2","hook_event_name":"SessionStart"}`, "hook", "session-start", "--timeout", "20s"); err != nil {
		t.Fatalf("session-start s2: %v\n%s", err, e)
	}
	if out, e, err := run("", "gate"); err != nil || out != "" {
		t.Fatalf("s2 started with the cycle: out=%q err=%v\n%s", out, err, e)
	}

	// claude-hook-gate's session_start: no stdin, the session in the
	// environment, the context as plain text; the baseline is recorded
	t.Setenv("CLAUDE_SESSION_ID", "s3")
	out, e, err = run("", "hook", "session-start", "--format", "text", "--timeout", "20s")
	if err != nil || !strings.HasPrefix(out, "## depgraph") || strings.Contains(out, "hookSpecificOutput") {
		t.Fatalf("session-start --format text: out=%q err=%v\n%s", out, err, e)
	}
	if base, ok, err := hooksession.New(st, key).Baseline(ctx, "s3"); err != nil || !ok || len(base) == 0 {
		t.Fatalf("no baseline for the env session: %+v ok=%v err=%v", base, ok, err)
	}
}

// testGit runs git in dir with a fixed identity and no global configuration.
func testGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t",
		"GIT_COMMITTER_EMAIL=t@e", "GIT_CONFIG_GLOBAL=/dev/null")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// The gate as claude-hook-gate runs it for an agent working in a linked
// worktree under the main tree: the working directory is the worktree,
// $CLAUDE_PROJECT_DIR is still the main tree and $CLAUDE_GATE_FILES are
// relative to the worktree. Only `watch` of the main tree runs.
func TestGateInLinkedWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	st, err := graphrepo.New(ctx, env("NEO4J_URI", "bolt://localhost:7687"), env("NEO4J_USER", "neo4j"),
		env("NEO4J_PASSWORD", "depgraph123"), env("NEO4J_DATABASE", "neo4j"))
	if err != nil {
		t.Skipf("neo4j not available: %v", err)
	}
	t.Cleanup(func() { _ = st.Close(context.Background()) })
	if os.Getenv("NEO4J_URI") == "" {
		t.Skip("NEO4J_URI not set: the commands would look for a container store")
	}

	main, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := fmt.Sprintf("zz_test_%d_gatewt", time.Now().UnixNano())
	wtKey := key + "@feat"
	t.Cleanup(func() {
		_ = projectrepo.New(st).Delete(context.Background(), key)
		_ = projectrepo.New(st).Delete(context.Background(), wtKey)
	})
	const bClean = "package b\n\n// B does nothing.\nfunc B() {}\n"
	const bCycle = "package b\n\nimport \"example.com/m/a\"\n\n// B calls A.\nfunc B() { a.A() }\n"
	writeTree(t, main, map[string]string{
		"go.mod":  "module example.com/m\n\ngo 1.22\n",
		"main.go": "package main\n\nimport (\n\t\"example.com/m/a\"\n\t\"example.com/m/c\"\n)\n\nfunc main() { a.A(); c.C() }\n",
		"a/a.go":  "package a\n\nimport \"example.com/m/b\"\n\n// A calls B.\nfunc A() { b.B() }\n",
		"b/b.go":  bClean,
		// debt committed before the session: c <-> d
		"c/c.go":        "package c\n\nimport \"example.com/m/d\"\n\n// C calls D.\nfunc C() { d.D() }\n",
		"d/d.go":        "package d\n\nimport \"example.com/m/c\"\n\n// D calls C.\nfunc D() { c.C() }\n",
		".depgraph.yml": "project: " + key + "\nrules:\n  duplicate-symbol: {enabled: false}\n",
		".gitignore":    ".claude/\n",
	})
	testGit(t, main, "init", "-q")
	testGit(t, main, "add", ".")
	testGit(t, main, "commit", "-qm", "init")
	wt := filepath.Join(main, ".claude", "worktrees", "feat")
	testGit(t, main, "worktree", "add", "-q", "-b", "feat", wt)

	// claude-hook-gate passes no --root: the root comes from the environment
	run := func(cwd, stdin string, args ...string) (string, string, error) {
		t.Helper()
		t.Chdir(cwd)
		var out, errOut bytes.Buffer
		c := newRootCommand(&options{})
		c.SetOut(&out)
		c.SetErr(&errOut)
		c.SetIn(strings.NewReader(stdin))
		c.SetArgs(args)
		err := c.ExecuteContext(ctx)
		return out.String(), errOut.String(), err
	}
	t.Setenv("CLAUDE_PROJECT_DIR", main)
	t.Setenv("CLAUDE_SESSION_ID", "s1")
	t.Setenv("CLAUDE_GATE_FILES", "")
	if _, e, err := run(main, "", "scan", "--claude-md", ""); err != nil {
		t.Fatalf("scan: %v\n%s", err, e)
	}
	if _, e, err := run(main, "", "hook", "session-start", "--format", "text", "--timeout", "20s"); err != nil {
		t.Fatalf("session-start: %v\n%s", err, e)
	}

	// the agent adds a cycle in the worktree
	writeTree(t, wt, map[string]string{"b/b.go": bCycle})
	t.Setenv("CLAUDE_GATE_FILES", "b/b.go")
	out, e, err := run(wt, "", "gate")
	var gf *gateFailure
	if !errors.As(err, &gf) || gf.n != 1 || !strings.Contains(out, "a -> b") && !strings.Contains(out, "b -> a") {
		t.Fatalf("worktree gate: err=%v out=%q\n%s", err, out, e)
	}
	if strings.Contains(out, "c ->") || strings.Contains(out, "d ->") {
		t.Errorf("debt committed before the session reported:\n%s", out)
	}
	if p, err := projectrepo.New(st).Get(ctx, wtKey); err != nil || p == nil || p.Root != wt {
		t.Fatalf("worktree project: %+v, %v", p, err)
	}

	// the main tree is untouched: its gate (the main agent) passes, and its
	// graph still has b without the import
	t.Setenv("CLAUDE_GATE_FILES", "")
	if out, e, err := run(main, "", "gate"); err != nil || out != "" {
		t.Fatalf("main gate after the worktree edit: out=%q err=%v\n%s", out, err, e)
	}
	if out, e, err := run(filepath.Join(main, "a"), "", "gate"); err != nil || out != "" {
		t.Fatalf("main gate from a subdirectory: out=%q err=%v\n%s", out, err, e)
	}

	// the stop check in the worktree sees the cycle without being told the files
	if _, _, err := run(wt, "", "gate"); !errors.As(err, &gf) {
		t.Errorf("worktree stop check passed with the cycle: %v", err)
	}

	// the agent commits, then undoes it with git (no edit the gate is told
	// about): the graph follows HEAD and the gate passes
	testGit(t, wt, "commit", "-qam", "cycle")
	if _, _, err := run(wt, "", "gate"); !errors.As(err, &gf) {
		t.Errorf("committed cycle passed: %v", err)
	}
	testGit(t, wt, "reset", "-q", "--hard", "HEAD~1")
	if out, e, err := run(wt, "", "gate"); err != nil || out != "" {
		t.Fatalf("gate after git reset: out=%q err=%v\n%s", out, err, e)
	}
	// and a new untracked file that brings the cycle back is found
	writeTree(t, wt, map[string]string{"b/b2.go": "package b\n\nimport \"example.com/m/a\"\n\n// B2 calls A.\nfunc B2() { a.A() }\n"})
	if _, _, err := run(wt, "", "gate"); !errors.As(err, &gf) {
		t.Errorf("untracked file with the cycle passed: %v", err)
	}
}

func TestGateRoot(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx := context.Background()
	main, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	other, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeTree(t, main, map[string]string{"a/a.go": "package a\n"})
	writeTree(t, other, map[string]string{"x.go": "package x\n"})
	for _, d := range []string{main, other} {
		testGit(t, d, "init", "-q")
		testGit(t, d, "add", ".")
		testGit(t, d, "commit", "-qm", "init")
	}
	wt := filepath.Join(main, ".claude", "worktrees", "feat")
	testGit(t, main, "worktree", "add", "-q", "-b", "feat", wt)
	plain := t.TempDir()
	tests := []struct {
		name, projectDir, cwd, want string
	}{
		{"no project dir", "", filepath.Join(main, "a"), filepath.Join(main, "a")},
		{"main tree", main, main, main},
		{"subdirectory of the main tree (dir: of a rule)", main, filepath.Join(main, "a"), main},
		{"linked worktree under the main tree", main, wt, wt},
		{"subdirectory of the worktree", main, filepath.Join(wt, "a"), wt},
		{"another repository", main, other, main},
		{"no git at cwd", main, plain, main},
	}
	for _, tt := range tests {
		if got := gateRoot(ctx, tt.projectDir, tt.cwd); got != tt.want {
			t.Errorf("%s: gateRoot = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestGateFiles(t *testing.T) {
	root := filepath.FromSlash("/r/wt")
	got := gateFiles(root, []string{"a/b.go", "./c.go", filepath.FromSlash("/r/wt/d/e.go"), filepath.FromSlash("/r/main/x.go")})
	if want := []string{"a/b.go", "c.go", "d/e.go"}; !slices.Equal(got, want) {
		t.Errorf("gateFiles = %q, want %q", got, want)
	}
}
