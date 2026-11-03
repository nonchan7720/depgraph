package container

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"time"
)

// The watchdog guarantees that a container depgraph started is stopped
// whatever happens to depgraph: a panic in any goroutine (which ends the
// process without running deferred functions), os.Exit, SIGKILL or a closed
// terminal. Right after `run` / `start`, depgraph starts itself as a child,
//
//	depgraph __container-watchdog --cli <cli> --id <container id> --mode rm|stop
//
// with the read end of a pipe as its stdin and keeps the write end. The child
// ignores SIGINT / SIGTERM / SIGHUP, runs in its own session (process group
// on Windows), and blocks reading the pipe. The pipe reaches EOF only when
// the parent is gone (the kernel closes its descriptors however it ended) or
// releases the store; the child then removes (ephemeral) or stops
// (persistent) exactly that container, and exits. No reaper container, no
// network port. The next-start sweep stays as a last resort (e.g. a machine
// that lost power).

// WatchdogCommand is the hidden subcommand that runs the watchdog.
const WatchdogCommand = "__container-watchdog"

// WatchMode is what the watchdog does to its container.
type WatchMode string

const (
	WatchRemove WatchMode = "rm"   // ephemeral: remove it with its data
	WatchStop   WatchMode = "stop" // persistent: stop it, keep it and its volume
)

// watchdogAttempts bounds the cleanup retries (daemon busy, restarting).
const watchdogAttempts = 5

// Spawner starts a watchdog for a container and returns disarm, which ends
// it (closes the pipe and waits for it); the parent calls disarm after its
// own cleanup.
type Spawner func(cli, id string, mode WatchMode) (disarm func(context.Context), err error)

// WatchdogArgs is the argument list of the watchdog child (after the binary).
func WatchdogArgs(cli, id string, mode WatchMode) []string {
	return []string{WatchdogCommand, "--cli", cli, "--id", id, "--mode", string(mode)}
}

// SpawnWatchdog starts the watchdog as a child of this process
// (os.Executable(), i.e. depgraph itself).
func SpawnWatchdog(cli, id string, mode WatchMode) (func(context.Context), error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, WatchdogArgs(cli, id, mode)...)
	cmd.Stdin = r // only the read end goes to the child; w stays here (close-on-exec)
	cmd.SysProcAttr = detachedProcAttr()
	if err := cmd.Start(); err != nil {
		_ = r.Close()
		_ = w.Close()
		return nil, err
	}
	_ = r.Close()
	keepOpen(w)
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	return func(ctx context.Context) {
		forget(w)
		_ = w.Close() // EOF: the watchdog cleans up (nothing left after our own cleanup) and exits
		t := time.NewTimer(40 * time.Second)
		defer t.Stop()
		select {
		case <-exited:
		case <-t.C:
		case <-ctx.Done():
		}
	}, nil
}

// armed holds the write ends of live watchdog pipes. An unreachable *os.File
// is closed by its finalizer, which would fire the watchdog while depgraph
// still runs; the registry keeps each one reachable until it is disarmed.
var (
	armedMu sync.Mutex
	armed   = map[*os.File]bool{}
)

func keepOpen(w *os.File) {
	armedMu.Lock()
	armed[w] = true
	armedMu.Unlock()
}

func forget(w *os.File) {
	armedMu.Lock()
	delete(armed, w)
	armedMu.Unlock()
}

// IgnoreSignals makes the watchdog outlive the signals meant for depgraph
// (ctrl-c, SIGTERM, SIGHUP on Unix): it must act only once its parent is gone.
func IgnoreSignals() {
	signal.Ignore(ignoredSignals()...)
}

// WatchdogMain is the watchdog process: args are the flags after
// WatchdogCommand, stdin the pipe from the parent.
func WatchdogMain(args []string, stdin io.Reader, run Runner) error {
	fs := flag.NewFlagSet(WatchdogCommand, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	cli := fs.String("cli", "", "container CLI")
	id := fs.String("id", "", "container id")
	mode := fs.String("mode", "", "rm or stop")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cli == "" || *id == "" {
		return errors.New("watchdog: --cli and --id are required")
	}
	m := WatchMode(*mode)
	if m != WatchRemove && m != WatchStop {
		return fmt.Errorf("watchdog: unknown --mode %q (rm or stop)", *mode)
	}
	IgnoreSignals()
	return watchContainer(stdin, run, *cli, *id, m, time.Second)
}

// watchContainer blocks until stdin reaches EOF, then removes or stops the
// container. "No such container" counts as done (the parent cleaned up).
func watchContainer(stdin io.Reader, run Runner, cli, id string, mode WatchMode, retryDelay time.Duration) error {
	_, _ = io.Copy(io.Discard, stdin) // the parent never writes: this returns when it is gone
	args := []string{"rm", "-f", id}
	if mode == WatchStop {
		args = []string{"stop", "-t", "10", id}
	}
	var err error
	for attempt := range watchdogAttempts {
		if attempt > 0 {
			time.Sleep(retryDelay)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		_, err = run.Run(ctx, nil, cli, args...)
		cancel()
		if err == nil || isNoSuch(err) {
			return nil
		}
	}
	return fmt.Errorf("watchdog: %w", err)
}
