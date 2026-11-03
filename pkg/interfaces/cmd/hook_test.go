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

func TestHookRejectsUnknownFormat(t *testing.T) {
	root := newRootCommand(&options{})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetIn(strings.NewReader(""))
	root.SetArgs([]string{"hook", "session-start", "--format", "yaml", "--neo4j", "bolt://127.0.0.1:1", "--timeout", "1s"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), `"yaml"`) {
		t.Fatalf("err = %v", err)
	}
}
