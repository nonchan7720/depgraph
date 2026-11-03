package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	graphrepo "github.com/nonchan7720/depgraph/pkg/infrastructure/repository/graph"
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
}
