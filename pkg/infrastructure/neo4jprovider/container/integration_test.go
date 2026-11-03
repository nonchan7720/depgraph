package container

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"

	"github.com/nonchan7720/depgraph/pkg/infrastructure/neo4jprovider"
)

// Integration tests run Neo4j with the container CLI of this machine
// ($DEPGRAPH_CONTAINER_CLI, else docker / podman / nerdctl) and skip when none
// answers. They only create and remove objects of project keys zz_test_*.

func integrationProvider(t *testing.T) *Provider {
	t.Helper()
	if testing.Short() {
		t.Skip("-short: no containers")
	}
	p := New(ProviderConfig{CLI: os.Getenv("DEPGRAPH_CONTAINER_CLI")})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.Available(ctx); err != nil {
		t.Skipf("no container runtime: %v", err)
	}
	return p
}

func zzKey(t *testing.T) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "zz_test_" + strings.ToLower(t.Name()[4:]) + "_" + hex.EncodeToString(b)
}

// cleanupKey removes everything of key, whatever the test did.
func cleanupKey(t *testing.T, p *Provider, root, key string) {
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		var err error
		for range 5 {
			if _, _, err = p.Remove(ctx, Selection{Root: root, Projects: []string{key}}); err == nil {
				break
			}
			time.Sleep(time.Second)
		}
		if err != nil {
			t.Errorf("cleanup of %s: %v", key, err)
		}
		insts, vols, _ := p.List(ctx)
		for _, i := range insts {
			if i.Project == key {
				t.Errorf("cleanup left container %s (%s)", i.Name, i.State)
			}
		}
		for _, v := range vols {
			if v.Project == key {
				t.Errorf("cleanup left volume %s", v.Name)
			}
		}
	})
}

func boltReady(ctx context.Context, c neo4jprovider.Conn) error {
	d, err := neo4j.NewDriver(c.URI, neo4j.BasicAuth(c.User, c.Password, ""))
	if err != nil {
		return err
	}
	defer func() { _ = d.Close(context.WithoutCancel(ctx)) }()
	return d.VerifyConnectivity(ctx)
}

func cypher(t *testing.T, c neo4jprovider.Conn, q string) []*neo4j.Record {
	t.Helper()
	ctx := context.Background()
	d, err := neo4j.NewDriver(c.URI, neo4j.BasicAuth(c.User, c.Password, ""))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = d.Close(ctx) }()
	res, err := neo4j.ExecuteQuery(ctx, d, q, nil, neo4j.EagerResultTransformer, neo4j.ExecuteQueryWithDatabase(c.Database))
	if err != nil {
		t.Fatal(err)
	}
	return res.Records
}

func options(root, key string, persist bool, policy neo4jprovider.Policy) neo4jprovider.StoreRequest {
	return neo4jprovider.StoreRequest{Root: root, Project: key, Persist: persist, Policy: policy, Ready: boltReady, Log: os.Stderr}
}

func waitGone(t *testing.T, p *Provider, root string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		insts, err := p.list(context.Background(), LabelRoot+"="+RootHash(root))
		if err == nil && len(insts) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("container of %s still there after %s: %+v (%v)", root, within, insts, err)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func TestIntegrationEphemeral(t *testing.T) {
	p := integrationProvider(t)
	ctx := context.Background()
	root, key := t.TempDir(), zzKey(t)
	cleanupKey(t, p, root, key)

	began := time.Now()
	c, err := p.Provide(ctx, options(root, key, false, neo4jprovider.Start))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s: ephemeral start %.1fs", p.rt.Flavor, time.Since(began).Seconds())
	if !c.Started || c.Persistent || !strings.HasPrefix(c.URI, "bolt://127.0.0.1:") {
		t.Errorf("conn = %+v", c)
	}
	cypher(t, c, "CREATE (:ZzTest {v: 1})")
	recordFixtures(t, p, root, c.Password)

	// another process finds it by labels, read-only
	other := New(ProviderConfig{CLI: p.rt.CLI})
	d, ok, err := other.Discover(ctx, root)
	if err != nil || !ok || d.URI != c.URI || d.Password != c.Password {
		t.Fatalf("Discover = %+v %v %v", d, ok, err)
	}
	if recs := cypher(t, d, "MATCH (n:ZzTest) RETURN count(n) AS n"); recs[0].Values[0].(int64) != 1 {
		t.Errorf("discovered store has %v nodes", recs[0].Values[0])
	}
	if err := c.Release(ctx); err != nil {
		t.Fatal(err)
	}
	waitGone(t, p, root, 30*time.Second)
	if _, ok, _ := other.Discover(ctx, root); ok {
		t.Error("released store still discovered")
	}
}

func TestIntegrationPersistent(t *testing.T) {
	p := integrationProvider(t)
	ctx := context.Background()
	root, key := t.TempDir(), zzKey(t)
	cleanupKey(t, p, root, key)

	c, err := p.Provide(ctx, options(root, key, true, neo4jprovider.Start))
	if err != nil {
		t.Fatal(err)
	}
	cypher(t, c, "CREATE (:ZzTest {v: 2})")
	// persist the data, not the process: the starter's Release stops it
	if err := c.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := p.Discover(ctx, root); ok {
		t.Fatal("a persistent store must be stopped by the Release of the process that started it")
	}
	insts, err := p.list(ctx, LabelRoot+"="+RootHash(root))
	if err != nil || len(insts) != 1 || insts[0].Running() {
		t.Fatalf("want the stopped container kept, got %+v %v", insts, err)
	}
	began := time.Now()
	c2, err := p.Provide(ctx, options(root, key, false, neo4jprovider.StartIfPersistent))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s: persistent restart %.1fs", p.rt.Flavor, time.Since(began).Seconds())
	if c2.Password != c.Password {
		t.Error("the restarted container must keep its password")
	}
	if recs := cypher(t, c2, "MATCH (n:ZzTest) RETURN count(n) AS n"); recs[0].Values[0].(int64) != 1 {
		t.Errorf("persisted nodes = %v, want 1", recs[0].Values[0])
	}
	if _, err := p.Down(ctx, root); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := p.Discover(ctx, root); ok {
		t.Fatal("store down must stop it")
	}
	cs, vs, err := p.Remove(ctx, Selection{Root: root, Projects: []string{key}})
	if err != nil || len(cs) != 1 || len(vs) != 1 {
		t.Fatalf("Remove = %v %v %v", cs, vs, err)
	}
	_, vols, err := p.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vols {
		if v.Project == key {
			t.Errorf("volume %s left behind", v.Name)
		}
	}
}

// helperEnd is how a helper process holding a store ends.
type helperEnd string

const (
	endPanicGoroutine helperEnd = "panic-goroutine" // kills the process without running main's defers
	endPanicMain      helperEnd = "panic-main"
	endExit           helperEnd = "exit" // os.Exit(1): no defers
	endSIGKILL        helperEnd = "sigkill"
)

// startHelper runs TestHelperHoldStore in a child process: it starts a store
// for root and then ends as told. It returns once the store is ready.
func startHelper(t *testing.T, p *Provider, root, key string, persist bool, end helperEnd, watchdog bool) (*exec.Cmd, io.WriteCloser) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperHoldStore$")
	env := append(os.Environ(),
		"DEPGRAPH_TEST_HELPER="+root, "DEPGRAPH_TEST_HELPER_KEY="+key,
		"DEPGRAPH_TEST_HELPER_END="+string(end), "DEPGRAPH_CONTAINER_CLI="+p.rt.CLI)
	if persist {
		env = append(env, "DEPGRAPH_TEST_HELPER_PERSIST=1")
	}
	if !watchdog {
		env = append(env, "DEPGRAPH_TEST_NO_WATCHDOG=1")
	}
	cmd.Env = env
	goOn, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	ready := make(chan bool, 1)
	go func() {
		s := bufio.NewScanner(out)
		for s.Scan() {
			if strings.Contains(s.Text(), "HELPER-READY") {
				ready <- true
				_, _ = io.Copy(io.Discard, out)
				return
			}
		}
		ready <- false
	}()
	select {
	case ok := <-ready:
		if !ok {
			_ = cmd.Wait()
			t.Fatal("helper did not start a store")
		}
	case <-time.After(3 * time.Minute):
		_ = cmd.Process.Kill()
		t.Fatal("helper timed out")
	}
	return cmd, goOn
}

// TestIntegrationWatchdog: however depgraph dies, its watchdog stops the
// container it started: an ephemeral one is removed, a persistent one is
// stopped with its volume kept, within 15 s and without the next start.
func TestIntegrationWatchdog(t *testing.T) {
	p := integrationProvider(t)
	if os.Getenv("DEPGRAPH_TEST_HELPER") != "" {
		t.Skip("helper")
	}
	for _, persist := range []bool{false, true} {
		for _, end := range []helperEnd{endPanicGoroutine, endPanicMain, endSIGKILL, endExit} {
			name := string(end) + "/ephemeral"
			if persist {
				name = string(end) + "/persistent"
			}
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				root, key := t.TempDir(), zzKey(t)
				cleanupKey(t, p, root, key)
				cmd, goOn := startHelper(t, p, root, key, persist, end, true)
				if _, ok, _ := p.Discover(context.Background(), root); !ok {
					t.Fatal("helper's store not running")
				}
				if end == endSIGKILL {
					_ = cmd.Process.Kill()
				} else {
					_, _ = io.WriteString(goOn, "go\n") // now end as told
				}
				_ = cmd.Wait()
				gone := time.Now()
				deadline := gone.Add(15 * time.Second)
				for {
					insts, err := p.list(context.Background(), LabelRoot+"="+RootHash(root))
					if err != nil {
						t.Fatal(err)
					}
					done := len(insts) == 0
					if persist {
						done = len(insts) == 1 && !insts[0].Running()
					}
					if done {
						t.Logf("cleaned up %.1fs after the helper died", time.Since(gone).Seconds())
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("15 s after the helper died: %+v", insts)
					}
					time.Sleep(250 * time.Millisecond)
				}
				if persist {
					_, vols, err := p.List(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					kept := false
					for _, v := range vols {
						kept = kept || v.Project == key
					}
					if !kept {
						t.Error("the persistent volume must be kept")
					}
				}
			})
		}
	}
}

// TestIntegrationCrashedOwnerIsSwept: without a watchdog (it could not be
// started), the next start removes the orphan of a SIGKILLed owner.
func TestIntegrationCrashedOwnerIsSwept(t *testing.T) {
	p := integrationProvider(t)
	if os.Getenv("DEPGRAPH_TEST_HELPER") != "" {
		t.Skip("helper")
	}
	root, key := t.TempDir(), zzKey(t)
	cleanupKey(t, p, root, key)
	cmd, _ := startHelper(t, p, root, key, false, endSIGKILL, false)
	_ = cmd.Process.Kill() // SIGKILL: no deferred cleanup
	_ = cmd.Wait()
	if _, ok, _ := p.Discover(context.Background(), root); ok {
		t.Error("an orphaned store must not be discovered")
	}
	removed := p.Sweep(context.Background(), neo4jprovider.StoreRequest{Log: os.Stderr})
	found := false
	for _, n := range removed {
		if n == ContainerName(key, root) {
			found = true
		}
	}
	if !found {
		t.Errorf("Sweep removed %v, want %s", removed, ContainerName(key, root))
	}
	waitGone(t, p, root, 30*time.Second)
}

// TestHelperHoldStore is the child process of the tests above.
func TestHelperHoldStore(t *testing.T) {
	root, key := os.Getenv("DEPGRAPH_TEST_HELPER"), os.Getenv("DEPGRAPH_TEST_HELPER_KEY")
	if root == "" {
		t.Skip("only run as a helper process")
	}
	cfg := ProviderConfig{CLI: os.Getenv("DEPGRAPH_CONTAINER_CLI")}
	if os.Getenv("DEPGRAPH_TEST_NO_WATCHDOG") != "" {
		cfg.Watchdog = func(string, string, WatchMode) (func(context.Context), error) {
			return nil, errors.New("disabled by the test")
		}
	}
	p := New(cfg)
	c, err := p.Provide(context.Background(), options(root, key, os.Getenv("DEPGRAPH_TEST_HELPER_PERSIST") != "", neo4jprovider.Start))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Release(context.Background()) }() // must not be what cleans up
	_, _ = os.Stdout.WriteString("HELPER-READY\n")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n') // the parent checked the store is up
	switch helperEnd(os.Getenv("DEPGRAPH_TEST_HELPER_END")) {
	case endPanicGoroutine:
		go func() { panic("depgraph test: panic in a goroutine") }()
		time.Sleep(time.Minute)
	case endPanicMain:
		panic("depgraph test: panic in main")
	case endExit:
		os.Exit(1)
	default:
		time.Sleep(10 * time.Minute) // SIGKILLed by the parent
	}
}

// recordFixtures writes the CLI's inspect / port output of the running store
// to $DEPGRAPH_RECORD_FIXTURES/<flavor>_{inspect.json,port.txt}, with the
// password, root and host-specific values replaced, for the parser tests.
func recordFixtures(t *testing.T, p *Provider, root, password string) {
	dir := os.Getenv("DEPGRAPH_RECORD_FIXTURES")
	if dir == "" {
		return
	}
	ctx := context.Background()
	insts, err := p.list(ctx, LabelRoot+"="+RootHash(root))
	if err != nil || len(insts) != 1 {
		t.Fatalf("record: %v %v", insts, err)
	}
	inspect, err := p.run(ctx, nil, "inspect", insts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	port, err := p.run(ctx, nil, "port", insts[0].ID, boltPort)
	if err != nil {
		t.Fatal(err)
	}
	host, _ := os.Hostname()
	r := strings.NewReplacer(password, "fixturepassword", root, "/work/app", RootHash(root), RootHash("/work/app"), HostID(), "hostid000000", host, "fixture-host")
	for name, b := range map[string][]byte{"_inspect.json": inspect, "_port.txt": port} {
		if err := os.WriteFile(filepath.Join(dir, string(p.rt.Flavor)+name), []byte(r.Replace(string(b))), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
