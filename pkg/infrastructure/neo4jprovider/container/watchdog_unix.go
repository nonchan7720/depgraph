//go:build !windows

package container

import (
	"os"
	"syscall"
)

// detachedProcAttr puts the watchdog in its own session: it is not in the
// terminal's process group (ctrl-c, kill of the group) and has no
// controlling terminal (no SIGHUP when the terminal closes).
func detachedProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// ignoredSignals are the signals the watchdog must outlive.
func ignoredSignals() []os.Signal {
	return []os.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP}
}
