// Package safefile reads small configuration files of the scanned project
// (a version file, a type checker's data) that a user or a pull request
// points depgraph at. The path is the project's, the content may be anyone's:
//
//   - the path is relative to the root and lexically inside it (no absolute
//     path, no ".." out of it);
//   - symlinks are followed (dotfiles managers link files and directories out
//     of the repository), loops fail;
//   - the target must be a regular file on disk of at most a given size: a
//     device, a FIFO (opened without blocking), a directory or a pseudo file
//     whose content does not match its size (/proc/self/environ) is refused;
//   - errors name the relative path only, never the real path or any content.
//
// Nothing is executed and nothing is written.
package safefile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// ErrRefused wraps every refusal other than a missing file (which wraps
// os.ErrNotExist, so callers keep telling "not there" apart).
var ErrRefused = errors.New("refused")

// Source and Manifest are the size caps of a source file and of a manifest
// or configuration file.
const (
	Source   = 64 << 20
	Manifest = 8 << 20
)

func refused(rel, why string) error { return fmt.Errorf("%s: %s: %w", rel, why, ErrRefused) }

// Info identifies what was read, to tell later whether it changed (a target
// outside the repository changes without any file event inside it).
type Info struct {
	Real    string // the resolved path (never shown to users)
	Size    int64
	ModTime time.Time
	ID      fileID // device and inode where the platform has them
}

// Read reads rel (slash-separated, relative to root) under the rules of
// the package doc, at most max bytes.
func Read(root, rel string, max int64) ([]byte, Info, error) {
	clean, err := local(rel)
	if err != nil {
		return nil, Info{}, err
	}
	return readResolved(root, clean, clean, max)
}

// readResolved reads root/clean; errors name shown.
func readResolved(root, clean, shown string, max int64) ([]byte, Info, error) {
	real, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(clean)))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, Info{}, fmt.Errorf("%s: missing: %w", shown, os.ErrNotExist)
		}
		return nil, Info{}, refused(shown, "cannot be resolved (a symlink loop?)")
	}
	f, err := openNoBlock(real)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, Info{}, fmt.Errorf("%s: missing: %w", shown, os.ErrNotExist)
		}
		return nil, Info{}, refused(shown, "cannot be opened")
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return nil, Info{}, refused(shown, "not a regular file")
	}
	if st.Size() > max {
		return nil, Info{}, refused(shown, fmt.Sprintf("larger than %d bytes", max))
	}
	var b bytes.Buffer
	if _, err := io.Copy(&b, io.LimitReader(f, max+1)); err != nil {
		return nil, Info{}, refused(shown, "cannot be read")
	}
	if int64(b.Len()) != st.Size() {
		// a pseudo file (size 0, content generated on read) or one being
		// rewritten: not a file on disk
		return nil, Info{}, refused(shown, "not a regular file (its content does not match its size)")
	}
	return b.Bytes(), Info{Real: real, Size: st.Size(), ModTime: st.ModTime(), ID: idOf(st)}, nil
}

// Unchanged reports whether rel still resolves to what info describes.
func Unchanged(root, rel string, info Info) bool {
	clean, err := local(rel)
	if err != nil {
		return false
	}
	real, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(clean)))
	if err != nil || real != info.Real {
		return false
	}
	st, err := os.Stat(real)
	return err == nil && st.Size() == info.Size && st.ModTime().Equal(info.ModTime) && idOf(st) == info.ID
}

// local cleans rel and checks it stays inside the root lexically.
func local(rel string) (string, error) {
	if rel == "" {
		return "", fmt.Errorf("empty path: %w", ErrRefused)
	}
	// a backslash is a path separator only on Windows; elsewhere it is a
	// valid character of a file name
	if strings.HasPrefix(rel, "/") || filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" ||
		(filepath.Separator == '\\' && strings.Contains(rel, `\`)) {
		return "", refused(rel, "want a path relative to the repository (slash-separated)")
	}
	clean := path.Clean(rel)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", refused(rel, "outside the repository")
	}
	return clean, nil
}

// ReadManifest reads a manifest or configuration file of the project
// (package.json, go.mod, .depgraph.yml, ...), at most Manifest bytes.
func ReadManifest(root, rel string) ([]byte, error) {
	b, _, err := Read(root, rel, Manifest)
	return b, err
}

// ReadSource reads a source file of the project, at most Source bytes.
func ReadSource(root, rel string) ([]byte, error) {
	b, _, err := Read(root, rel, Source)
	return b, err
}

// ReadPath reads a file at a path a user gave depgraph (a flag, absolute or
// relative to the working directory) under the same checks as Read, without
// the restriction to the repository.
func ReadPath(p string, max int64) ([]byte, error) {
	if p == "" {
		return nil, fmt.Errorf("empty path: %w", ErrRefused)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return nil, refused(p, "cannot be resolved")
	}
	b, _, err := readResolved(filepath.Dir(abs), filepath.Base(abs), p, max)
	return b, err
}
