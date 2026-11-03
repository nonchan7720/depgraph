//go:build windows

package container

import (
	"os"
	"os/signal"
	"syscall"
)

// detachedProcAttr starts the watchdog in a new process group, so ctrl-c /
// ctrl-break sent to depgraph's console group does not reach it.
func detachedProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// IgnoreSignals makes the watchdog outlive the signals meant for depgraph:
// it must act only once its parent is gone.
func IgnoreSignals() {
	signal.Ignore(os.Interrupt, syscall.SIGTERM)
}
