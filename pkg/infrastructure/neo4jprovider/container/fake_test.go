package container

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// fakeCLI is an in-memory docker-compatible CLI: it understands exactly the
// commands the provider runs and records every call.
type fakeCLI struct {
	mu         sync.Mutex
	answers    bool // version succeeds
	containers map[string]*fakeContainer
	volumes    map[string]map[string]string
	images     map[string]bool
	calls      [][]string
	envs       [][]string
	nextID     int
	nextPort   int
	// runErr, when set, fails `run` with it; rmErr fails `rm`.
	runErr error
	rmErr  error
	// onRun runs after a successful `run` (e.g. a racing second start).
	onRun func(f *fakeCLI)
}

type fakeContainer struct {
	id, name string
	labels   map[string]string
	env      []string
	state    string
	port     int
	autoRm   bool
	volume   string
}

func newFakeCLI() *fakeCLI {
	return &fakeCLI{
		answers:    true,
		containers: map[string]*fakeContainer{},
		volumes:    map[string]map[string]string{},
		images:     map[string]bool{},
		nextPort:   40000,
	}
}

func (f *fakeCLI) lookPath(name string) (string, error) {
	if name == "docker" {
		return "/usr/bin/docker", nil
	}
	return "", errors.New("not found")
}

// add puts a container into the fake directly.
func (f *fakeCLI) add(name, state string, port int, labels map[string]string, pw string) *fakeContainer {
	f.nextID++
	c := &fakeContainer{
		id: fmt.Sprintf("c%02d", f.nextID), name: name, labels: labels, state: state, port: port,
		env: []string{"PATH=/usr/bin", "NEO4J_AUTH=neo4j/" + pw},
	}
	f.containers[c.id] = c
	return c
}

func (f *fakeCLI) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		out = append(out, strings.Join(c, " "))
	}
	return out
}

func (f *fakeCLI) ran(prefix string) bool {
	for _, c := range f.commands() {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func (f *fakeCLI) Run(_ context.Context, env []string, _ string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, args)
	f.envs = append(f.envs, env)
	if len(args) == 0 {
		return nil, errors.New("no args")
	}
	switch args[0] {
	case "version":
		if !f.answers {
			return nil, errors.New("Cannot connect to the Docker daemon")
		}
		return []byte("Version: 1\n"), nil
	case "ps":
		var filters []string
		for i := 0; i < len(args); i++ {
			if args[i] == "--filter" {
				filters = append(filters, strings.TrimPrefix(args[i+1], "label="))
				i++
			}
		}
		var ids []string
		for id, c := range f.containers {
			ok := true
			for _, flt := range filters {
				k, v, _ := strings.Cut(flt, "=")
				if c.labels[k] != v {
					ok = false
				}
			}
			if ok {
				ids = append(ids, id)
			}
		}
		return []byte(strings.Join(ids, "\n") + "\n"), nil
	case "inspect":
		var out []map[string]any
		for _, id := range args[1:] {
			c, ok := f.containers[id]
			if !ok {
				return nil, fmt.Errorf("Error: No such object: %s", id)
			}
			ports := map[string]any{}
			if c.state == "running" {
				ports["7687/tcp"] = []map[string]string{{"HostIp": "127.0.0.1", "HostPort": strconv.Itoa(c.port)}}
			}
			out = append(out, map[string]any{
				"Id": c.id, "Name": "/" + c.name,
				"Config":          map[string]any{"Labels": c.labels, "Env": c.env},
				"State":           map[string]any{"Status": c.state, "Running": c.state == "running"},
				"NetworkSettings": map[string]any{"Ports": ports},
				"HostConfig":      map[string]any{"PortBindings": map[string]any{"7687/tcp": []map[string]string{{"HostIp": "127.0.0.1", "HostPort": ""}}}},
			})
		}
		return json.Marshal(out)
	case "port":
		c, ok := f.containers[args[1]]
		if !ok || c.state != "running" {
			return nil, errors.New("no public port")
		}
		return []byte(fmt.Sprintf("127.0.0.1:%d\n", c.port)), nil
	case "image":
		if f.images[args[len(args)-1]] {
			return []byte("[{}]"), nil
		}
		return nil, errors.New("Error: No such image")
	case "pull":
		f.images[args[1]] = true
		return nil, nil
	case "run":
		if f.runErr != nil {
			return nil, f.runErr
		}
		c := &fakeContainer{labels: map[string]string{}, state: "running"}
		for i := 1; i < len(args)-1; i++ {
			switch args[i] {
			case "--rm":
				c.autoRm = true
			case "--name":
				c.name = args[i+1]
				i++
			case "--label":
				k, v, _ := strings.Cut(args[i+1], "=")
				c.labels[k] = v
				i++
			case "-e":
				e := args[i+1]
				if !strings.Contains(e, "=") { // -e NAME: from the CLI's environment
					for _, kv := range env {
						if strings.HasPrefix(kv, e+"=") {
							e = kv
						}
					}
				}
				c.env = append(c.env, e)
				i++
			case "-v":
				c.volume, _, _ = strings.Cut(args[i+1], ":")
				i++
			case "-p", "--tmpfs":
				i++
			}
		}
		for _, o := range f.containers {
			if o.name == c.name {
				return nil, fmt.Errorf("docker run: Conflict. The container name %q is already in use", "/"+c.name)
			}
		}
		if c.volume != "" {
			if _, ok := f.volumes[c.volume]; !ok {
				f.volumes[c.volume] = map[string]string{}
			}
		}
		f.nextID++
		f.nextPort++
		c.id = fmt.Sprintf("c%02d", f.nextID)
		c.port = f.nextPort
		f.containers[c.id] = c
		if f.onRun != nil {
			f.onRun(f)
		}
		return []byte(c.id + "\n"), nil
	case "start":
		c, ok := f.containers[args[1]]
		if !ok {
			return nil, errors.New("No such container")
		}
		f.nextPort++
		c.state, c.port = "running", f.nextPort
		return nil, nil
	case "stop":
		id := args[len(args)-1]
		c, ok := f.containers[id]
		if !ok {
			return nil, errors.New("No such container: " + id)
		}
		if c.autoRm {
			delete(f.containers, id)
		} else {
			c.state = "exited"
		}
		return nil, nil
	case "rm":
		if f.rmErr != nil {
			return nil, f.rmErr
		}
		id := args[len(args)-1]
		if _, ok := f.containers[id]; !ok {
			return nil, errors.New("Error response from daemon: No such container: " + id)
		}
		delete(f.containers, id)
		return nil, nil
	case "volume":
		switch args[1] {
		case "inspect":
			var out []map[string]any
			for _, n := range args[2:] {
				l, ok := f.volumes[n]
				if !ok {
					return nil, errors.New("Error: no such volume")
				}
				out = append(out, map[string]any{"Name": n, "Labels": l})
			}
			return json.Marshal(out)
		case "create":
			l := map[string]string{}
			for i := 2; i < len(args)-1; i++ {
				if args[i] == "--label" {
					k, v, _ := strings.Cut(args[i+1], "=")
					l[k] = v
					i++
				}
			}
			f.volumes[args[len(args)-1]] = l
			return nil, nil
		case "ls":
			var names []string
			for n, l := range f.volumes {
				if l[LabelManaged] == "1" {
					names = append(names, n)
				}
			}
			return []byte(strings.Join(names, "\n")), nil
		case "rm":
			n := args[len(args)-1]
			for _, c := range f.containers {
				if c.volume == n {
					return nil, errors.New("volume is in use")
				}
			}
			delete(f.volumes, n)
			return nil, nil
		}
	}
	return nil, fmt.Errorf("fake: unexpected command %q", args)
}
