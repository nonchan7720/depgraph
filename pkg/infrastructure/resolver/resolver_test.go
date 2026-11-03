package resolver

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultSkipDirs(t *testing.T) {
	ResetSkipPatterns()
	for name := range SkipDirs {
		if !ShouldSkipDir(name, name) {
			t.Errorf("%s should be skipped", name)
		}
	}
	if ShouldSkipDir("src", "src") {
		t.Error("src should not be skipped")
	}
	if !ShouldSkipPath("a/node_modules/x.go") {
		t.Error("node_modules path should be skipped")
	}
}

func TestNamePattern(t *testing.T) {
	ResetSkipPatterns()
	AddSkipPatterns([]string{"tmp*"})
	if !ShouldSkipDir("tmp1", "tmp1") || !ShouldSkipDir("a/b/tmp1", "tmp1") {
		t.Error("tmp1 dir should be skipped at any depth")
	}
	if !ShouldSkipPath("a/tmp1/x.go") {
		t.Error("a/tmp1/x.go should be skipped")
	}
	if ShouldSkipPath("a/other/x.go") {
		t.Error("a/other/x.go should not be skipped")
	}
}

func TestPathPrefixPattern(t *testing.T) {
	ResetSkipPatterns()
	AddSkipPatterns([]string{"/worktrees"})
	if !ShouldSkipPath("worktrees/feature/x.go") {
		t.Error("worktrees/feature/x.go should be skipped")
	}
	if ShouldSkipPath("src/worktrees/x.go") {
		t.Error("src/worktrees/x.go should not be skipped")
	}
}

func TestDoubleStar(t *testing.T) {
	ResetSkipPatterns()
	AddSkipPatterns([]string{"internal/**/testdata"})
	if !ShouldSkipPath("internal/a/b/testdata/x.go") {
		t.Error("deep testdata should be skipped")
	}
	if ShouldSkipPath("internal/a/b/other/x.go") {
		t.Error("other dir should not be skipped")
	}
}

func TestFileGlob(t *testing.T) {
	ResetSkipPatterns()
	AddSkipPatterns([]string{"*.gen.go"})
	if !ShouldSkipPath("pkg/foo.gen.go") {
		t.Error("foo.gen.go should be skipped")
	}
	if ShouldSkipPath("pkg/foo.go") {
		t.Error("foo.go should not be skipped")
	}
}

func TestIgnoreFilePath(t *testing.T) {
	cases := []struct{ root, file, want string }{
		{"/repo", ".depgraphignore", "/repo/.depgraphignore"},
		{"/repo", "conf/ignore", "/repo/conf/ignore"},
		{"/repo", "/etc/depgraph/ignore", "/etc/depgraph/ignore"},
		{"/repo", "", ""},
	}
	for _, c := range cases {
		if got := IgnoreFilePath(c.root, c.file); got != c.want {
			t.Errorf("IgnoreFilePath(%q, %q) = %q, want %q", c.root, c.file, got, c.want)
		}
	}
}

func TestLoadIgnoreFileFromRoot(t *testing.T) {
	ResetSkipPatterns()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".depgraphignore"), []byte("# c\nskipme\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := LoadIgnoreFile(IgnoreFilePath(root, ".depgraphignore")); err != nil {
		t.Fatal(err)
	}
	if !ShouldSkipPath("skipme/x.go") {
		t.Error("skipme/x.go should be skipped by root's .depgraphignore")
	}
	if ShouldSkipPath("keep/x.go") {
		t.Error("keep/x.go should not be skipped")
	}
}
