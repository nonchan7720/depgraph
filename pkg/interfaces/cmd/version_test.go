package cmd

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/nonchan7720/depgraph/pkg/version"
)

// setBuildInfo overrides the ldflags-injected variables for one test.
func setBuildInfo(t *testing.T, v, commit, date string) {
	t.Helper()
	oldV, oldC, oldD := version.Version, version.Commit, version.Date
	version.Version, version.Commit, version.Date = v, commit, date
	t.Cleanup(func() { version.Version, version.Commit, version.Date = oldV, oldC, oldD })
}

func runRoot(t *testing.T, args ...string) string {
	t.Helper()
	root := newRootCommand(&options{})
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	if err := root.Execute(); err != nil {
		t.Fatalf("depgraph %s: %v", strings.Join(args, " "), err)
	}
	return out.String()
}

func TestRootVersionIsSet(t *testing.T) {
	setBuildInfo(t, "1.2.3", "abc1234", "2026-01-02T03:04:05Z")
	root := newRootCommand(&options{})
	if root.Version != "1.2.3" {
		t.Errorf("root.Version = %q, want %q", root.Version, "1.2.3")
	}
}

func TestVersionFlag(t *testing.T) {
	setBuildInfo(t, "1.2.3", "abc1234", "2026-01-02T03:04:05Z")
	got := runRoot(t, "--version")
	want := "depgraph 1.2.3 (commit abc1234, built 2026-01-02T03:04:05Z)\n"
	if got != want {
		t.Errorf("--version = %q, want %q", got, want)
	}
}

func TestVersionCommand(t *testing.T) {
	setBuildInfo(t, "1.2.3", "abc1234", "2026-01-02T03:04:05Z")
	got := runRoot(t, "version")
	want := "depgraph 1.2.3\n" +
		"commit: abc1234\n" +
		"built:  2026-01-02T03:04:05Z\n" +
		"go:     " + runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH + "\n"
	if got != want {
		t.Errorf("version =\n%s\nwant\n%s", got, want)
	}
}

// version must work outside a repository: it skips the root / ignore-file setup.
func TestVersionCommandIgnoresRoot(t *testing.T) {
	setBuildInfo(t, "1.2.3", "abc1234", "2026-01-02T03:04:05Z")
	got := runRoot(t, "version", "--root", "/does/not/exist", "--ignore-file", "/does/not/exist/.depgraphignore")
	if !strings.HasPrefix(got, "depgraph 1.2.3\n") {
		t.Errorf("version = %q", got)
	}
}
