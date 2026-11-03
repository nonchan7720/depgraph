package container

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Instance is one managed container as `<cli> inspect` reports it.
type Instance struct {
	ID       string
	Name     string
	State    string // running, exited, created, ... (lower case)
	RootHash string
	RootPath string
	Project  string
	Persist  bool
	OwnerPID int
	Host     string
	Volume   string
	// Password is NEO4J_AUTH's password from the container's environment.
	Password string
	// Port is the host port bolt is published on (0: none).
	Port int
}

// Running reports whether the container is up.
func (i Instance) Running() bool { return i.State == "running" }

// inspectJSON is the subset of `inspect` output read here. docker, podman
// and nerdctl (its docker-compatible mode) share these paths:
//   - Id, Name ("/name" in docker, "name" in podman / nerdctl)
//   - Config.Labels, Config.Env
//   - State.Status
//   - NetworkSettings.Ports{"7687/tcp": [{HostIp, HostPort}]} (null when stopped)
//   - HostConfig.PortBindings, the requested bindings (docker leaves HostPort
//     "" for a random port; nerdctl fills it in)
type inspectJSON struct {
	ID     string `json:"Id"`
	Name   string `json:"Name"`
	Config struct {
		Labels map[string]string `json:"Labels"`
		Env    []string          `json:"Env"`
	} `json:"Config"`
	State struct {
		Status  string `json:"Status"`
		Running bool   `json:"Running"`
	} `json:"State"`
	NetworkSettings struct {
		Ports map[string][]portBinding `json:"Ports"`
	} `json:"NetworkSettings"`
	HostConfig struct {
		PortBindings map[string][]portBinding `json:"PortBindings"`
	} `json:"HostConfig"`
}

type portBinding struct {
	HostIP   string `json:"HostIp"`
	HostPort string `json:"HostPort"`
}

// parseInspect decodes `<cli> inspect <id>...` (a JSON array).
func parseInspect(b []byte) ([]Instance, error) {
	var raw []inspectJSON
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("parse inspect output: %w", err)
	}
	out := make([]Instance, 0, len(raw))
	for _, r := range raw {
		l := r.Config.Labels
		state := strings.ToLower(r.State.Status)
		if state == "" && r.State.Running {
			state = "running"
		}
		pid, _ := strconv.Atoi(l[LabelOwnerPID])
		out = append(out, Instance{
			ID:       r.ID,
			Name:     strings.TrimPrefix(r.Name, "/"),
			State:    state,
			RootHash: l[LabelRoot],
			RootPath: l[LabelRootPath],
			Project:  l[LabelProject],
			Persist:  l[LabelPersist] == "1",
			OwnerPID: pid,
			Host:     l[LabelHost],
			Volume:   l[LabelVolume],
			Password: authPassword(r.Config.Env),
			Port:     firstPort(r.NetworkSettings.Ports[boltPort], r.HostConfig.PortBindings[boltPort]),
		})
	}
	return out, nil
}

// authPassword returns the password of NEO4J_AUTH=neo4j/<password>.
func authPassword(env []string) string {
	for _, e := range env {
		v, ok := strings.CutPrefix(e, "NEO4J_AUTH=")
		if !ok {
			continue
		}
		if _, pw, ok := strings.Cut(v, "/"); ok {
			return pw
		}
	}
	return ""
}

// firstPort returns the first published host port of the live bindings,
// else of the requested ones (0 for a port still to be allocated).
func firstPort(lists ...[]portBinding) int {
	for _, l := range lists {
		for _, b := range l {
			if p, err := strconv.Atoi(b.HostPort); err == nil && p > 0 {
				return p
			}
		}
	}
	return 0
}

// parsePortOutput reads `<cli> port <id> 7687/tcp`: one "host:port" per
// line ("127.0.0.1:32768", "[::1]:32768", "0.0.0.0:32768").
func parsePortOutput(b []byte) int {
	for line := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
		line = strings.TrimSpace(line)
		if i := strings.LastIndex(line, " -> "); i >= 0 { // "7687/tcp -> 127.0.0.1:1" (no port argument)
			line = line[i+4:]
		}
		_, port, err := net.SplitHostPort(line)
		if err != nil {
			continue
		}
		if p, err := strconv.Atoi(port); err == nil && p > 0 {
			return p
		}
	}
	return 0
}

// parseIDs splits `ps -q` / `volume ls -q` output.
func parseIDs(b []byte) []string {
	return strings.Fields(string(b))
}
