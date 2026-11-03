//go:build windows

package container

import (
	"os"
	"syscall"
)

// detachedProcAttr starts the watchdog in a new process group, so ctrl-c /
// ctrl-break sent to depgraph's console group does not reach it.
func detachedProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// ignoredSignals are the signals the watchdog must outlive.
func ignoredSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}
