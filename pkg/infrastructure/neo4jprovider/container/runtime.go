package container

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Runner runs a container CLI with a fixed argument list (never a shell).
// env holds extra NAME=value pairs for the CLI's own environment (on top of
// the current one), used to hand the password to `run -e NAME` without
// putting it on the command line.
type Runner interface {
	Run(ctx context.Context, env []string, cli string, args ...string) ([]byte, error)
}

// ExecRunner runs the CLI with os/exec. The user's DOCKER_HOST, docker
// context, CONTAINER_HOST etc. apply because the environment is inherited.
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, env []string, cli string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, cli, args...)
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = time.Second
	if err := cmd.Run(); err != nil {
		msg := strings.Join(strings.Fields(stderr.String()), " ")
		if msg == "" {
			return stdout.Bytes(), fmt.Errorf("%s %s: %w", filepath.Base(cli), firstArgs(args), err)
		}
		return stdout.Bytes(), fmt.Errorf("%s %s: %s", filepath.Base(cli), firstArgs(args), msg)
	}
	return stdout.Bytes(), nil
}

// firstArgs names the subcommand in errors (never the full argument list).
func firstArgs(args []string) string {
	var out []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") || len(out) == 2 {
			break
		}
		out = append(out, a)
	}
	return strings.Join(out, " ")
}

// Flavor is a container CLI family. They share the subset of commands this
// package uses; inspect output differs in details handled by parseInspect.
type Flavor string

const (
	Docker  Flavor = "docker"
	Podman  Flavor = "podman"
	Nerdctl Flavor = "nerdctl"
)

// Candidates are tried in this order when no CLI is configured.
var Candidates = []Flavor{Docker, Podman, Nerdctl}

// Runtime is a container CLI that answered.
type Runtime struct {
	// CLI is what is executed (a name on PATH or a path).
	CLI    string
	Flavor Flavor
}

func (r Runtime) String() string { return string(r.Flavor) }

// probeTimeout bounds `<cli> version` (it contacts the daemon / machine).
const probeTimeout = 2 * time.Second

// flavorOf derives the family from a CLI name or path ("" when unknown).
func flavorOf(cli string) Flavor {
	base := strings.TrimSuffix(strings.ToLower(filepath.Base(cli)), ".exe")
	for _, f := range Candidates {
		if base == string(f) {
			return f
		}
	}
	return ""
}

// errUnsupportedCLI explains the CLIs that cannot be used.
func checkSupported(cli string) error {
	base := strings.TrimSuffix(strings.ToLower(filepath.Base(cli)), ".exe")
	if base == "ctr" {
		return errors.New("ctr (bare containerd) is not supported: it cannot publish ports; install nerdctl")
	}
	return nil
}

// DetectRuntime returns the container CLI to use: explicit (a name or path) when
// set, else the first of Candidates that is on PATH and answers
// `<cli> version` within probeTimeout.
func DetectRuntime(ctx context.Context, explicit string, lookPath func(string) (string, error), run Runner) (Runtime, error) {
	probe := func(cli string) error {
		pctx, cancel := context.WithTimeout(ctx, probeTimeout)
		defer cancel()
		_, err := run.Run(pctx, nil, cli, "version")
		return err
	}
	if explicit != "" {
		if err := checkSupported(explicit); err != nil {
			return Runtime{}, err
		}
		if _, err := lookPath(explicit); err != nil {
			return Runtime{}, fmt.Errorf("container CLI %q not found", explicit)
		}
		f := flavorOf(explicit)
		if f == "" {
			f = Docker // a docker-compatible CLI under another name
		}
		if err := probe(explicit); err != nil {
			return Runtime{}, fmt.Errorf("container CLI %s does not answer: %w", explicit, err)
		}
		return Runtime{CLI: explicit, Flavor: f}, nil
	}
	var found []string
	for _, f := range Candidates {
		if _, err := lookPath(string(f)); err != nil {
			continue
		}
		if err := probe(string(f)); err != nil {
			found = append(found, fmt.Sprintf("%s does not answer (is its daemon / machine running?)", f))
			continue
		}
		return Runtime{CLI: string(f), Flavor: f}, nil
	}
	if len(found) == 0 {
		return Runtime{}, errors.New("no container runtime: none of docker, podman, nerdctl is on PATH")
	}
	return Runtime{}, errors.New(strings.Join(found, "; "))
}
