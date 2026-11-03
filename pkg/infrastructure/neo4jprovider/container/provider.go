// Package container is the Neo4j provider that runs Neo4j in a container
// with a docker-compatible CLI (docker, podman or nerdctl) and cleans it up.
//
// Lifecycle:
//   - ephemeral (default): `run -d --rm` with /data on tmpfs; the process that
//     started it stops it on exit. A crashed owner's container is removed by
//     the next depgraph start on the same machine (owner PID + host labels).
//   - persistent: data in a named volume per project, no --rm. The data
//     persists, not the process: the process that started (or restarted) the
//     container stops it on exit, and the next start of the root starts the
//     same container again. `depgraph store down` stops it, `depgraph store
//     rm` deletes container and volume.
//
// Bolt is published on 127.0.0.1 only, on a random port, with a random
// password handed to the CLI through its environment (never argv). Commands
// find a running store by labels and read port and password from inspect, so
// no connection information is written to disk.
package container

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nonchan7720/depgraph/pkg/infrastructure/neo4jprovider"
)

// ProviderConfig configures a Provider; zero fields take the defaults.
type ProviderConfig struct {
	// CLI is the container CLI to run (name or path); "" detects one.
	CLI      string
	Runner   Runner
	LookPath func(string) (string, error)
	// Alive reports whether a PID lives on this machine.
	Alive func(pid int) bool
	PID   int    // this process (owner label)
	Host  string // HostID()
	Image string
	// Poll is the interval of readiness / port polling; StartTimeout bounds
	// the wait for a container to accept connections.
	Poll         time.Duration
	StartTimeout time.Duration
	// Watchdog starts the watchdog child of a container this process
	// started (nil: SpawnWatchdog).
	Watchdog Spawner
}

// Provider is the container Neo4j provider.
type Provider struct {
	cfg ProviderConfig

	mu    sync.Mutex
	rt    Runtime
	rtErr error
	done  bool
}

var _ neo4jprovider.Provider = (*Provider)(nil)

// New returns a provider; nothing is run until a method needs the runtime.
func New(cfg ProviderConfig) *Provider {
	if cfg.Runner == nil {
		cfg.Runner = ExecRunner{}
	}
	if cfg.LookPath == nil {
		cfg.LookPath = exec.LookPath
	}
	if cfg.Alive == nil {
		cfg.Alive = processAlive
	}
	if cfg.PID == 0 {
		cfg.PID = os.Getpid()
	}
	if cfg.Host == "" {
		cfg.Host = HostID()
	}
	if cfg.Image == "" {
		cfg.Image = Image
	}
	if cfg.Poll == 0 {
		cfg.Poll = 250 * time.Millisecond
	}
	if cfg.StartTimeout == 0 {
		cfg.StartTimeout = 2 * time.Minute
	}
	if cfg.Watchdog == nil {
		cfg.Watchdog = SpawnWatchdog
	}
	return &Provider{cfg: cfg}
}

// arm starts the watchdog of a container this process started. Without one
// (it could not be started) the container is still cleaned up on a normal
// exit and by the next start's sweep, which is reported.
func (p *Provider) arm(o neo4jprovider.StoreRequest, id, name string, mode WatchMode) func(context.Context) {
	disarm, err := p.cfg.Watchdog(p.rt.CLI, id, mode)
	if err != nil {
		o.Logf("no watchdog for container %s (%v): it is still cleaned up on a normal exit and by the next start", name, err)
		return func(context.Context) {}
	}
	return disarm
}

// then runs release, then disarm (the watchdog then finds nothing to do).
func then(release func(context.Context) error, disarm func(context.Context)) func(context.Context) error {
	return func(ctx context.Context) error {
		err := release(ctx)
		disarm(ctx)
		return err
	}
}

func (p *Provider) Name() string { return neo4jprovider.Container }

// Runtime returns the detected container CLI (detected once).
func (p *Provider) Runtime(ctx context.Context) (Runtime, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.done {
		p.rt, p.rtErr = DetectRuntime(ctx, p.cfg.CLI, p.cfg.LookPath, p.cfg.Runner)
		// a probe cut short by ctx is not a verdict on the runtime
		p.done = ctx.Err() == nil
	}
	return p.rt, p.rtErr
}

// Available reports whether a container CLI answers.
func (p *Provider) Available(ctx context.Context) error {
	_, err := p.Runtime(ctx)
	return err
}

func (p *Provider) run(ctx context.Context, env []string, args ...string) ([]byte, error) {
	rt, err := p.Runtime(ctx)
	if err != nil {
		return nil, err
	}
	return p.cfg.Runner.Run(ctx, env, rt.CLI, args...)
}

// list returns the managed containers matching every label filter
// ("key=value").
func (p *Provider) list(ctx context.Context, labels ...string) ([]Instance, error) {
	args := []string{"ps", "-a", "-q", "--filter", "label=" + LabelManaged + "=1"}
	for _, l := range labels {
		args = append(args, "--filter", "label="+l)
	}
	out, err := p.run(ctx, nil, args...)
	if err != nil {
		return nil, err
	}
	ids := parseIDs(out)
	if len(ids) == 0 {
		return nil, nil
	}
	insts, err := p.inspect(ctx, ids...)
	if err == nil {
		return insts, nil
	}
	// one of them went away between ps and inspect: inspect one by one
	var all []Instance
	for _, id := range ids {
		if one, err := p.inspect(ctx, id); err == nil {
			all = append(all, one...)
		}
	}
	return all, nil
}

func (p *Provider) inspect(ctx context.Context, ids ...string) ([]Instance, error) {
	out, err := p.run(ctx, nil, append([]string{"inspect"}, ids...)...)
	if err != nil {
		return nil, err
	}
	insts, err := parseInspect(out)
	if err != nil {
		return nil, err
	}
	// only ours, whatever a CLI's filter did
	kept := insts[:0]
	for _, i := range insts {
		if i.RootHash != "" {
			kept = append(kept, i)
		}
	}
	return kept, nil
}

// orphan: an ephemeral container of this machine whose owner process is gone.
func (p *Provider) orphan(i Instance) bool {
	return !i.Persist && i.Host == p.cfg.Host && i.OwnerPID > 0 && i.OwnerPID != p.cfg.PID && !p.cfg.Alive(i.OwnerPID)
}

// Sweep removes the ephemeral containers of this machine whose depgraph
// process is gone (crashed or killed). Persistent containers, containers of
// other machines and anything without our labels are never touched.
func (p *Provider) Sweep(ctx context.Context, o neo4jprovider.StoreRequest) []string {
	insts, err := p.list(ctx, LabelPersist+"=0", LabelHost+"="+p.cfg.Host)
	if err != nil {
		return nil
	}
	var removed []string
	for _, i := range insts {
		if !p.orphan(i) {
			continue
		}
		if _, err := p.run(ctx, nil, "rm", "-f", i.ID); err == nil {
			removed = append(removed, i.Name)
			o.Logf("removed leftover Neo4j container %s (its depgraph process %d is gone)", i.Name, i.OwnerPID)
		}
	}
	return removed
}

func (p *Provider) conn(i Instance) neo4jprovider.Conn {
	return neo4jprovider.Conn{
		URI:        "bolt://127.0.0.1:" + strconv.Itoa(i.Port),
		User:       "neo4j",
		Password:   i.Password,
		Database:   "neo4j",
		Persistent: i.Persist,
		Describe:   fmt.Sprintf("container %s (%s)", i.Name, p.rt.Flavor),
	}
}

// Discover returns the running store of root without changing anything.
// An ephemeral store whose owner is gone does not count.
func (p *Provider) Discover(ctx context.Context, root string) (neo4jprovider.Conn, bool, error) {
	insts, err := p.list(ctx, LabelRoot+"="+RootHash(root))
	if err != nil {
		return neo4jprovider.Conn{}, false, err
	}
	for _, i := range insts {
		if i.Running() && i.Port > 0 && i.Password != "" && !p.orphan(i) {
			return p.conn(i), true, nil
		}
	}
	return neo4jprovider.Conn{}, false, nil
}

// Provide reuses the running store of the root, restarts its stopped
// persistent store, or starts a new one as the policy allows.
func (p *Provider) Provide(ctx context.Context, o neo4jprovider.StoreRequest) (neo4jprovider.Conn, error) {
	if _, err := p.Runtime(ctx); err != nil {
		return neo4jprovider.Conn{}, err
	}
	if o.Policy != neo4jprovider.Existing {
		p.Sweep(ctx, o)
	}
	insts, err := p.list(ctx, LabelRoot+"="+RootHash(o.Root))
	if err != nil {
		return neo4jprovider.Conn{}, err
	}
	var stopped []Instance
	for _, i := range insts {
		if p.orphan(i) {
			continue
		}
		if !i.Running() {
			stopped = append(stopped, i)
			continue
		}
		i, err := p.waitPort(ctx, i)
		if err != nil {
			return neo4jprovider.Conn{}, err
		}
		c := p.conn(i)
		mode := "ephemeral"
		if i.Persist {
			mode = "persistent"
		}
		o.Logf("using the running Neo4j %s, %s", c.Describe, mode)
		if o.Persist && !i.Persist {
			o.Logf("  persistence takes effect once it stops (it was started without --persist)")
		}
		if err := p.waitReady(ctx, o, c, i.ID); err != nil {
			return neo4jprovider.Conn{}, err
		}
		return c, nil
	}
	if o.Policy == neo4jprovider.Existing {
		return neo4jprovider.Conn{}, neo4jprovider.ErrNotRunning
	}
	for _, i := range stopped {
		switch {
		case i.Persist:
			return p.restart(ctx, o, i)
		case starting(i.State) && i.OwnerPID != p.cfg.PID && (i.Host != p.cfg.Host || p.cfg.Alive(i.OwnerPID)):
			// another live depgraph process is starting it
			return p.awaitOther(ctx, o)
		default:
			// an ephemeral container that stopped without being removed
			_, _ = p.run(ctx, nil, "rm", "-f", i.ID)
		}
	}
	if o.Policy == neo4jprovider.StartIfPersistent && !o.Persist {
		return neo4jprovider.Conn{}, neo4jprovider.ErrNotRunning
	}
	return p.start(ctx, o)
}

// starting reports a state on the way to running (podman says "configured"
// before "created").
func starting(state string) bool {
	return state == "created" || state == "configured" || state == "restarting"
}

// restart starts a stopped persistent container (same password, same volume;
// the host port may change).
func (p *Provider) restart(ctx context.Context, o neo4jprovider.StoreRequest, i Instance) (neo4jprovider.Conn, error) {
	o.Logf("starting the persistent Neo4j container %s (%s); this takes 10-20 s", i.Name, p.rt.Flavor)
	began := time.Now()
	if _, err := p.run(ctx, nil, "start", i.ID); err != nil {
		return neo4jprovider.Conn{}, err
	}
	release := then(p.stopper(i.ID), p.arm(o, i.ID, i.Name, WatchStop))
	i, err := p.waitPort(ctx, i)
	if err != nil {
		_ = release(ctx)
		return neo4jprovider.Conn{}, err
	}
	c := p.conn(i)
	c.Started = true
	if err := p.waitReady(ctx, o, c, i.ID); err != nil {
		_ = release(ctx)
		return neo4jprovider.Conn{}, err
	}
	o.Logf("Neo4j ready in %.1fs (%s)", time.Since(began).Seconds(), c.URI)
	return c.WithRelease(release), nil
}

// stopper stops (never removes) a persistent container: the data is what
// persists, not the process. The next start of the root starts it again
// (same container, so the password in its environment survives).
func (p *Provider) stopper(id string) func(context.Context) error {
	return func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if _, err := p.run(ctx, nil, "stop", "-t", "10", id); err != nil && !isNoSuch(err) {
			return err
		}
		return nil
	}
}

func randomPassword() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// RunArgs is the argument list of `<cli> run` for a new store (exported for
// documentation and tests). The password is not in it: `-e NEO4J_AUTH`
// takes the value from the CLI's environment.
func RunArgs(image, name string, labels []string, persistVolume string) []string {
	args := []string{"run", "-d"}
	if persistVolume == "" {
		args = append(args, "--rm")
	}
	args = append(args, "--name", name)
	for _, l := range labels {
		args = append(args, "--label", l)
	}
	args = append(args, "-p", "127.0.0.1::7687", "-e", "NEO4J_AUTH")
	for _, e := range neo4jEnv {
		args = append(args, "-e", e)
	}
	args = append(args, "--tmpfs", "/logs")
	if persistVolume == "" {
		args = append(args, "--tmpfs", "/data")
	} else {
		args = append(args, "-v", persistVolume+":/data")
	}
	return append(args, image)
}

// start runs a new container for the root.
func (p *Provider) start(ctx context.Context, o neo4jprovider.StoreRequest) (neo4jprovider.Conn, error) {
	name := ContainerName(o.Project, o.Root)
	labels := []string{
		LabelManaged + "=1",
		LabelRoot + "=" + RootHash(o.Root),
		LabelRootPath + "=" + o.Root,
		LabelProject + "=" + o.Project,
		LabelPersist + "=0",
		LabelOwnerPID + "=" + strconv.Itoa(p.cfg.PID),
		LabelHost + "=" + p.cfg.Host,
	}
	volume := ""
	createdVolume := false
	if o.Persist {
		labels[4] = LabelPersist + "=1"
		volume = VolumeName(o.Project)
		labels = append(labels, LabelVolume+"="+volume)
		var err error
		if createdVolume, err = p.ensureVolume(ctx, o, volume); err != nil {
			return neo4jprovider.Conn{}, err
		}
	}
	if _, err := p.run(ctx, nil, "image", "inspect", p.cfg.Image); err != nil {
		o.Logf("pulling %s (first run only, about 1 GB)", p.cfg.Image)
		if _, err := p.run(ctx, nil, "pull", p.cfg.Image); err != nil {
			return neo4jprovider.Conn{}, err
		}
	}
	pw, err := randomPassword()
	if err != nil {
		return neo4jprovider.Conn{}, err
	}
	if o.Persist {
		o.Logf("starting Neo4j in a %s container %s (persistent: data in volume %s, kept until `depgraph store rm`); this takes 10-20 s", p.rt.Flavor, name, volume)
	} else {
		o.Logf("starting Neo4j in a %s container %s (ephemeral: removed with its data when depgraph exits); this takes 10-20 s", p.rt.Flavor, name)
	}
	began := time.Now()
	out, err := p.run(ctx, []string{"NEO4J_AUTH=neo4j/" + pw}, RunArgs(p.cfg.Image, name, labels, volume)...)
	if err != nil {
		if createdVolume {
			_, _ = p.run(context.WithoutCancel(ctx), nil, "volume", "rm", volume)
		}
		if isNameConflict(err) {
			// another depgraph process is starting the store of this root
			return p.awaitOther(ctx, o)
		}
		return neo4jprovider.Conn{}, err
	}
	ids := parseIDs(out)
	if len(ids) == 0 {
		return neo4jprovider.Conn{}, fmt.Errorf("%s run printed no container id", p.rt.Flavor)
	}
	id := ids[len(ids)-1]
	remove := func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_, _ = p.run(ctx, nil, "stop", "-t", "10", id)
		_, err := p.run(ctx, nil, "rm", "-f", id)
		if err != nil && isNoSuch(err) {
			return nil // --rm already removed it
		}
		return err
	}
	// from here on the watchdog guarantees cleanup, whatever happens to this process
	var release func(context.Context) error
	if o.Persist {
		release = then(p.stopper(id), p.arm(o, id, name, WatchStop))
	} else {
		release = then(remove, p.arm(o, id, name, WatchRemove))
	}
	inst, err := p.waitPort(ctx, Instance{ID: id, Name: name, Persist: o.Persist, State: "running"})
	if err == nil {
		inst.Password = pw
		c := p.conn(inst)
		c.Started = true
		if err = p.waitReady(ctx, o, c, id); err == nil {
			o.Logf("Neo4j ready in %.1fs (%s)", time.Since(began).Seconds(), c.URI)
			return c.WithRelease(release), nil
		}
	}
	_ = release(ctx)
	return neo4jprovider.Conn{}, err
}

// ensureVolume creates the labelled data volume of a persistent project. A
// volume left without its container cannot be reused: the Neo4j password
// lives in the container only.
func (p *Provider) ensureVolume(ctx context.Context, o neo4jprovider.StoreRequest, volume string) (created bool, err error) {
	users, err := p.list(ctx, LabelVolume+"="+volume)
	if err != nil {
		return false, err
	}
	for _, u := range users {
		return false, fmt.Errorf("volume %s is used by container %s of %s; stop it with `depgraph store down --root %s`", volume, u.Name, u.RootPath, u.RootPath)
	}
	if _, err := p.run(ctx, nil, "volume", "inspect", volume); err == nil {
		return false, fmt.Errorf("volume %s exists but its container was removed outside depgraph, so its password is unknown; delete it with `depgraph store rm` (the next scan rebuilds the graph)", volume)
	}
	_, err = p.run(ctx, nil, "volume", "create",
		"--label", LabelManaged+"=1",
		"--label", LabelProject+"="+o.Project,
		"--label", LabelRootPath+"="+o.Root,
		volume)
	return err == nil, err
}

// awaitOther waits for the store another process is starting for the root.
func (p *Provider) awaitOther(ctx context.Context, o neo4jprovider.StoreRequest) (neo4jprovider.Conn, error) {
	o.Logf("another depgraph is starting the Neo4j of this root; waiting for it")
	ctx, cancel := context.WithTimeout(ctx, p.cfg.StartTimeout)
	defer cancel()
	for {
		insts, err := p.list(ctx, LabelRoot+"="+RootHash(o.Root))
		if err != nil {
			return neo4jprovider.Conn{}, err
		}
		for _, i := range insts {
			if i.Running() && i.Port > 0 && i.Password != "" {
				c := p.conn(i)
				return c, p.waitReady(ctx, o, c, i.ID)
			}
		}
		if err := sleep(ctx, p.cfg.Poll); err != nil {
			return neo4jprovider.Conn{}, fmt.Errorf("waiting for the Neo4j container: %w", err)
		}
	}
}

// waitPort returns i with its published bolt port (inspect, then `port`).
func (p *Provider) waitPort(ctx context.Context, i Instance) (Instance, error) {
	ctx, cancel := context.WithTimeout(ctx, p.cfg.StartTimeout)
	defer cancel()
	for {
		insts, err := p.inspect(ctx, i.ID)
		if err == nil && len(insts) == 1 {
			cur := insts[0]
			if cur.Password == "" {
				cur.Password = i.Password
			}
			if cur.Port == 0 {
				if out, err := p.run(ctx, nil, "port", i.ID, boltPort); err == nil {
					cur.Port = parsePortOutput(out)
				}
			}
			if cur.Port > 0 {
				return cur, nil
			}
			if !cur.Running() && cur.State != "created" {
				return i, fmt.Errorf("Neo4j container %s is %s", i.Name, cur.State)
			}
		} else if err != nil && isNoSuch(err) {
			return i, fmt.Errorf("Neo4j container %s exited during startup", i.Name)
		}
		if err := sleep(ctx, p.cfg.Poll); err != nil {
			return i, fmt.Errorf("no bolt port published by container %s: %w", i.Name, err)
		}
	}
}

// waitReady polls o.Ready until Neo4j accepts connections, the container
// stops or StartTimeout passes.
func (p *Provider) waitReady(ctx context.Context, o neo4jprovider.StoreRequest, c neo4jprovider.Conn, id string) error {
	if o.Ready == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, p.cfg.StartTimeout)
	defer cancel()
	for n := 0; ; n++ {
		err := o.Ready(ctx, c)
		if err == nil {
			return nil
		}
		if n%8 == 7 { // every couple of seconds: did it die?
			insts, ierr := p.inspect(ctx, id)
			if (ierr != nil && isNoSuch(ierr)) || (ierr == nil && (len(insts) == 0 || !insts[0].Running())) {
				return fmt.Errorf("Neo4j %s stopped during startup (last error: %v)", c.Describe, err)
			}
		}
		if serr := sleep(ctx, p.cfg.Poll); serr != nil {
			return fmt.Errorf("Neo4j %s is not ready after %s: %v", c.Describe, p.cfg.StartTimeout, err)
		}
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// isNameConflict recognises "the container name is already in use" of
// docker ("Conflict"), podman ("already in use") and nerdctl ("already used").
func isNameConflict(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "conflict") || strings.Contains(s, "already in use") || strings.Contains(s, "already used") || strings.Contains(s, "already exists")
}

// isNoSuch recognises an object that is gone or going: "no such container /
// object / volume" of the three CLIs, and docker's "removal ... already in
// progress" (stop of a --rm container starts its removal).
func isNoSuch(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "no such") || strings.Contains(s, "not found") || strings.Contains(s, "already in progress")
}

// ---- store management (depgraph store ls / down / rm) ----------------------

// Volume is a managed data volume.
type Volume struct {
	Name     string
	Project  string
	RootPath string
}

// List returns every managed container and volume of the runtime.
func (p *Provider) List(ctx context.Context) ([]Instance, []Volume, error) {
	insts, err := p.list(ctx)
	if err != nil {
		return nil, nil, err
	}
	vols, err := p.volumes(ctx)
	return insts, vols, err
}

func (p *Provider) volumes(ctx context.Context) ([]Volume, error) {
	out, err := p.run(ctx, nil, "volume", "ls", "-q", "--filter", "label="+LabelManaged+"=1")
	if err != nil {
		return nil, err
	}
	names := parseIDs(out)
	if len(names) == 0 {
		return nil, nil
	}
	out, err = p.run(ctx, nil, append([]string{"volume", "inspect"}, names...)...)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Name   string            `json:"Name"`
		Labels map[string]string `json:"Labels"`
	}
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse volume inspect output: %w", err)
	}
	var vols []Volume
	for _, v := range raw {
		if v.Labels[LabelManaged] != "1" {
			continue
		}
		vols = append(vols, Volume{Name: v.Name, Project: v.Labels[LabelProject], RootPath: v.Labels[LabelRootPath]})
	}
	return vols, nil
}

// Down stops the store of root. An ephemeral one is removed (with its data);
// a persistent one stays, stopped, with its volume.
func (p *Provider) Down(ctx context.Context, root string) ([]Instance, error) {
	insts, err := p.list(ctx, LabelRoot+"="+RootHash(root))
	if err != nil {
		return nil, err
	}
	for _, i := range insts {
		if i.Running() {
			if _, err := p.run(ctx, nil, "stop", "-t", "10", i.ID); err != nil && !isNoSuch(err) {
				return nil, err
			}
		}
		if !i.Persist {
			if _, err := p.run(ctx, nil, "rm", "-f", i.ID); err != nil && !isNoSuch(err) {
				return nil, err
			}
		}
	}
	return insts, nil
}

// Selection picks what Remove deletes.
type Selection struct {
	All      bool
	Root     string   // containers of this root
	Projects []string // containers and volumes of these project keys
}

func (s Selection) container(i Instance) bool {
	if s.All || (s.Root != "" && i.RootHash == RootHash(s.Root)) {
		return true
	}
	for _, k := range s.Projects {
		if i.Project == k {
			return true
		}
	}
	return false
}

func (s Selection) volume(v Volume) bool {
	if s.All {
		return true
	}
	for _, k := range s.Projects {
		if v.Project == k {
			return true
		}
	}
	return false
}

// Remove deletes the selected containers and data volumes and returns their
// names. Only objects labelled depgraph.managed=1 are considered.
func (p *Provider) Remove(ctx context.Context, s Selection) (containers, volumes []string, err error) {
	insts, vols, err := p.List(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, i := range insts {
		if !s.container(i) {
			continue
		}
		if _, err := p.run(ctx, nil, "rm", "-f", i.ID); err != nil && !isNoSuch(err) {
			return containers, volumes, err
		}
		containers = append(containers, i.Name)
	}
	for _, v := range vols {
		if !s.volume(v) {
			continue
		}
		if _, err := p.run(ctx, nil, "volume", "rm", v.Name); err != nil && !isNoSuch(err) {
			return containers, volumes, err
		}
		volumes = append(volumes, v.Name)
	}
	return containers, volumes, nil
}
