package container

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/infrastructure/neo4jprovider"
)

// recordingRunner records argv and answers from a script of errors.
type recordingRunner struct {
	mu    sync.Mutex
	calls [][]string
	errs  []error // returned in order; nil when exhausted
}

func (r *recordingRunner) Run(_ context.Context, _ []string, _ string, args ...string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, args)
	if len(r.errs) == 0 {
		return nil, nil
	}
	err := r.errs[0]
	r.errs = r.errs[1:]
	return nil, err
}

func (r *recordingRunner) got() [][]string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]string(nil), r.calls...)
}

func TestWatchdogCleansUpOnceWhenThePipeCloses(t *testing.T) {
	for mode, want := range map[WatchMode][]string{
		WatchRemove: {"rm", "-f", "c1"},
		WatchStop:   {"stop", "-t", "10", "c1"},
	} {
		pr, pw := io.Pipe()
		run := &recordingRunner{}
		done := make(chan error, 1)
		go func() { done <- watchContainer(pr, run, "docker", "c1", mode, time.Millisecond) }()
		time.Sleep(50 * time.Millisecond)
		if len(run.got()) != 0 {
			t.Fatalf("%s: ran %q before the parent went away", mode, run.got())
		}
		_ = pw.Close() // the parent exited, for whatever reason
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s: %v", mode, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: watchdog did not act on EOF", mode)
		}
		if got := run.got(); !reflect.DeepEqual(got, [][]string{want}) {
			t.Errorf("%s: ran %q, want once %q", mode, got, want)
		}
	}
}

func TestWatchdogNoSuchContainerIsSuccessAndErrorsAreRetried(t *testing.T) {
	gone := &recordingRunner{errs: []error{errors.New("Error response from daemon: No such container: c1")}}
	if err := watchContainer(strings.NewReader(""), gone, "docker", "c1", WatchRemove, time.Millisecond); err != nil || len(gone.got()) != 1 {
		t.Errorf("already gone: err=%v calls=%q", err, gone.got())
	}
	flaky := &recordingRunner{errs: []error{errors.New("daemon busy"), errors.New("daemon busy")}}
	if err := watchContainer(strings.NewReader(""), flaky, "docker", "c1", WatchRemove, time.Millisecond); err != nil || len(flaky.got()) != 3 {
		t.Errorf("retries: err=%v calls=%q", err, flaky.got())
	}
	down := &recordingRunner{errs: []error{errors.New("x"), errors.New("x"), errors.New("x"), errors.New("x"), errors.New("x"), errors.New("x")}}
	if err := watchContainer(strings.NewReader(""), down, "docker", "c1", WatchRemove, time.Millisecond); err == nil || len(down.got()) != watchdogAttempts {
		t.Errorf("gives up after %d attempts: err=%v calls=%d", watchdogAttempts, err, len(down.got()))
	}
}

func TestWatchdogArgs(t *testing.T) {
	want := []string{"__container-watchdog", "--cli", "podman", "--id", "abc", "--mode", "stop"}
	if got := WatchdogArgs("podman", "abc", WatchStop); !reflect.DeepEqual(got, want) {
		t.Errorf("= %q", got)
	}
	if err := WatchdogMain([]string{"--cli", "docker", "--id", "x", "--mode", "kill"}, strings.NewReader(""), &recordingRunner{}); err == nil {
		t.Error("unknown mode must be an error")
	}
	if err := WatchdogMain([]string{"--cli", "docker", "--mode", "rm"}, strings.NewReader(""), &recordingRunner{}); err == nil {
		t.Error("missing id must be an error")
	}
}

// fakeSpawner records watchdogs and when they were disarmed.
type fakeSpawner struct {
	mu      sync.Mutex
	spawned []string // "mode id"
	f       *fakeCLI
	// disarmedAfter records the fake CLI's call count at disarm time.
	disarmedAfter []int
}

func (s *fakeSpawner) spawn(_ string, id string, mode WatchMode) (func(context.Context), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.spawned = append(s.spawned, string(mode)+" "+id)
	return func(context.Context) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.disarmedAfter = append(s.disarmedAfter, len(s.f.commands()))
	}, nil
}

func TestProviderArmsAWatchdogForWhatItStarts(t *testing.T) {
	f := newFakeCLI()
	s := &fakeSpawner{f: f}
	p := newTestProvider(f)
	p.cfg.Watchdog = s.spawn

	c, err := p.Provide(context.Background(), opts(neo4jprovider.Start, false, nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.spawned) != 1 || !strings.HasPrefix(s.spawned[0], "rm c") {
		t.Fatalf("ephemeral start: watchdogs %q, want one in rm mode", s.spawned)
	}
	if err := c.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(s.disarmedAfter) != 1 || !f.ran("rm -f") {
		t.Errorf("Release must clean up explicitly and then disarm: %v %q", s.disarmedAfter, f.commands())
	}

	// persistent: stop mode, for the first start and for a restart
	c, err = p.Provide(context.Background(), opts(neo4jprovider.Start, true, nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Provide(context.Background(), opts(neo4jprovider.Start, true, nil)); err != nil {
		t.Fatal(err)
	}
	if len(s.spawned) != 3 || !strings.HasPrefix(s.spawned[1], "stop ") || !strings.HasPrefix(s.spawned[2], "stop ") {
		t.Errorf("persistent watchdogs = %q", s.spawned)
	}
	// a store this process only reuses gets none
	other := newTestProvider(f)
	other.cfg.Watchdog = s.spawn
	if _, err := other.Provide(context.Background(), opts(neo4jprovider.Start, true, nil)); err != nil {
		t.Fatal(err)
	}
	if len(s.spawned) != 3 {
		t.Errorf("reuse must not arm a watchdog: %q", s.spawned)
	}
}

func TestProviderStartsWithoutWatchdogWhenSpawnFails(t *testing.T) {
	f := newFakeCLI()
	p := newTestProvider(f)
	p.cfg.Watchdog = func(string, string, WatchMode) (func(context.Context), error) {
		return nil, errors.New("fork failed")
	}
	var log strings.Builder
	o := opts(neo4jprovider.Start, false, nil)
	o.Log = &log
	c, err := p.Provide(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "fork failed") {
		t.Errorf("a missing watchdog must be reported: %q", log.String())
	}
	if err := c.Release(context.Background()); err != nil || len(f.containers) != 0 {
		t.Errorf("Release still cleans up: %v, %d left", err, len(f.containers))
	}
}
