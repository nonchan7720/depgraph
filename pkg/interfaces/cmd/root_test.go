package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver"
)

func TestSubcommands(t *testing.T) {
	root := newRootCommand(&options{})
	var names []string
	for _, c := range root.Commands() {
		if !c.Hidden && c.Name() != "help" && c.Name() != "completion" {
			names = append(names, c.Name())
		}
	}
	sort.Strings(names)
	want := []string{"check", "codex", "gate", "hook", "init", "mcp", "parse", "projects", "scan", "schema", "serve", "store", "version", "watch"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("subcommands = %v, want %v", names, want)
	}
}

func TestFlagDefaultsFromEnv(t *testing.T) {
	t.Setenv("NEO4J_URI", "bolt://example:7687")
	t.Setenv("DEPGRAPH_ADDR", "127.0.0.1:9999")
	o := &options{}
	root := newRootCommand(o)
	root.SetArgs([]string{"watch", "--help"})
	root.SetOut(&bytes.Buffer{})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if o.neo4jURI != "bolt://example:7687" || o.addr != "127.0.0.1:9999" {
		t.Errorf("env defaults not applied: %+v", o)
	}
	if o.debounce != 400*time.Millisecond || o.ignoreFile != ".depgraphignore" {
		t.Errorf("defaults: %+v", o)
	}
}

func TestParseHonoursIgnoreFileUnderRoot(t *testing.T) {
	resolver.ResetSkipPatterns()
	t.Cleanup(resolver.ResetSkipPatterns)
	dir := t.TempDir()
	write := func(rel, body string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("keep/a.go", "package keep\nfunc A() {}\n")
	write("skipme/b.go", "package skipme\n")
	write(".depgraphignore", "skipme\n")

	var out bytes.Buffer
	root := newRootCommand(&options{})
	root.SetOut(&out)
	root.SetArgs([]string{"parse", "--root", dir})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(&out)
	var paths []string
	for dec.More() {
		var fg struct{ Path string }
		if err := dec.Decode(&fg); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, fg.Path)
	}
	if strings.Join(paths, ",") != "keep/a.go" {
		t.Errorf("parse output paths = %v, want [keep/a.go]", paths)
	}
}
