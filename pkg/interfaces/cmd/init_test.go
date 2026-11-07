package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runInit(t *testing.T, root, stdin string, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	c := newRootCommand(&options{})
	c.SetArgs(append([]string{"init", "--root", root, "--ignore-file", ""}, args...))
	c.SetOut(&out)
	c.SetErr(&out)
	c.SetIn(strings.NewReader(stdin))
	if err := c.Execute(); err != nil {
		t.Fatalf("init: %v\n%s", err, out.String())
	}
	return out.String()
}

func writeTreeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// init writes the version file of every directory that pins one, keeps the
// rest of .depgraph.yml, asks a person only where the files of a directory
// disagree (and leaves it out without a terminal), and says when no
// typeshed copy is there to classify with.
func TestInitPython(t *testing.T) {
	root := t.TempDir()
	writeTreeFiles(t, root, map[string]string{
		".depgraph.yml":           "# keep me\nlayers:\n  domain: [\"app/domain/**\"]\n",
		".python-version":         "3.12\n",
		"app/domain/x.py":         "",
		"lib/foo/.python-version": "3.9\n",
		"lib/foo/foo.py":          "",
		"svc/.python-version":     "3.10\n",
		"svc/Dockerfile":          "FROM python:3.11\n",
		"svc/s.py":                "",
	})
	dry := runInit(t, root, "", "--dry-run")
	if b, _ := os.ReadFile(filepath.Join(root, ".depgraph.yml")); strings.Contains(string(b), "languages") {
		t.Fatal("--dry-run wrote the file")
	}
	if !strings.Contains(dry, "versionFile: .python-version") {
		t.Errorf("dry run output:\n%s", dry)
	}

	out := runInit(t, root, "") // no terminal: svc is left out
	for _, want := range []string{"svc: .python-version pins 3.10, Dockerfile pins 3.11", "left out", "no typeshed stdlib found"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	b, err := os.ReadFile(filepath.Join(root, ".depgraph.yml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"# keep me", "domain:", "python: {versionFile: .python-version}", "lib/foo:"} {
		if !strings.Contains(s, want) {
			t.Errorf(".depgraph.yml lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "svc:") {
		t.Errorf("an undecided directory was written:\n%s", s)
	}

	// a second run only proposes what is left; a person picks for svc
	withTerminal(t, true)
	out = runInit(t, root, "2\n")
	b, _ = os.ReadFile(filepath.Join(root, ".depgraph.yml"))
	if !strings.Contains(string(b), "svc:") || !strings.Contains(string(b), "versionFile: Dockerfile") {
		t.Errorf("the choice was not written:\n%s\n%s", b, out)
	}
	if strings.Count(string(b), "lib/foo:") != 1 {
		t.Errorf("a setting was added twice:\n%s", b)
	}
}

func withTerminal(t *testing.T, v bool) {
	old := stdinIsTerminal
	stdinIsTerminal = func(any) bool { return v }
	t.Cleanup(func() { stdinIsTerminal = old })
}
