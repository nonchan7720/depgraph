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

func TestNegationLiftsBuiltinSkipDir(t *testing.T) {
	t.Cleanup(ResetSkipPatterns)
	ResetSkipPatterns()
	AddSkipPatterns([]string{"!src/build"})
	if ShouldSkipDir("src/build", "build") {
		t.Error("src/build should be re-included")
	}
	if !ShouldSkipDir("build", "build") {
		t.Error("root build is still skipped")
	}
	if !ShouldSkipDir("other/build", "build") {
		t.Error("other/build is still skipped")
	}
	// a negation matches the directory itself, not its descendants
	if !ShouldSkipDir("src/build/node_modules", "node_modules") {
		t.Error("src/build/node_modules must stay skipped")
	}
	if ShouldSkipPath("src/build/startup/register.tsx") {
		t.Error("register.tsx under src/build should not be skipped")
	}
	if !ShouldSkipPath("src/build/node_modules/x.js") {
		t.Error("node_modules under src/build should be skipped")
	}
	if !ShouldSkipPath("build/x.js") {
		t.Error("root build should be skipped")
	}
}

func TestNegationNameAndAnchored(t *testing.T) {
	t.Cleanup(ResetSkipPatterns)
	ResetSkipPatterns()
	AddSkipPatterns([]string{"!build"})
	if ShouldSkipDir("a/build", "build") || ShouldSkipDir("build", "build") {
		t.Error("!build lifts every dir named build")
	}
	if !ShouldSkipDir("a/dist", "dist") {
		t.Error("dist stays skipped")
	}
	ResetSkipPatterns()
	AddSkipPatterns([]string{"!/build"})
	if ShouldSkipDir("build", "build") {
		t.Error("!/build lifts the root build")
	}
	if !ShouldSkipDir("a/build", "build") {
		t.Error("!/build must not lift a/build")
	}
}

func TestNegationOrderLastWins(t *testing.T) {
	t.Cleanup(ResetSkipPatterns)
	ResetSkipPatterns()
	AddSkipPatterns([]string{"!src/build", "src/build"})
	if !ShouldSkipDir("src/build", "build") {
		t.Error("later positive pattern wins over earlier negation")
	}
	ResetSkipPatterns()
	AddSkipPatterns([]string{"src/build", "!src/build"})
	if ShouldSkipDir("src/build", "build") {
		t.Error("later negation wins")
	}
}

func TestNegationFilePattern(t *testing.T) {
	t.Cleanup(ResetSkipPatterns)
	ResetSkipPatterns()
	AddSkipPatterns([]string{"*.gen.go", "!keep.gen.go"})
	if !ShouldSkipPath("a/x.gen.go") {
		t.Error("x.gen.go should be skipped")
	}
	if ShouldSkipPath("a/keep.gen.go") {
		t.Error("keep.gen.go should be re-included")
	}
}

func TestNegationDoubleStar(t *testing.T) {
	t.Cleanup(ResetSkipPatterns)
	ResetSkipPatterns()
	AddSkipPatterns([]string{"!src/**/build"})
	if ShouldSkipDir("src/a/b/build", "build") {
		t.Error("src/a/b/build should be re-included")
	}
	if !ShouldSkipDir("src/a/b/build/node_modules", "node_modules") {
		t.Error("node_modules under it stays skipped")
	}
}

func TestNegationCannotReincludeUnderExcludedParent(t *testing.T) {
	t.Cleanup(ResetSkipPatterns)
	ResetSkipPatterns()
	AddSkipPatterns([]string{"gen", "!gen/keep"})
	if !ShouldSkipDir("gen", "gen") || !ShouldSkipPath("gen/keep/x.go") {
		t.Error("a child of an excluded dir stays excluded (gitignore rule)")
	}
}

func TestEscapedBang(t *testing.T) {
	t.Cleanup(ResetSkipPatterns)
	ResetSkipPatterns()
	AddSkipPatterns([]string{`\!weird`})
	if !ShouldSkipDir("!weird", "!weird") {
		t.Error(`\! should match a literal leading !`)
	}
}
