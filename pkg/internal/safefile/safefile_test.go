package safefile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRead(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	write(t, filepath.Join(root, ".python-version"), "3.12\n")
	write(t, filepath.Join(outside, "dotfiles", ".tool-versions"), "python 3.11.9\n")
	write(t, filepath.Join(root, "big"), strings.Repeat("x", 100))
	// dotfiles managers link files, and whole directories, out of the repository
	if err := os.Symlink(filepath.Join(outside, "dotfiles", ".tool-versions"), filepath.Join(root, ".tool-versions")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "dotfiles"), filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("loop-b", filepath.Join(root, "loop-a")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("loop-a", filepath.Join(root, "loop-b")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/dev/zero", filepath.Join(root, "zero")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		rel, want string
	}{
		{".python-version", "3.12\n"},
		{"./.python-version", "3.12\n"},
		{".tool-versions", "python 3.11.9\n"},
		{"linked/.tool-versions", "python 3.11.9\n"},
	} {
		b, info, err := Read(root, tc.rel, 64)
		if err != nil || string(b) != tc.want || info.Real == "" {
			t.Errorf("Read(%q) = %q, %+v, %v", tc.rel, b, info, err)
		}
	}

	for _, tc := range []struct{ rel, want string }{
		{"", "empty path"},
		{"/etc/passwd", "relative"},
		{"../x", "outside the repository"},
		{"a/../../x", "outside the repository"},
		{"missing", "missing"},
		{"loop-a", "loop-a"},
		{"zero", "not a regular file"},
		{"dir", "not a regular file"},
		{"big", "larger than 64 bytes"},
	} {
		_, _, err := Read(root, tc.rel, 64)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("Read(%q) err = %v, want %q", tc.rel, err, tc.want)
		}
		if missing := tc.rel == "missing"; err != nil && errors.Is(err, os.ErrNotExist) != missing {
			t.Errorf("Read(%q): errors.Is(err, os.ErrNotExist) = %v", tc.rel, !missing)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, ErrRefused) {
			t.Errorf("Read(%q): %v wraps neither ErrNotExist nor ErrRefused", tc.rel, err)
		}
		if err != nil && strings.Contains(err.Error(), outside) {
			t.Errorf("Read(%q): the error names the real path: %v", tc.rel, err)
		}
	}
}

// A FIFO must not block the scan (an attacker's symlink in a pull request).
func TestReadFIFO(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no FIFOs")
	}
	root := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(root, "fifo"), 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan error, 1)
	go func() { _, _, err := Read(root, "fifo", 64); done <- err }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Errorf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Read blocked on a FIFO")
	}
}

// A pseudo file that reports size 0 but has content (/proc/self/environ)
// is not a file on disk.
func TestReadPseudoFile(t *testing.T) {
	if _, err := os.Stat("/proc/self/environ"); err != nil {
		t.Skip("no /proc")
	}
	root := t.TempDir()
	if err := os.Symlink("/proc/self/environ", filepath.Join(root, "env")); err != nil {
		t.Fatal(err)
	}
	_, _, err := Read(root, "env", 1<<20)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("err = %v", err)
	}
	if err != nil && errors.Is(err, os.ErrNotExist) {
		t.Errorf("err = %v", err)
	}
}

// The fingerprint changes when the target changes, even outside the
// repository, where no file event of the repository reports it.
func TestInfoChanges(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "v")
	write(t, target, "3.12\n")
	if err := os.Symlink(target, filepath.Join(root, "v")); err != nil {
		t.Fatal(err)
	}
	_, a, err := Read(root, "v", 64)
	if err != nil {
		t.Fatal(err)
	}
	if !Unchanged(root, "v", a) {
		t.Error("unchanged file reported as changed")
	}
	write(t, target, "3.13.1\n")
	if Unchanged(root, "v", a) {
		t.Error("a changed target outside the repository is reported unchanged")
	}
}

// On Unix a backslash is a character of a file name, not a separator.
func TestReadBackslashName(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a separator on Windows")
	}
	root := t.TempDir()
	write(t, filepath.Join(root, `a\b.go`), "package a\n")
	if b, _, err := Read(root, `a\b.go`, 64); err != nil || string(b) != "package a\n" {
		t.Errorf("Read = %q, %v", b, err)
	}
}
