//go:build !unix

package safefile

import (
	"io/fs"
	"os"
)

// openNoBlock opens the resolved path read-only (no FIFOs to block on).
func openNoBlock(p string) (*os.File, error) { return os.Open(p) }

type fileID struct{}

func idOf(fs.FileInfo) fileID { return fileID{} }
