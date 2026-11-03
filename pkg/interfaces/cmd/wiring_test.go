package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nonchan7720/depgraph/pkg/infrastructure/claudemd"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver"
)

func TestReportWriter(t *testing.T) {
	root := filepath.FromSlash("/repo")
	tests := []struct {
		name     string
		claudeMD string
		want     string // "" means no writer
	}{
		{"disabled", "", ""},
		{"relative to root", "CLAUDE.md", filepath.Join(root, "CLAUDE.md")},
		{"absolute", filepath.FromSlash("/elsewhere/AGENTS.md"), filepath.FromSlash("/elsewhere/AGENTS.md")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := reportWriter(&options{root: root, claudeMD: tt.claudeMD})
			if tt.want == "" {
				if w != nil {
					t.Fatalf("reportWriter = %#v, want nil", w)
				}
				return
			}
			cw, ok := w.(claudemd.Writer)
			if !ok || cw.Path != tt.want {
				t.Fatalf("reportWriter = %#v, want claudemd.Writer{Path: %q}", w, tt.want)
			}
		})
	}
}

func TestCommandFlags(t *testing.T) {
	root := newRootCommand(&options{})
	has := map[string][]string{
		"scan":  {"neo4j", "user", "pass", "db", "claude-md", "reset"},
		"check": {"neo4j", "user", "pass", "db", "claude-md"},
		"watch": {"neo4j", "user", "pass", "db", "claude-md", "reset", "debounce", "addr", "no-ui", "no-lsp"},
		"serve": {"neo4j", "user", "pass", "db", "addr"},
		"hook":  {"neo4j", "user", "pass", "db", "timeout", "debug"},
	}
	lacks := map[string][]string{
		"serve": {"claude-md", "reset"}, // serve never writes CLAUDE.md
		"parse": {"neo4j", "claude-md"},
		"hook":  {"claude-md", "reset"}, // hook は読むだけ
	}
	for _, name := range []string{"root", "exclude", "ignore-file", "project"} {
		if root.PersistentFlags().Lookup(name) == nil {
			t.Errorf("root lacks persistent flag --%s", name)
		}
	}
	for _, c := range root.Commands() {
		for _, f := range has[c.Name()] {
			if c.Flags().Lookup(f) == nil {
				t.Errorf("%s lacks --%s", c.Name(), f)
			}
		}
		for _, f := range lacks[c.Name()] {
			if c.Flags().Lookup(f) != nil {
				t.Errorf("%s must not have --%s", c.Name(), f)
			}
		}
	}
}

func TestParseExcludeAndRootAbs(t *testing.T) {
	resolver.ResetSkipPatterns()
	t.Cleanup(resolver.ResetSkipPatterns)
	dir := t.TempDir()
	for rel, body := range map[string]string{
		"keep/a.go": "package keep\n",
		"gen/b.go":  "package gen\n",
	} {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)

	var out bytes.Buffer
	o := &options{}
	root := newRootCommand(o)
	root.SetOut(&out)
	root.SetArgs([]string{"parse", "--root", ".", "--exclude", "gen"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(o.root) {
		t.Errorf("root not absolute: %q", o.root)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("want one JSON line, got %q", out.String())
	}
	var fg struct{ Path string }
	if err := json.Unmarshal([]byte(lines[0]), &fg); err != nil || fg.Path != "keep/a.go" {
		t.Errorf("parse output = %q (err %v), want keep/a.go", lines[0], err)
	}
}

func TestParseFiles(t *testing.T) {
	resolver.ResetSkipPatterns()
	t.Cleanup(resolver.ResetSkipPatterns)
	dir := t.TempDir()
	p := filepath.Join(dir, "x", "c.go")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("package x\nfunc C() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	root := newRootCommand(&options{})
	root.SetOut(&out)
	root.SetArgs([]string{"parse", "--root", dir, p})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var fg struct{ Path string }
	if err := json.Unmarshal(out.Bytes(), &fg); err != nil || fg.Path != "x/c.go" {
		t.Errorf("parse output = %q (err %v), want x/c.go", out.String(), err)
	}
}
