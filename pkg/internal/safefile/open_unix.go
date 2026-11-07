//go:build unix

package safefile

import (
	"io/fs"
	"os"
	"syscall"
)

// openNoBlock opens the resolved path read-only without following a final
// symlink (it was resolved already: a swap in between fails) and without
// blocking on a FIFO.
func openNoBlock(p string) (*os.File, error) {
	return os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}

type fileID struct{ dev, ino uint64 }

func idOf(st fs.FileInfo) fileID {
	if s, ok := st.Sys().(*syscall.Stat_t); ok {
		return fileID{dev: uint64(s.Dev), ino: uint64(s.Ino)} //nolint:unconvert // the field types differ by platform
	}
	return fileID{}
}
