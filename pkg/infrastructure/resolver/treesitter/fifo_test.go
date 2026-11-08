//go:build unix

package treesitter

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A FIFO in the repository (a pull request can add one) must not block the
// scan: not as an extensionless file that may be a Ruby script, as a .go
// file of an imported package, as package.json, or as the file parsed.
func TestFIFOsDoNotBlock(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/app\n")
	writeFile(t, root, "Gemfile", "source 'https://rubygems.org'\n")
	writeFile(t, root, "lib/x.go", "package lib\n")
	writeFile(t, root, "main.go", "package main\nimport \"example.com/app/lib\"\nvar _ = lib.X\n")
	writeFile(t, root, "app/models/user.rb", "class User; end\n")
	writeFile(t, root, "web/a.ts", "import x from 'y';\n")
	for _, f := range []string{"bin/tool", "lib/a.go", "web/package.json", "fifo.ts"} {
		p := filepath.Join(root, filepath.FromSlash(f))
		writeFile(t, root, f, "")
		_ = syscall.Unlink(p)
		if err := syscall.Mkfifo(p, 0o644); err != nil {
			t.Skipf("mkfifo: %v", err)
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		r := New()
		defer r.Close()
		for _, rel := range []string{"main.go", "app/models/user.rb", "web/a.ts"} {
			if _, err := r.ResolveFile(context.Background(), root, rel); err != nil {
				t.Errorf("%s: %v", rel, err)
			}
		}
		if _, err := r.ResolveFile(context.Background(), root, "fifo.ts"); err == nil {
			t.Error("a FIFO was parsed as a source file")
		}
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the scan blocked on a FIFO")
	}
}
