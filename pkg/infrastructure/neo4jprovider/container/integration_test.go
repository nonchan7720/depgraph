package container

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
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
		_, _, _ = p.Remove(ctx, Selection{Root: root, Projects: []string{key}})
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
	if err := c.Release(ctx); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := p.Discover(ctx, root); !ok {
		t.Fatal("a persistent store must keep running after Release")
	}
	if _, err := p.Down(ctx, root); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := p.Discover(ctx, root); ok {
		t.Fatal("store down must stop it")
	}
	began := time.Now()
	c2, err := p.Provide(ctx, options(root, key, false, neo4jprovider.StartIfPersistent))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s: persistent restart %.1fs", p.rt.Flavor, time.Since(began).Seconds())
	if recs := cypher(t, c2, "MATCH (n:ZzTest) RETURN count(n) AS n"); recs[0].Values[0].(int64) != 1 {
		t.Errorf("persisted nodes = %v, want 1", recs[0].Values[0])
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

// TestIntegrationCrashedOwnerIsSwept starts a store in a child process,
// kills it with SIGKILL (no cleanup runs) and checks the next start removes
// the orphan.
func TestIntegrationCrashedOwnerIsSwept(t *testing.T) {
	p := integrationProvider(t)
	if os.Getenv("DEPGRAPH_TEST_HELPER") != "" {
		t.Skip("helper")
	}
	root, key := t.TempDir(), zzKey(t)
	cleanupKey(t, p, root, key)

	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperHoldStore$", "-test.v")
	cmd.Env = append(os.Environ(), "DEPGRAPH_TEST_HELPER="+root, "DEPGRAPH_TEST_HELPER_KEY="+key, "DEPGRAPH_CONTAINER_CLI="+p.rt.CLI)
	cmd.Stderr = os.Stderr
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
	if _, ok, _ := p.Discover(context.Background(), root); !ok {
		t.Fatal("helper's store not running")
	}
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

// TestHelperHoldStore is the child process of TestIntegrationCrashedOwnerIsSwept.
func TestHelperHoldStore(t *testing.T) {
	root, key := os.Getenv("DEPGRAPH_TEST_HELPER"), os.Getenv("DEPGRAPH_TEST_HELPER_KEY")
	if root == "" {
		t.Skip("only run as a helper process")
	}
	p := New(ProviderConfig{CLI: os.Getenv("DEPGRAPH_CONTAINER_CLI")})
	if _, err := p.Provide(context.Background(), options(root, key, false, neo4jprovider.Start)); err != nil {
		t.Fatal(err)
	}
	_, _ = os.Stdout.WriteString("HELPER-READY\n")
	time.Sleep(10 * time.Minute) // killed by the parent
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
