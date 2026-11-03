package container

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestMain lets the test binary stand in for depgraph when it is re-executed:
//   - as the watchdog (`<test binary> __container-watchdog ...`), which the
//     real spawner starts from os.Executable();
//   - as a watchdog with a CLI that only prints argv
//     (DEPGRAPH_TEST_FAKE_WATCHDOG), for the signal test.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == WatchdogCommand {
		if err := WatchdogMain(os.Args[2:], os.Stdin, ExecRunner{}); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if os.Getenv("DEPGRAPH_TEST_FAKE_WATCHDOG") != "" {
		IgnoreSignals()
		_, _ = fmt.Println("ARMED")
		if err := WatchdogMain([]string{"--cli", "docker", "--id", "c9", "--mode", "rm"}, os.Stdin, printRunner{}); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// printRunner prints each command instead of running it.
type printRunner struct{}

func (printRunner) Run(_ context.Context, _ []string, _ string, args ...string) ([]byte, error) {
	_, _ = fmt.Println(strings.Join(args, " "))
	return nil, nil
}
