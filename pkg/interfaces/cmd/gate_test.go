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

	"github.com/nonchan7720/depgraph/pkg/domain/project"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/neo4jprovider"
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

const (
	bClean = "package b\n\n// B does nothing.\nfunc B() {}\n"
	bCycle = "package b\n\nimport \"example.com/m/a\"\n\n// B calls A.\nfunc B() { a.A() }\n"
)

// wtFixture is a scanned main tree with a linked worktree under
// .claude/worktrees/feat, on the Neo4j of $NEO4J_URI.
type wtFixture struct {
	ctx        context.Context
	st         *graphrepo.Store
	main, wt   string
	key, wtKey string
	projects   *projectrepo.Projects
	t          *testing.T
}

func newWTFixture(t *testing.T) *wtFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
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
	f := &wtFixture{ctx: ctx, st: st, main: main, projects: projectrepo.New(st), t: t,
		key: fmt.Sprintf("zz_test_%d_gatewt", time.Now().UnixNano())}
	f.wtKey = f.key + "@feat"
	t.Cleanup(func() {
		_ = f.projects.Delete(context.Background(), f.key)
		_ = f.projects.Delete(context.Background(), f.wtKey)
	})
	writeTree(t, main, map[string]string{
		"go.mod":  "module example.com/m\n\ngo 1.22\n",
		"main.go": "package main\n\nimport (\n\t\"example.com/m/a\"\n\t\"example.com/m/c\"\n)\n\nfunc main() { a.A(); c.C() }\n",
		"a/a.go":  "package a\n\nimport \"example.com/m/b\"\n\n// A calls B.\nfunc A() { b.B() }\n",
		"b/b.go":  bClean,
		// debt committed before the session: c <-> d
		"c/c.go":        "package c\n\nimport \"example.com/m/d\"\n\n// C calls D.\nfunc C() { d.D() }\n",
		"d/d.go":        "package d\n\nimport \"example.com/m/c\"\n\n// D calls C.\nfunc D() { c.C() }\n",
		".depgraph.yml": "project: " + f.key + "\nrules:\n  duplicate-symbol: {enabled: false}\n",
		".gitignore":    ".claude/\n",
	})
	testGit(t, main, "init", "-q", "-b", "main")
	testGit(t, main, "add", ".")
	testGit(t, main, "commit", "-qm", "init")
	f.wt = filepath.Join(main, ".claude", "worktrees", "feat")
	testGit(t, main, "worktree", "add", "-q", "-b", "feat", f.wt)
	t.Setenv("CLAUDE_PROJECT_DIR", main)
	t.Setenv("CLAUDE_SESSION_ID", "s1")
	t.Setenv("CLAUDE_GATE_FILES", "")
	if _, e, err := f.run(main, "scan", "--claude-md", ""); err != nil {
		t.Fatalf("scan: %v\n%s", err, e)
	}
	if _, e, err := f.run(main, "hook", "session-start", "--format", "text", "--timeout", "20s"); err != nil {
		t.Fatalf("session-start: %v\n%s", err, e)
	}
	return f
}

// run runs depgraph in cwd as claude-hook-gate does: no --root, the root
// comes from the environment and the working directory.
func (f *wtFixture) run(cwd string, args ...string) (string, string, error) {
	f.t.Helper()
	f.t.Chdir(cwd)
	var out, errOut bytes.Buffer
	c := newRootCommand(&options{})
	c.SetOut(&out)
	c.SetErr(&errOut)
	c.SetIn(strings.NewReader(""))
	c.SetArgs(args)
	err := c.ExecuteContext(f.ctx)
	return out.String(), errOut.String(), err
}

// gate runs the gate in cwd with files as $CLAUDE_GATE_FILES and reports
// whether it failed; any other error fails the test.
func (f *wtFixture) gate(cwd, files string) (bool, string) {
	f.t.Helper()
	f.t.Setenv("CLAUDE_GATE_FILES", files)
	out, e, err := f.run(cwd, "gate")
	var gf *gateFailure
	switch {
	case err == nil && out == "":
		return false, e
	case errors.As(err, &gf):
		return true, out
	}
	f.t.Fatalf("gate in %s: out=%q err=%v\n%s", cwd, out, err, e)
	return false, ""
}

// The gate as claude-hook-gate runs it for an agent working in a linked
// worktree under the main tree: the working directory is the worktree,
// $CLAUDE_PROJECT_DIR is still the main tree and $CLAUDE_GATE_FILES are
// relative to the worktree. Only the main tree is scanned.
func TestGateInLinkedWorktree(t *testing.T) {
	f := newWTFixture(t)
	main, wt := f.main, f.wt

	// the agent adds a cycle in the worktree
	writeTree(t, wt, map[string]string{"b/b.go": bCycle})
	failed, out := f.gate(wt, "b/b.go")
	if !failed || !strings.Contains(out, "a -> b") && !strings.Contains(out, "b -> a") {
		t.Fatalf("worktree gate did not report the cycle: %q", out)
	}
	if strings.Contains(out, "c ->") || strings.Contains(out, "d ->") {
		t.Errorf("debt committed before the session reported:\n%s", out)
	}
	if p, err := f.projects.Get(f.ctx, f.wtKey); err != nil || p == nil || p.Root != wt {
		t.Fatalf("worktree project: %+v, %v", p, err)
	}

	// the main tree is untouched: its gate (the main agent) passes, also
	// from a subdirectory
	if failed, out := f.gate(main, ""); failed {
		t.Fatalf("main gate after the worktree edit: %q", out)
	}
	if failed, out := f.gate(filepath.Join(main, "a"), ""); failed {
		t.Fatalf("main gate from a subdirectory: %q", out)
	}

	// the stop check in the worktree sees the cycle without being told the files
	if failed, _ := f.gate(wt, ""); !failed {
		t.Error("worktree stop check passed with the cycle")
	}
	// the agent commits, then undoes it with git (no edit the gate is told
	// about): the graph follows HEAD and the gate passes
	testGit(t, wt, "commit", "-qam", "cycle")
	if failed, _ := f.gate(wt, ""); !failed {
		t.Error("committed cycle passed")
	}
	testGit(t, wt, "reset", "-q", "--hard", "HEAD~1")
	if failed, out := f.gate(wt, ""); failed {
		t.Fatalf("gate after git reset: %q", out)
	}
	// a new untracked file that brings the cycle back is found
	writeTree(t, wt, map[string]string{"b/b2.go": "package b\n\nimport \"example.com/m/a\"\n\n// B2 calls A.\nfunc B2() { a.A() }\n"})
	if failed, _ := f.gate(wt, ""); !failed {
		t.Error("untracked file with the cycle passed")
	}
}

// Debt that reaches the worktree through git from upstream (another agent's
// work merged into main, then merged or rebased in here) is not this
// session's: neither for the session that was running, nor for a new one.
func TestGateWorktreeUpstreamDebtIsNotTheSessions(t *testing.T) {
	f := newWTFixture(t)
	main, wt := f.main, f.wt
	// the session's first run builds the worktree project
	writeTree(t, wt, map[string]string{"a/a.go": "package a\n\nimport \"example.com/m/b\"\n\n// A calls B, documented.\nfunc A() { b.B() }\n"})
	if failed, out := f.gate(wt, "a/a.go"); failed {
		t.Fatalf("clean edit failed: %q", out)
	}
	testGit(t, wt, "add", ".")
	testGit(t, wt, "commit", "-qm", "doc")

	// another agent's cycle lands on main
	writeTree(t, main, map[string]string{"b/b.go": bCycle})
	testGit(t, main, "commit", "-qam", "cycle from elsewhere")
	testGit(t, wt, "merge", "-q", "--no-edit", "main")
	if failed, out := f.gate(wt, ""); failed {
		t.Fatalf("merged upstream debt blamed on the session: %q", out)
	}
	// a session that starts now does not get it either
	t.Setenv("CLAUDE_SESSION_ID", "s2")
	if failed, out := f.gate(wt, ""); failed {
		t.Fatalf("new session blamed for upstream debt: %q", out)
	}
	// but what the session adds itself still fails
	writeTree(t, wt, map[string]string{"e/e.go": "package e\n\nimport \"example.com/m/f\"\n\n// E calls F.\nfunc E() { f.F() }\n",
		"f/f.go": "package f\n\nimport \"example.com/m/e\"\n\n// F calls E.\nfunc F() { e.E() }\n"})
	failed, out := f.gate(wt, "e/e.go f/f.go")
	if !failed || !strings.Contains(out, "e -> f") && !strings.Contains(out, "f -> e") || strings.Contains(out, "a -> b") {
		t.Fatalf("own cycle: failed=%v %q", failed, out)
	}
	// an edit to a dirty file that did not change is not applied again
	p, err := f.projects.Get(f.ctx, f.wtKey)
	if err != nil || p == nil || len(p.Dirty) != 2 || p.Dirty[0].Hash == "-" {
		t.Fatalf("dirty state: %+v, %v", p, err)
	}
}

// What the agent commits itself is its own, also when no gate run saw the
// edit (edited and committed in one Bash command), before or after the
// worktree's first gate run.
func TestGateWorktreeOwnCommits(t *testing.T) {
	f := newWTFixture(t)
	// committed before the first gate run: the graph is built from the base
	writeTree(t, f.wt, map[string]string{"b/b.go": bCycle})
	testGit(t, f.wt, "commit", "-qam", "cycle")
	if failed, _ := f.gate(f.wt, ""); !failed {
		t.Fatal("cycle committed before the first gate run passed")
	}
	testGit(t, f.wt, "revert", "--no-edit", "HEAD")
	if failed, out := f.gate(f.wt, ""); failed {
		t.Fatalf("after the revert: %q", out)
	}
	// edited and committed in one step after it
	writeTree(t, f.wt, map[string]string{"b/b.go": bCycle})
	testGit(t, f.wt, "commit", "-qam", "cycle again")
	if failed, _ := f.gate(f.wt, ""); !failed {
		t.Fatal("cycle edited and committed in one step passed")
	}
}

// A merge of upstream that conflicts in the very file that brings the other
// agent's cycle: while it is in progress the gate passes (the incoming
// changes are staged as if the agent made them); once committed, the
// upstream cycle is not the agent's, and later edits are checked as usual.
func TestGateWorktreeConflictingMerge(t *testing.T) {
	f := newWTFixture(t)
	writeTree(t, f.wt, map[string]string{"b/b.go": "package b\n\n// B does nothing, on purpose.\nfunc B() {}\n"})
	if failed, out := f.gate(f.wt, "b/b.go"); failed {
		t.Fatalf("clean edit: %q", out)
	}
	testGit(t, f.wt, "commit", "-qam", "doc b")
	// upstream: another agent's cycle, in b/b.go
	writeTree(t, f.main, map[string]string{"b/b.go": bCycle})
	testGit(t, f.main, "commit", "-qam", "cycle from elsewhere")
	cmd := exec.Command("git", "-C", f.wt, "merge", "main")
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t",
		"GIT_COMMITTER_EMAIL=t@e", "GIT_CONFIG_GLOBAL=/dev/null")
	if err := cmd.Run(); err == nil {
		t.Fatal("merge should conflict")
	}
	// the agent resolves the conflict keeping both sides: the gate passes and says why
	writeTree(t, f.wt, map[string]string{"b/b.go": "package b\n\nimport \"example.com/m/a\"\n\n// B calls A, on purpose.\nfunc B() { a.A() }\n"})
	failed, e := f.gate(f.wt, "b/b.go")
	if failed || !strings.Contains(e, "merge is in progress") {
		t.Fatalf("gate during the merge: failed=%v %q", failed, e)
	}
	testGit(t, f.wt, "commit", "-qam", "merge main")
	if failed, out := f.gate(f.wt, ""); failed {
		t.Fatalf("upstream cycle blamed on the agent after the merge: %q", out)
	}
	// the agent's own debt after the merge still fails
	writeTree(t, f.wt, map[string]string{"e/e.go": "package e\n\nimport \"example.com/m/f\"\n\n// E calls F.\nfunc E() { f.F() }\n",
		"f/f.go": "package f\n\nimport \"example.com/m/e\"\n\n// F calls E.\nfunc F() { e.E() }\n"})
	if failed, out := f.gate(f.wt, "e/e.go f/f.go"); !failed || strings.Contains(out, "a -> b") {
		t.Fatalf("own cycle after the merge: failed=%v %q", failed, out)
	}
}

// A clean rebase onto main, where main's change (another agent's cycle) and
// the agent's own commit touch the same file: the rebased commit is the
// agent's, but the cycle came with main and is not.
func TestGateWorktreeRebaseSameFile(t *testing.T) {
	f := newWTFixture(t)
	long := func(top, z string) string {
		return "package b\n\n" + top + "\n\n\n\n\n\n// " + z + "\nfunc Z() {}\n"
	}
	clean := "// B does nothing.\nfunc B() {}"
	cycle := "import \"example.com/m/a\"\n\n// B calls A.\nfunc B() { a.A() }"
	writeTree(t, f.main, map[string]string{"b/b.go": long(clean, "Z is the end.")})
	testGit(t, f.main, "commit", "-qam", "longer b")
	testGit(t, f.wt, "merge", "-q", "--ff-only", "main")
	if failed, out := f.gate(f.wt, ""); failed {
		t.Fatalf("after the fast-forward: %q", out)
	}
	// the agent's commit: the comment of Z
	writeTree(t, f.wt, map[string]string{"b/b.go": long(clean, "Z is the end, really.")})
	if failed, out := f.gate(f.wt, "b/b.go"); failed {
		t.Fatalf("own edit: %q", out)
	}
	testGit(t, f.wt, "commit", "-qam", "doc Z")
	if failed, out := f.gate(f.wt, ""); failed {
		t.Fatalf("own commit: %q", out)
	}
	// main: another agent's cycle at the top of the same file
	writeTree(t, f.main, map[string]string{"b/b.go": long(cycle, "Z is the end.")})
	testGit(t, f.main, "commit", "-qam", "cycle from elsewhere")
	testGit(t, f.wt, "rebase", "-q", "main")
	if failed, out := f.gate(f.wt, ""); failed {
		t.Fatalf("upstream cycle blamed on the agent after the rebase: %q", out)
	}
	// the agent's own debt still fails
	writeTree(t, f.wt, map[string]string{"e/e.go": "package e\n\nimport \"example.com/m/f\"\n\n// E calls F.\nfunc E() { f.F() }\n",
		"f/f.go": "package f\n\nimport \"example.com/m/e\"\n\n// F calls E.\nfunc F() { e.E() }\n"})
	if failed, out := f.gate(f.wt, "e/e.go f/f.go"); !failed || strings.Contains(out, "a -> b") {
		t.Fatalf("own cycle after the rebase: failed=%v %q", failed, out)
	}
}

// While another process holds the worktree's sync lease (the first build of
// a large repository running in the background), the gate passes and says
// why; once it is released the gate checks again.
func TestGateWorktreeWaitsForBuild(t *testing.T) {
	f := newWTFixture(t)
	writeTree(t, f.wt, map[string]string{"b/b.go": bCycle})
	p := project.Project{Key: f.wtKey, Name: "feat", Root: f.wt, Base: f.key, Worktree: "feat"}
	now := time.Now()
	if ok, err := f.projects.ClaimSync(f.ctx, p, now, now.Add(time.Hour)); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	t.Setenv("CLAUDE_GATE_FILES", "b/b.go")
	start := time.Now()
	out, e, err := f.run(f.wt, "gate", "--timeout", "3s")
	if err != nil || out != "" || !strings.Contains(e, "background") {
		t.Fatalf("gate while building: out=%q err=%v stderr=%q", out, err, e)
	}
	if d := time.Since(start); d > 6*time.Second {
		t.Errorf("took %s", d)
	}
	if err := f.projects.ReleaseSync(f.ctx, f.wtKey); err != nil {
		t.Fatal(err)
	}
	if failed, _ := f.gate(f.wt, "b/b.go"); !failed {
		t.Error("cycle passed after the build")
	}
}

// A Claude session started inside the worktree ($CLAUDE_PROJECT_DIR is the
// worktree) checks the worktree too, with the main tree's store.
func TestGateSessionStartedInWorktree(t *testing.T) {
	f := newWTFixture(t)
	t.Setenv("CLAUDE_PROJECT_DIR", f.wt)
	writeTree(t, f.wt, map[string]string{"b/b.go": bCycle})
	if failed, out := f.gate(f.wt, "b/b.go"); !failed || !strings.Contains(out, "a -> b") && !strings.Contains(out, "b -> a") {
		t.Fatalf("worktree session: failed=%v %q", failed, out)
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
	got := gateFiles(root, []string{"a/b.go", "./c.go", filepath.FromSlash("/r/wt/d/e.go"), filepath.FromSlash("/r/main/x.go"), "../main/y.go", "a/../../z.go"})
	if want := []string{"a/b.go", "c.go", "d/e.go"}; !slices.Equal(got, want) {
		t.Errorf("gateFiles = %q, want %q", got, want)
	}
}

// Only projects of this main tree's own removed worktrees are pruned, never
// another clone's or machine's (same base, other origin or root elsewhere).
func TestPruneGoneWorktrees(t *testing.T) {
	main := t.TempDir()
	live := filepath.Join(main, ".claude", "worktrees", "live")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(main, ".claude", "worktrees", "gone")
	repo := fakeProjects{}
	for _, p := range []project.Project{
		{Key: "app", Base: "app", Root: main, Origin: "o"},
		{Key: "app@live", Base: "app", Worktree: "live", Root: live, Origin: "o"},
		{Key: "app@gone", Base: "app", Worktree: "gone", Root: gone, Origin: "o"},
		{Key: "app@elsewhere", Base: "app", Worktree: "x", Root: "/other/machine/app/.claude/worktrees/x", Origin: "o"},
		{Key: "app@fork", Base: "app", Worktree: "f", Root: filepath.Join(main, "f"), Origin: "fork"},
		{Key: "other@gone", Base: "other", Worktree: "gone", Root: gone, Origin: "o"},
	} {
		repo[p.Key] = p
	}
	pruneGoneWorktrees(context.Background(), repo, repo["app@live"], main)
	var keys []string
	for k := range repo {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if want := []string{"app", "app@elsewhere", "app@fork", "app@live", "other@gone"}; !slices.Equal(keys, want) {
		t.Errorf("left %q, want %q", keys, want)
	}
}

// fakeDiscover is a provider whose Discover knows a set of roots.
type fakeDiscover struct {
	name  string
	roots map[string]bool
}

func (f fakeDiscover) Name() string                    { return f.name }
func (f fakeDiscover) Available(context.Context) error { return nil }
func (f fakeDiscover) Provide(context.Context, neo4jprovider.StoreRequest) (neo4jprovider.Conn, error) {
	return neo4jprovider.Conn{}, errors.New("not used")
}
func (f fakeDiscover) Discover(_ context.Context, root string) (neo4jprovider.Conn, bool, error) {
	return neo4jprovider.Conn{URI: "bolt://" + root}, f.roots[root], nil
}

func TestDiscoverStore(t *testing.T) {
	ctx := context.Background()
	p := fakeDiscover{name: neo4jprovider.Container, roots: map[string]bool{"/main": true}}
	if c, live, err := discoverStore(ctx, p, "/main", nil); err != nil || !live || c.URI != "bolt:///main" {
		t.Errorf("own store: %+v live=%v err=%v", c, live, err)
	}
	// a worktree without its own store uses the main tree's; not live
	if c, live, err := discoverStore(ctx, p, "/main/.claude/worktrees/x", []string{"", "/nope", "/main"}); err != nil || live || c.URI != "bolt:///main" {
		t.Errorf("fallback: %+v live=%v err=%v", c, live, err)
	}
	if _, _, err := discoverStore(ctx, p, "/x", []string{"/y"}); err == nil {
		t.Error("no store: error expected")
	}
	// an external server serves every root but tells nothing about watching
	ext := fakeDiscover{name: neo4jprovider.External, roots: map[string]bool{"/w": true}}
	if _, live, _ := discoverStore(ctx, ext, "/w", nil); live {
		t.Error("external store counted as live")
	}
}
