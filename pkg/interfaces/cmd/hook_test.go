package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHookNeverBlocksWhenNeo4jIsDown(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"hook", "session-start"},
		{"hook", "prompt"},
	} {
		var out, errOut bytes.Buffer
		root := newRootCommand(&options{})
		root.SetOut(&out)
		root.SetErr(&errOut)
		root.SetIn(strings.NewReader(`{"session_id":"s1","cwd":"` + dir + `","hook_event_name":"UserPromptSubmit","prompt":"hi"}`))
		// 何も listen していないポート。--timeout 内に必ず終わる。
		root.SetArgs(append(args, "--neo4j", "bolt://127.0.0.1:1", "--timeout", "2s"))
		start := time.Now()
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: err = %v (hook must exit 0)", args, err)
		}
		if out.Len() != 0 || errOut.Len() != 0 {
			t.Errorf("%v: stdout=%q stderr=%q, want nothing", args, out.String(), errOut.String())
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Errorf("%v took %s", args, d)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude")); !os.IsNotExist(err) {
		t.Error("nothing must be written when Neo4j is down")
	}
}

func TestHookIgnoresBrokenInput(t *testing.T) {
	var out bytes.Buffer
	root := newRootCommand(&options{})
	root.SetOut(&out)
	root.SetErr(&bytes.Buffer{})
	root.SetIn(strings.NewReader("{broken"))
	root.SetArgs([]string{"hook", "prompt", "--neo4j", "bolt://127.0.0.1:1", "--timeout", "1s"})
	if err := root.Execute(); err != nil || out.Len() != 0 {
		t.Errorf("err=%v out=%q", err, out.String())
	}
}

// post-edit works without Neo4j: an edited file importing something that
// does not exist gets a "block" decision naming the import; a clean file
// gets nothing.
func TestHookPostEditWithoutNeo4j(t *testing.T) {
	dir := t.TempDir()
	// $CLAUDE_PROJECT_DIR names another checkout (e.g. the main worktree while
	// the session edits a git worktree): the edited file's own cwd root wins.
	t.Setenv("CLAUDE_PROJECT_DIR", t.TempDir())
	write := func(rel, content string) string {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("package.json", `{"name":"app"}`)
	write("src/ok.ts", "export const ok = 1\n")
	bad := write("src/bad.ts", "import { x } from './missing'\nimport { ok } from './ok'\n")
	good := write("src/good.ts", "import { ok } from './ok'\n")
	run := func(file string) string {
		var out, errOut bytes.Buffer
		root := newRootCommand(&options{})
		root.SetOut(&out)
		root.SetErr(&errOut)
		root.SetIn(strings.NewReader(`{"session_id":"s1","cwd":"` + dir + `","hook_event_name":"PostToolUse","tool_name":"Edit","tool_input":{"file_path":"` + file + `"}}`))
		root.SetArgs([]string{"hook", "post-edit", "--neo4j", "bolt://127.0.0.1:1", "--timeout", "5s"})
		if err := root.Execute(); err != nil {
			t.Fatalf("err = %v (hook must exit 0)", err)
		}
		if errOut.Len() != 0 {
			t.Errorf("stderr = %q", errOut.String())
		}
		return out.String()
	}
	out := run(bad)
	for _, want := range []string{`"decision":"block"`, "src/bad.ts", `\"./missing\"`, "unresolved-import"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in %s", want, out)
		}
	}
	if out := run(good); out != "" {
		t.Errorf("clean file: %s", out)
	}
}
