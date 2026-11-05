package cmd

import (
	"fmt"
	"os"
	"testing"
)

// TestMain lets the test binary stand in for depgraph when the gate starts
// its background sync child (`<test binary> __gate-sync ...`, from
// os.Executable()).
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == gateSyncCommand {
		c := newRootCommand(&options{})
		c.SetArgs(os.Args[1:])
		if err := c.Execute(); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
