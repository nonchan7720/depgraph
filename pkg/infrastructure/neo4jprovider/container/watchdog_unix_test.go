//go:build !windows

package container

import (
	"bufio"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The watchdog survives the signals a terminal sends to the whole process
// group (ctrl-c, closing the terminal, kill of the group) and acts only when
// its parent is gone, i.e. when its stdin pipe reaches EOF.
func TestWatchdogIgnoresSignalsAndActsOnEOF(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), "DEPGRAPH_TEST_FAKE_WATCHDOG=1")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	lines := make(chan string, 4)
	go func() {
		s := bufio.NewScanner(stdout)
		for s.Scan() {
			lines <- s.Text()
		}
		if err := s.Err(); err != nil {
			lines <- "read error: " + err.Error()
		}
		close(lines)
	}()
	if l := <-lines; l != "ARMED" {
		t.Fatalf("first line %q", l)
	}
	for _, sig := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		if err := cmd.Process.Signal(sig); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(300 * time.Millisecond)
	if err := cmd.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("watchdog died from a signal: %v", err)
	}
	select {
	case l := <-lines:
		t.Fatalf("watchdog acted before EOF: %q", l)
	default:
	}
	_ = stdin.Close() // the parent is gone
	var got []string
	for l := range lines {
		got = append(got, l)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("watchdog exit: %v", err)
	}
	if strings.Join(got, "|") != "rm -f c9" {
		t.Errorf("watchdog ran %q, want once `rm -f c9`", got)
	}
}
