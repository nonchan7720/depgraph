package container

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/infrastructure/neo4jprovider"
)

const (
	testRoot = "/work/app"
	testKey  = "app"
	myPID    = 4242
	myHost   = "host-a"
)

func newTestProvider(f *fakeCLI, alive ...int) *Provider {
	live := map[int]bool{myPID: true}
	for _, p := range alive {
		live[p] = true
	}
	return New(ProviderConfig{
		Runner: f, LookPath: f.lookPath,
		Alive: func(pid int) bool { return live[pid] },
		PID:   myPID, Host: myHost, Image: "neo4j:test",
		Poll: time.Millisecond, StartTimeout: time.Second,
		// never a real watchdog against fake container ids
		Watchdog: func(string, string, WatchMode) (func(context.Context), error) {
			return func(context.Context) {}, nil
		},
	})
}

func labelsFor(root, project string, persist bool, owner int, host string) map[string]string {
	p := "0"
	if persist {
		p = "1"
	}
	return map[string]string{
		LabelManaged: "1", LabelRoot: RootHash(root), LabelRootPath: root, LabelProject: project,
		LabelPersist: p, LabelOwnerPID: strconv.Itoa(owner), LabelHost: host,
	}
}

func opts(policy neo4jprovider.Policy, persist bool, log *bytes.Buffer) neo4jprovider.StoreRequest {
	o := neo4jprovider.StoreRequest{
		Root: testRoot, Project: testKey, Persist: persist, Policy: policy,
		Ready: func(context.Context, neo4jprovider.Conn) error { return nil },
	}
	if log != nil {
		o.Log = log
	}
	return o
}

func TestProvideStartsEphemeralAndReleaseRemovesIt(t *testing.T) {
	f := newFakeCLI()
	p := newTestProvider(f)
	var log bytes.Buffer
	c, err := p.Provide(context.Background(), opts(neo4jprovider.Start, false, &log))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Started || c.Persistent || c.User != "neo4j" || c.Database != "neo4j" || !strings.HasPrefix(c.URI, "bolt://127.0.0.1:") || len(c.Password) < 16 {
		t.Errorf("conn = %+v", c)
	}
	if !f.ran("pull neo4j:test") {
		t.Error("a missing image must be pulled")
	}
	if !strings.Contains(log.String(), "this takes 10-20 s") || !strings.Contains(log.String(), "ephemeral") {
		t.Errorf("startup must be announced: %q", log.String())
	}
	// the run command: fixed argv, loopback port, tmpfs data, no password
	var run []string
	for i, args := range f.calls {
		if args[0] == "run" {
			run = args
			if !reflect.DeepEqual(f.envs[i], []string{"NEO4J_AUTH=neo4j/" + c.Password}) {
				t.Errorf("password must reach the CLI through its environment, got %q", f.envs[i])
			}
		}
	}
	want := RunArgs("neo4j:test", ContainerName(testKey, testRoot), []string{
		LabelManaged + "=1", LabelRoot + "=" + RootHash(testRoot), LabelRootPath + "=" + testRoot,
		LabelProject + "=" + testKey, LabelPersist + "=0", LabelOwnerPID + "=4242", LabelHost + "=" + myHost,
	}, "")
	if !reflect.DeepEqual(run, want) {
		t.Errorf("run args\n got %q\nwant %q", run, want)
	}
	joined := strings.Join(run, " ")
	for _, s := range []string{"--rm", "-p 127.0.0.1::7687", "--tmpfs /data", "--tmpfs /logs", "-e NEO4J_AUTH "} {
		if !strings.Contains(joined, s) {
			t.Errorf("run lacks %q: %s", s, joined)
		}
	}
	for _, args := range f.calls {
		if strings.Contains(strings.Join(args, " "), c.Password) {
			t.Errorf("password on a command line: %q", args)
		}
	}
	if len(f.containers) != 1 {
		t.Fatalf("containers = %d", len(f.containers))
	}
	if err := c.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.containers) != 0 {
		t.Errorf("Release must remove the ephemeral container, left %d", len(f.containers))
	}
}

func TestProvideReusesRunningStoreAndReleaseKeepsIt(t *testing.T) {
	f := newFakeCLI()
	f.add(ContainerName(testKey, testRoot), "running", 41000, labelsFor(testRoot, testKey, false, 777, myHost), "secretpw")
	p := newTestProvider(f, 777)
	for _, policy := range []neo4jprovider.Policy{neo4jprovider.Existing, neo4jprovider.StartIfPersistent, neo4jprovider.Start} {
		c, err := p.Provide(context.Background(), opts(policy, false, nil))
		if err != nil {
			t.Fatal(err)
		}
		if c.Started || c.URI != "bolt://127.0.0.1:41000" || c.Password != "secretpw" {
			t.Errorf("policy %v: conn = %+v", policy, c)
		}
		if err := c.Release(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if f.ran("run") || f.ran("stop") || f.ran("rm") {
		t.Errorf("a store of another process must be used as it is: %q", f.commands())
	}
	if len(f.containers) != 1 {
		t.Error("the reused container must survive Release")
	}
}

func TestProvidePolicyWithoutRunningStore(t *testing.T) {
	for _, tt := range []struct {
		policy  neo4jprovider.Policy
		persist bool
		starts  bool
	}{
		{neo4jprovider.Existing, false, false},
		{neo4jprovider.Existing, true, false},
		{neo4jprovider.StartIfPersistent, false, false},
		{neo4jprovider.StartIfPersistent, true, true},
		{neo4jprovider.Start, false, true},
	} {
		f := newFakeCLI()
		p := newTestProvider(f)
		_, err := p.Provide(context.Background(), opts(tt.policy, tt.persist, nil))
		if tt.starts != f.ran("run") {
			t.Errorf("policy %v persist %v: started = %v", tt.policy, tt.persist, f.ran("run"))
		}
		if !tt.starts && !errors.Is(err, neo4jprovider.ErrNotRunning) {
			t.Errorf("policy %v persist %v: err = %v, want ErrNotRunning", tt.policy, tt.persist, err)
		}
		if tt.policy == neo4jprovider.Existing && len(f.commands()) > 0 {
			for _, c := range f.commands() {
				if !strings.HasPrefix(c, "version") && !strings.HasPrefix(c, "ps") && !strings.HasPrefix(c, "inspect") {
					t.Errorf("Existing must only read, ran %q", c)
				}
			}
		}
	}
}

func TestProvidePersistentKeepsContainerAndVolume(t *testing.T) {
	f := newFakeCLI()
	p := newTestProvider(f)
	var log bytes.Buffer
	c, err := p.Provide(context.Background(), opts(neo4jprovider.Start, true, &log))
	if err != nil {
		t.Fatal(err)
	}
	vol := VolumeName(testKey)
	if !c.Started || !c.Persistent {
		t.Errorf("conn = %+v", c)
	}
	if l, ok := f.volumes[vol]; !ok || l[LabelManaged] != "1" || l[LabelProject] != testKey {
		t.Errorf("volume %s = %v, %v", vol, l, ok)
	}
	var run string
	for _, cmd := range f.commands() {
		if strings.HasPrefix(cmd, "run") {
			run = cmd
		}
	}
	if strings.Contains(run, "--rm") || !strings.Contains(run, "-v "+vol+":/data") || strings.Contains(run, "--tmpfs /data") || !strings.Contains(run, LabelPersist+"=1") {
		t.Errorf("persistent run = %s", run)
	}
	if !strings.Contains(log.String(), vol) || !strings.Contains(log.String(), "depgraph store rm") {
		t.Errorf("persistent start must name the volume and how to remove it: %q", log.String())
	}
	// Persist the data, not the process: Release stops (never removes) the
	// container this process started.
	before := len(f.commands())
	if err := c.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	released := f.commands()[before:]
	if len(f.containers) != 1 {
		t.Fatal("Release must keep a persistent container (stopped)")
	}
	for _, ct := range f.containers {
		if ct.state != "exited" {
			t.Errorf("after Release: %s, want exited", ct.state)
		}
	}
	if len(released) != 1 || !strings.HasPrefix(released[0], "stop -t 10 ") {
		t.Errorf("Release ran %q, want only `stop -t 10 <id>`", released)
	}

	// the next start restarts the same container (same password, same volume)
	c2, err := p.Provide(context.Background(), opts(neo4jprovider.StartIfPersistent, false, nil))
	if err != nil {
		t.Fatal(err)
	}
	if !f.ran("start ") || c2.Password != c.Password || !c2.Persistent || !c2.Started {
		t.Errorf("restart: %+v (calls %q)", c2, f.commands())
	}
	// a store another process started is left running by this one's Release
	other := newTestProvider(f)
	c3, err := other.Provide(context.Background(), opts(neo4jprovider.Start, true, nil))
	if err != nil || c3.Started {
		t.Fatalf("reuse: %+v %v", c3, err)
	}
	if err := c3.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, ct := range f.containers {
		if ct.state != "running" {
			t.Errorf("Release of a reused store stopped it: %s", ct.state)
		}
	}
	// the restarting process stops it again on exit
	if err := c2.Release(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, ct := range f.containers {
		if ct.state != "exited" {
			t.Errorf("after the restarter's Release: %s", ct.state)
		}
	}

	// store down on a running one: stopped, kept
	if _, err := p.Provide(context.Background(), opts(neo4jprovider.Start, true, nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Down(context.Background(), testRoot); err != nil {
		t.Fatal(err)
	}
	for _, ct := range f.containers {
		if ct.state != "exited" {
			t.Errorf("after down: %s", ct.state)
		}
	}

	// store rm: container and volume gone
	cs, vs, err := p.Remove(context.Background(), Selection{Root: testRoot, Projects: []string{testKey}})
	if err != nil || len(cs) != 1 || len(vs) != 1 || len(f.containers) != 0 || len(f.volumes) != 0 {
		t.Errorf("Remove = %v %v %v; left %d containers %d volumes", cs, vs, err, len(f.containers), len(f.volumes))
	}
}

// docker starts removing a --rm container on stop; the rm that follows then
// reports "removal ... already in progress", which is a successful release.
func TestReleaseAcceptsRemovalInProgress(t *testing.T) {
	f := newFakeCLI()
	p := newTestProvider(f)
	c, err := p.Provide(context.Background(), opts(neo4jprovider.Start, false, nil))
	if err != nil {
		t.Fatal(err)
	}
	f.rmErr = errors.New("docker rm: Error response from daemon: removal of container 05df7d4d is already in progress")
	if err := c.Release(context.Background()); err != nil {
		t.Errorf("Release = %v", err)
	}
}

func TestPersistentVolumeWithoutContainerIsAnError(t *testing.T) {
	f := newFakeCLI()
	f.volumes[VolumeName(testKey)] = map[string]string{LabelManaged: "1", LabelProject: testKey}
	p := newTestProvider(f)
	_, err := p.Provide(context.Background(), opts(neo4jprovider.Start, true, nil))
	if err == nil || !strings.Contains(err.Error(), "depgraph store rm") {
		t.Errorf("err = %v", err)
	}
	if _, ok := f.volumes[VolumeName(testKey)]; !ok {
		t.Error("data must never be deleted implicitly")
	}
}

func TestSweepRemovesOnlyOrphansOfThisHost(t *testing.T) {
	f := newFakeCLI()
	dead, live := 1001, 1002
	orphan := f.add("depgraph-x-1", "running", 1, labelsFor("/r/x", "x", false, dead, myHost), "p")
	f.add("depgraph-y-1", "running", 2, labelsFor("/r/y", "y", false, live, myHost), "p")          // owner alive
	f.add("depgraph-z-1", "running", 3, labelsFor("/r/z", "z", true, dead, myHost), "p")           // persistent
	f.add("depgraph-w-1", "running", 4, labelsFor("/r/w", "w", false, dead, "other-host"), "p")    // another machine
	f.add("unrelated", "running", 5, map[string]string{LabelPersist: "0", LabelHost: myHost}, "p") // not ours
	p := newTestProvider(f, live)
	var log bytes.Buffer
	if _, err := p.Provide(context.Background(), opts(neo4jprovider.Start, false, &log)); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.containers[orphan.id]; ok {
		t.Error("orphan of this host must be removed")
	}
	for _, name := range []string{"depgraph-y-1", "depgraph-z-1", "depgraph-w-1", "unrelated"} {
		found := false
		for _, c := range f.containers {
			if c.name == name {
				found = true
			}
		}
		if !found {
			t.Errorf("%s must not be removed", name)
		}
	}
	if !strings.Contains(log.String(), "depgraph-x-1") {
		t.Errorf("sweep must say what it removed: %q", log.String())
	}
}

func TestDiscoverIsReadOnlyAndSkipsOrphans(t *testing.T) {
	f := newFakeCLI()
	p := newTestProvider(f)
	if _, ok, err := p.Discover(context.Background(), testRoot); ok || err != nil {
		t.Errorf("empty: ok=%v err=%v", ok, err)
	}
	f.add(ContainerName(testKey, testRoot), "running", 41000, labelsFor(testRoot, testKey, false, 999, myHost), "pw1234567890")
	if _, ok, _ := p.Discover(context.Background(), testRoot); ok {
		t.Error("an orphaned ephemeral store must not be discovered")
	}
	f2 := newFakeCLI()
	f2.add(ContainerName(testKey, testRoot), "running", 41001, labelsFor(testRoot, testKey, true, 999, myHost), "pw1234567890")
	f2.add("depgraph-other", "running", 41002, labelsFor("/elsewhere", "other", true, 999, myHost), "zz")
	p2 := newTestProvider(f2)
	c, ok, err := p2.Discover(context.Background(), testRoot)
	if err != nil || !ok || c.URI != "bolt://127.0.0.1:41001" || c.Password != "pw1234567890" {
		t.Errorf("Discover = %+v %v %v", c, ok, err)
	}
	for _, cmd := range append(f.commands(), f2.commands()...) {
		if !strings.HasPrefix(cmd, "version") && !strings.HasPrefix(cmd, "ps") && !strings.HasPrefix(cmd, "inspect") {
			t.Errorf("Discover ran %q", cmd)
		}
	}
}

func TestStartRaceWaitsForTheOtherProcess(t *testing.T) {
	f := newFakeCLI()
	f.images["neo4j:test"] = true
	f.runErr = errors.New(`docker run: Conflict. The container name "/x" is already in use by container "abc"`)
	// the other process's container appears while we wait
	f.add(ContainerName(testKey, testRoot), "created", 0, labelsFor(testRoot, testKey, false, 5555, myHost), "theirs")
	go func() {
		time.Sleep(20 * time.Millisecond)
		f.mu.Lock()
		for _, c := range f.containers {
			c.state, c.port = "running", 42000
		}
		f.mu.Unlock()
	}()
	p := newTestProvider(f, 5555)
	c, err := p.Provide(context.Background(), opts(neo4jprovider.Start, false, nil))
	if err != nil {
		t.Fatal(err)
	}
	if c.Started || c.Password != "theirs" || c.URI != "bolt://127.0.0.1:42000" {
		t.Errorf("conn = %+v", c)
	}
}

func TestStartFailureCleansUp(t *testing.T) {
	f := newFakeCLI()
	f.images["neo4j:test"] = true
	p := newTestProvider(f)
	o := opts(neo4jprovider.Start, false, nil)
	o.Ready = func(context.Context, neo4jprovider.Conn) error { return errors.New("connection refused") }
	_, err := p.Provide(context.Background(), o)
	if err == nil || !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("err = %v", err)
	}
	if len(f.containers) != 0 {
		t.Error("a container that never became ready must be removed")
	}
}

func TestDetect(t *testing.T) {
	on := func(names ...string) func(string) (string, error) {
		return func(n string) (string, error) {
			for _, x := range names {
				if x == n {
					return "/bin/" + n, nil
				}
			}
			return "", errors.New("not found")
		}
	}
	answering := func(ok ...string) Runner {
		return runnerFunc(func(cli string) error {
			for _, x := range ok {
				if x == cli {
					return nil
				}
			}
			return errors.New("cannot connect")
		})
	}
	ctx := context.Background()
	tests := []struct {
		name     string
		explicit string
		path     []string
		answer   []string
		want     Flavor
		errHas   string
	}{
		{"docker first", "", []string{"docker", "podman", "nerdctl"}, []string{"docker", "podman", "nerdctl"}, Docker, ""},
		{"podman when docker does not answer", "", []string{"docker", "podman"}, []string{"podman"}, Podman, ""},
		{"nerdctl last", "", []string{"nerdctl"}, []string{"nerdctl"}, Nerdctl, ""},
		{"explicit wins", "nerdctl", []string{"docker", "nerdctl"}, []string{"docker", "nerdctl"}, Nerdctl, ""},
		{"explicit path", "/opt/podman/bin/podman", []string{"/opt/podman/bin/podman"}, []string{"/opt/podman/bin/podman"}, Podman, ""},
		{"explicit not answering", "podman", []string{"podman"}, nil, "", "does not answer"},
		{"explicit missing", "podman", nil, nil, "", "not found"},
		{"ctr is unsupported", "ctr", []string{"ctr"}, []string{"ctr"}, "", "not supported"},
		{"none on PATH", "", nil, nil, "", "none of docker, podman, nerdctl"},
		{"present but down", "", []string{"docker"}, nil, "", "docker does not answer"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt, err := DetectRuntime(ctx, tt.explicit, on(tt.path...), answering(tt.answer...))
			if tt.errHas != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errHas) {
					t.Fatalf("err = %v, want %q", err, tt.errHas)
				}
				return
			}
			if err != nil || rt.Flavor != tt.want {
				t.Fatalf("Detect = %+v, %v; want %s", rt, err, tt.want)
			}
		})
	}
}

type runnerFunc func(cli string) error

func (r runnerFunc) Run(_ context.Context, _ []string, cli string, _ ...string) ([]byte, error) {
	return nil, r(cli)
}

func TestNames(t *testing.T) {
	if got := ContainerName("My App@feature/x", "/r"); !strings.HasPrefix(got, "depgraph-my-app-feature-x-") {
		t.Errorf("ContainerName = %q", got)
	}
	if VolumeName("a@b") == VolumeName("a-b") {
		t.Error("keys that slug alike must get different volumes")
	}
	if RootHash("/a") == RootHash("/b") || len(RootHash("/a")) != 16 {
		t.Error("RootHash")
	}
	if ContainerName("", "/r") == "" || !strings.HasPrefix(ContainerName("", "/r"), "depgraph-project-") {
		t.Errorf("empty key: %q", ContainerName("", "/r"))
	}
}
