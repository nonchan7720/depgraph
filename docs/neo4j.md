# Neo4j store and projects

English | [日本語](ja/neo4j.md) · [README](../README.md)

Where the graph is stored, how depgraph gets a Neo4j, and how one database holds several repositories. Why the store is Neo4j and what the queries do is in [Architecture](architecture.md).

The graph lives in Neo4j and the rules are Cypher queries. How depgraph gets a Neo4j is a
**provider**, chosen in this order:

1. `--neo4j-provider <name>`, else `$DEPGRAPH_NEO4J_PROVIDER`, else `store.provider` in `.depgraph.yml`
2. `external` when a URI is configured (`--neo4j` or `$NEO4J_URI`)
3. `container` when a container runtime answers
4. `local` (planned: a Neo4j installation on the machine run as a child process; not implemented yet)

If none is usable, depgraph stops with one line that says what each provider is missing.

## `container` (default): depgraph runs and cleans up Neo4j

Supported container CLIs: **docker** (Docker Desktop / Engine, OrbStack, Colima, Rancher
Desktop with dockerd), **podman** (including rootless; on macOS / Windows with a running
`podman machine`) and **nerdctl** (containerd: Rancher Desktop with containerd, Finch, Lima).
Bare containerd without nerdctl (`ctr`) is not supported: it cannot publish ports.
The CLI is `--container-cli` (a name or a path), else `$DEPGRAPH_CONTAINER_CLI`, else
`store.containerCli` in `.depgraph.yml`, else the first of docker, podman, nerdctl that is on
`PATH` and answers `<cli> version` within 2 s. The CLI runs with your environment, so
`DOCKER_HOST`, docker contexts and `CONTAINER_HOST` apply. The daemon must be on this machine:
the port is published on its loopback interface.

What depgraph runs (fixed argument lists, never a shell):

```
<cli> run -d --rm --name depgraph-<project>-<hash> --label depgraph.managed=1 --label depgraph.root=<hash> ...
          -p 127.0.0.1::7687 -e NEO4J_AUTH -e NEO4J_server_memory_heap_max__size=512m ...
          --tmpfs /logs --tmpfs /data docker.io/library/neo4j:5.26.32@sha256:249a1ec6...
```

- The image is the same release and digest as `docker-compose.yml` and CI.
- Bolt is published on `127.0.0.1` only, on a random port. The password is random per
  container and is handed to the CLI through its environment (`-e NEO4J_AUTH` without a
  value), never on the command line. Commands find the store of a root by its labels and read
  the port and password with `<cli> inspect`; nothing is written to disk.
- Memory: heap 128 MiB initial / 512 MiB max, page cache 64 MiB, transaction logs not
  preallocated. Measured about 330 MiB idle and 480 MiB after scanning this repository.
- Who starts it: `watch`, `scan` and `check` reuse the running store of the root or start one
  (a `scan` / `check` without `watch` starts one only for itself). `serve` and `projects` use
  a running one (and start a stopped persistent one). Whoever starts a container stops it on
  exit; a store another process started is left to that process. The Claude Code hook only
  looks for a running one: it never starts anything and prints nothing when there is none.

**The process that starts a container always stops it**, however that process ends: a normal
exit, ctrl-c / SIGTERM, a panic in any goroutine, `os.Exit`, SIGKILL or a closed terminal.
Right after `run` / `start`, depgraph starts itself as a small watchdog child
(`depgraph __container-watchdog --cli <cli> --id <container> --mode rm|stop`, fixed argv, no
shell) in its own session / process group. The child ignores SIGINT / SIGTERM / SIGHUP and
waits on a pipe from its parent. The pipe closes when the parent is gone, whatever the
reason, and the child then stops or removes exactly that container and exits. A normal exit
cleans up first, so the watchdog finds nothing to do. No reaper container and no network port
are involved. As a last resort (e.g. a power loss), the next `watch` / `scan` / `check` on
the same machine removes ephemeral leftovers whose owner process is gone (owner PID and host id
labels); only containers labelled `depgraph.managed=1` are ever touched.

**Ephemeral (default).** Data is on a tmpfs inside the container; stopping it removes it with
its data.

**Persistent (opt-in).** `--persist` (scan / check / watch / serve) or in `.depgraph.yml`:

```yaml
store:
  persist: true          # keep the data in a named volume per project key
  # provider: container  # external | container
  # containerCli: podman # docker | podman | nerdctl | a path
```

The **data** persists, not the process. It is kept in the volume
`depgraph-<project>-<hash>`. The container (no `--rm`) is stopped when the process that
started it exits, and the next `watch` / `scan` / `check` starts the same container again
(same password; about 5 s, the store is not rebuilt). `serve` and `projects` also start a
stopped persistent container. The hook works while a `watch` runs. Persisted data never
accumulates silently:

```bash
depgraph store ls                     # every container and volume depgraph manages
depgraph store down [--root DIR]      # stop the store of a root (ephemeral: removed; persistent: stopped, data kept)
depgraph store rm [--root DIR]        # delete the container and data volume of the root's project
depgraph store rm <project-key>...    # ... of these projects
depgraph store rm --all               # ... of every project
```

If the container of a persistent volume is removed outside depgraph, its password is lost;
depgraph then asks you to `depgraph store rm` the volume (the next scan rebuilds the graph).

## `external`: your own Neo4j (docker compose, a server, Aura)

Set `--neo4j bolt://host:7687` (with `--user`, `--pass`, `--db`) or `NEO4J_URI` (and
`NEO4J_USER`, `NEO4J_PASSWORD`, `NEO4J_DATABASE`). depgraph only connects; it never starts,
stops or deletes anything. This repository's `docker-compose.yml` is such a Neo4j:

```bash
docker compose up -d                  # Neo4j on bolt://localhost:7687 (neo4j / depgraph123), browser http://localhost:7474
NEO4J_URI=bolt://localhost:7687 depgraph watch --root /path/to/your/repo
```

In this repository, `mise.toml` sets `NEO4J_URI` (and the user / password of the compose
file), so `mise run watch` / `scan` / ... and the tests use the compose Neo4j (`mise run neo4j`
starts it). To use the container provider from a mise shell, run `mise run watch:container`
or pass `--neo4j-provider container`.

## Projects

One Neo4j database holds many repositories. Every scan belongs to a **project**, and all
queries, `--reset` and the UI are scoped to it. The project key is, in order:

1. `--project <key>`
2. `project: <key>` in `<root>/.depgraph.yml`
3. the directory name of `--root`

so the same repository gets the same key wherever and on whichever machine it is checked out.
In a linked git worktree the key becomes `<base>@<worktree name>` (the base falls back to the
main worktree's directory name), so worktrees never overwrite each other.

A `(:Project)` node records the last root and the normalized `remote.origin.url`. When a key
is reused by another root, it is treated as **the same repository moved** if the worktree name
matches and either the origin matches or the previous root no longer exists; the root is then
updated. Otherwise depgraph stops with an error asking you to pick another key with
`--project` or `.depgraph.yml`.

```bash
depgraph projects list               # key, base, worktree, root, last scan, files
depgraph projects rm <key>           # delete a project and all of its data
depgraph projects prune [--dry-run]  # delete projects whose root no longer exists
```
