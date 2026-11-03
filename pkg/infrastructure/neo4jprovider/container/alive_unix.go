//go:build !windows

package container

import (
	"errors"
	"syscall"
)

// processAlive reports whether pid is a live process on this machine. A
// process of another user (EPERM) counts as alive: when in doubt, keep.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
