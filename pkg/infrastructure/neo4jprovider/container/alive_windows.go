//go:build windows

package container

import "os"

// processAlive reports whether pid is a live process on this machine
// (os.FindProcess opens the process on Windows and fails when it is gone).
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	return true
}
