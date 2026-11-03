package treesitter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A generated file may start with a comment line longer than
// bufio.Scanner's 64KB limit; the package clause after it must still be read.
func TestGoPackageNameAfterLongLine(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "gen")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := "// " + strings.Repeat("x", 70*1024) + "\npackage generated\n"
	if err := os.WriteFile(filepath.Join(dir, "gen.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := goPackageName(root, "gen"); got != "generated" {
		t.Fatalf("goPackageName = %q, want %q", got, "generated")
	}
}
