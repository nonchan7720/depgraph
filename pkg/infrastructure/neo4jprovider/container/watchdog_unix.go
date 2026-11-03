//go:build !windows

package container

import (
	"os/signal"
	"syscall"
)

// detachedProcAttr puts the watchdog in its own session: it is not in the
// terminal's process group (ctrl-c, kill of the group) and has no
// controlling terminal (no SIGHUP when the terminal closes).
func detachedProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}

// IgnoreSignals makes the watchdog outlive the signals meant for depgraph:
// it must act only once its parent is gone.
func IgnoreSignals() {
	signal.Ignore(syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
}
