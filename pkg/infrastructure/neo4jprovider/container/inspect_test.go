package container

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Fixtures in testdata/ are inspect / port outputs of the store this package
// starts (written by the integration test with DEPGRAPH_RECORD_FIXTURES set,
// password / root / host replaced):
//   - docker_*: recorded with Docker 29
//   - podman_*: podman's inspect layout (rootless, slirp4netns); refreshed from the CI podman job
//   - nerdctl_*: built from nerdctl v2.4's dockercompat inspect types (not recorded: nerdctl is not in CI)
func TestParseInspectFixtures(t *testing.T) {
	for _, flavor := range Candidates {
		t.Run(string(flavor), func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join("testdata", string(flavor)+"_inspect.json"))
			if err != nil {
				t.Fatal(err)
			}
			insts, err := parseInspect(b)
			if err != nil {
				t.Fatal(err)
			}
			if len(insts) != 1 {
				t.Fatalf("instances = %d", len(insts))
			}
			i := insts[0]
			want := Instance{
				ID:       i.ID,
				Name:     "depgraph-zz_test_integrationephemeral_6557c6c1-1036b289",
				State:    "running",
				RootHash: "f683a430aa33895d",
				RootPath: "/work/app",
				Project:  "zz_test_integrationephemeral_6557c6c1",
				Persist:  false,
				OwnerPID: 60318,
				Host:     "hostid000000",
				Password: "fixturepassword",
				Port:     32773,
			}
			if i.ID == "" || i != want {
				t.Errorf("parsed\n got %+v\nwant %+v", i, want)
			}
			port, err := os.ReadFile(filepath.Join("testdata", string(flavor)+"_port.txt"))
			if err != nil {
				t.Fatal(err)
			}
			if p := parsePortOutput(port); p != 32773 {
				t.Errorf("port output %q = %d", port, p)
			}
		})
	}
}

func TestParseInspectStoppedUsesNoStalePort(t *testing.T) {
	// docker: a stopped container has Ports null and PortBindings HostPort "".
	b := []byte(`[{"Id":"x","Name":"/n","Config":{"Labels":{"depgraph.root":"h"},"Env":null},"State":{"Status":"exited"},
		"NetworkSettings":{"Ports":{}},"HostConfig":{"PortBindings":{"7687/tcp":[{"HostIp":"127.0.0.1","HostPort":""}]}}}]`)
	insts, err := parseInspect(b)
	if err != nil || len(insts) != 1 || insts[0].Port != 0 || insts[0].Running() || insts[0].Password != "" {
		t.Errorf("= %+v, %v", insts, err)
	}
	if _, err := parseInspect([]byte("not json")); err == nil {
		t.Error("garbage must be an error")
	}
}

func TestParsePortOutput(t *testing.T) {
	for in, want := range map[string]int{
		"127.0.0.1:32768\n":                  32768,
		"[::1]:40000\n":                      40000,
		"0.0.0.0:5000\n[::]:5000\n":          5000,
		"7687/tcp -> 127.0.0.1:32769\n":      32769,
		"":                                   0,
		"Error: no public port '7687/tcp'\n": 0,
	} {
		if got := parsePortOutput([]byte(in)); got != want {
			t.Errorf("%q = %d, want %d", in, got, want)
		}
	}
}

// The managed image must be the one docker-compose.yml and the CI service
// container pin (Renovate updates all three; this keeps a manual edit honest).
func TestImageMatchesComposeAndCI(t *testing.T) {
	short := strings.TrimPrefix(Image, "docker.io/library/")
	for _, f := range []string{"docker-compose.yml", ".github/workflows/ci.yml"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "..", "..", f))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), "image: "+short) {
			t.Errorf("%s does not pin image %s", f, short)
		}
	}
}
