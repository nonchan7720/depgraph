package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/infrastructure/config"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/neo4jprovider"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/neo4jprovider/container"
)

// readOnlyCLI is a container CLI that answers and holds no containers; any
// command that would change something fails the test.
type readOnlyCLI struct {
	t     *testing.T
	mu    sync.Mutex
	calls []string
}

func (r *readOnlyCLI) Run(_ context.Context, _ []string, _ string, args ...string) ([]byte, error) {
	r.mu.Lock()
	r.calls = append(r.calls, strings.Join(args, " "))
	r.mu.Unlock()
	switch {
	case args[0] == "version", args[0] == "ps", args[0] == "volume" && args[1] == "ls":
		return nil, nil
	}
	r.t.Errorf("unexpected container command %q", args)
	return nil, errors.New("unexpected")
}

func noCLI(string) (string, error) { return "", errors.New("not found") }

func onlyDocker(name string) (string, error) {
	if name == "docker" {
		return "/usr/bin/docker", nil
	}
	return "", errors.New("not found")
}

// clearNeo4jEnv makes the tests independent of mise's NEO4J_URI etc.
func clearNeo4jEnv(t *testing.T) {
	for _, k := range []string{"NEO4J_URI", "DEPGRAPH_NEO4J_PROVIDER", "DEPGRAPH_CONTAINER_CLI"} {
		t.Setenv(k, "")
	}
}

func TestStoreChoicePrecedence(t *testing.T) {
	yes, no := true, false
	tests := []struct {
		name string
		o    options
		s    config.StoreSettings
		want storeChoice
	}{
		{"nothing set", options{}, config.StoreSettings{}, storeChoice{}},
		{"file only", options{}, config.StoreSettings{Provider: "container", Persist: &yes, ContainerCLI: "podman"}, storeChoice{"container", true, "podman"}},
		{"flags beat the file", options{neo4jProvider: "external", containerCLI: "nerdctl", persist: false, persistSet: true},
			config.StoreSettings{Provider: "container", Persist: &yes, ContainerCLI: "podman"}, storeChoice{"external", false, "nerdctl"}},
		{"--persist", options{persist: true, persistSet: true}, config.StoreSettings{Persist: &no}, storeChoice{persist: true}},
		{"unset flag keeps the file", options{persist: false}, config.StoreSettings{Persist: &yes}, storeChoice{persist: true}},
	}
	for _, tt := range tests {
		if got := tt.o.storeChoice(tt.s); got != tt.want {
			t.Errorf("%s: %+v, want %+v", tt.name, got, tt.want)
		}
	}
}

func TestProviderOrder(t *testing.T) {
	o := &options{neo4jURI: "bolt://x:7687"}
	var names []string
	for _, p := range o.providers(storeChoice{}) {
		names = append(names, p.Name())
	}
	if strings.Join(names, ",") != "external,container,local" {
		t.Errorf("order = %v", names)
	}
	p, err := neo4jprovider.Select(context.Background(), "", o.providers(storeChoice{}))
	if err != nil || p.Name() != neo4jprovider.External {
		t.Errorf("a configured URI selects external: %v %v", p, err)
	}
}

func TestScanWithoutAnyNeo4jIsOneClearLine(t *testing.T) {
	clearNeo4jEnv(t)
	dir := t.TempDir()
	var out bytes.Buffer
	root := newRootCommand(&options{containerConfig: container.ProviderConfig{LookPath: noCLI}})
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"scan", "--root", dir, "--claude-md", ""})
	err := root.Execute()
	if err == nil {
		t.Fatal("scan without Neo4j and without a container runtime must fail")
	}
	msg := err.Error()
	for _, s := range []string{"no Neo4j available", "--neo4j / NEO4J_URI", "docker, podman, nerdctl", "local: planned"} {
		if !strings.Contains(msg, s) {
			t.Errorf("error %q lacks %q", msg, s)
		}
	}
	if strings.Contains(msg, "\n") {
		t.Errorf("error must be one line: %q", msg)
	}
}

func TestHookIsSilentWithoutRunningStore(t *testing.T) {
	clearNeo4jEnv(t)
	dir := t.TempDir()
	for name, cfg := range map[string]container.ProviderConfig{
		"no runtime":       {LookPath: noCLI},
		"no store running": {LookPath: onlyDocker, Runner: &readOnlyCLI{t: t}},
	} {
		var out, errOut bytes.Buffer
		root := newRootCommand(&options{containerConfig: cfg})
		root.SetOut(&out)
		root.SetErr(&errOut)
		root.SetIn(strings.NewReader(`{"session_id":"s1","cwd":"` + dir + `","hook_event_name":"UserPromptSubmit","prompt":"hi"}`))
		root.SetArgs([]string{"hook", "prompt", "--timeout", "2s"})
		start := time.Now()
		if err := root.Execute(); err != nil {
			t.Fatalf("%s: err = %v (hook must exit 0)", name, err)
		}
		if out.Len() != 0 || errOut.Len() != 0 {
			t.Errorf("%s: stdout=%q stderr=%q, want nothing", name, out.String(), errOut.String())
		}
		if d := time.Since(start); d > 3*time.Second {
			t.Errorf("%s took %s", name, d)
		}
	}
}

func TestServeNeverStartsAnEphemeralStore(t *testing.T) {
	clearNeo4jEnv(t)
	cli := &readOnlyCLI{t: t}
	root := newRootCommand(&options{containerConfig: container.ProviderConfig{LookPath: onlyDocker, Runner: cli}})
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"serve", "--root", t.TempDir()})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "no running Neo4j") || !strings.Contains(err.Error(), "depgraph watch") {
		t.Errorf("err = %v", err)
	}
}

// The watchdog child is reachable under the name the spawner uses, is hidden
// and parses its own arguments (no root preparation, no other flags).
func TestContainerWatchdogCommand(t *testing.T) {
	root := newRootCommand(&options{})
	c, _, err := root.Find([]string{container.WatchdogCommand})
	if err != nil || c.Name() != container.WatchdogCommand || !c.Hidden {
		t.Fatalf("watchdog command = %v, %v", c, err)
	}
	root.SetIn(strings.NewReader(""))
	root.SetOut(&bytes.Buffer{})
	root.SetErr(&bytes.Buffer{})
	root.SetArgs(container.WatchdogArgs("docker", "abc", "kill"))
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), `unknown --mode "kill"`) {
		t.Errorf("err = %v", err)
	}
}

func TestStoreLsWithNothingManaged(t *testing.T) {
	clearNeo4jEnv(t)
	var out bytes.Buffer
	root := newRootCommand(&options{containerConfig: container.ProviderConfig{LookPath: onlyDocker, Runner: &readOnlyCLI{t: t}}})
	root.SetOut(&out)
	root.SetArgs([]string{"store", "ls", "--root", t.TempDir()})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no managed Neo4j containers or volumes") {
		t.Errorf("out = %q", out.String())
	}
}
