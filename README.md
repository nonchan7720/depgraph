# depgraph

English | [日本語](README_ja.md)

A tool that loads the dependency graph of AI-written code into Neo4j, visualizes it, checks architectural rules and writes the results back into `CLAUDE.md`.
During vibe coding it updates the graph every time a file is rewritten and immediately detects "a new import cycle", "a layer violation" or "a duplicated function", feeding that back into the next prompt.

Supported languages (tree-sitter): Go / TypeScript / JavaScript / Python / Ruby (Rails, engines, packwerk packs)

## Install

**GitHub Releases (recommended; the only distributed binaries).** Download the archive for your OS / CPU from
[Releases](https://github.com/nonchan7720/depgraph/releases): `depgraph_<os>_<arch>.tar.gz`
(`.zip` on Windows) for linux / darwin / windows × amd64 / arm64. Verify it with `checksums.txt`,
then put `depgraph` on your `PATH`. Every archive also contains `LICENSE`, `NOTICE` and
`THIRD_PARTY_NOTICES.md`.

```bash
sha256sum --ignore-missing -c checksums.txt   # macOS: shasum -a 256 --ignore-missing -c checksums.txt
tar xzf depgraph_linux_amd64.tar.gz && install depgraph ~/.local/bin/
depgraph --version
```

Releases named `v<version>-dev.<commit>` are prereleases built from every push to `main`.

**From source.** Clone the repository and build with the toolchain pinned in `mise.toml`
(Go, node and pnpm; the browser UI in `web/` is built and embedded into the binary):

```bash
git clone https://github.com/nonchan7720/depgraph.git && cd depgraph
mise install && mise run build   # writes bin/depgraph
```

Parsing uses [gotreesitter](https://github.com/odvcencio/gotreesitter), a pure-Go tree-sitter runtime, so depgraph needs no C toolchain: it builds with `CGO_ENABLED=0` and cross-compiles to any `GOOS`/`GOARCH` with plain `go build`.
Builds use the tags in `GOFLAGS` of `mise.toml` (`gotreesitter_no_copyleft` and `grammar_subset_<language>`), which keep the GPL/MPL grammars that gotreesitter also vendors out of the binary; `mise run check:no-copyleft` verifies it. Release builds (goreleaser) take the same `GOFLAGS` and run the same check on every binary.

## Quick start

Requirements: depgraph (see Install) and a container runtime: Docker, Podman or nerdctl
(see [Neo4j](#neo4j)). No compose file, no Neo4j installation.

```bash
depgraph watch --root /path/to/your/repo
```

depgraph starts Neo4j in a container (10-20 s the first time per run; the image, about 1 GB,
is pulled once), scans the repository, then watches for file changes and re-analyzes only
what changed. A browser UI starts at http://127.0.0.1:7777 and the graph and findings update
live (SSE). Findings are printed to stderr, and the `<!-- depgraph:start -->` ...
`<!-- depgraph:end -->` section of `/path/to/your/repo/CLAUDE.md` is updated (hand-written
content is kept). When `watch` exits, for whatever reason (even a crash), the container is
removed with its data; add `--persist` to keep the data (see [Neo4j](#neo4j)).

To try it on a generated sample repository, run `mise run demo`. It watches the sample as
the project `depgraph-demo` in the docker compose Neo4j below (UI http://127.0.0.1:7778); its
`--reset` clears only that project, so other projects are never touched. Remove it with
`bin/depgraph projects rm depgraph-demo`.

## Neo4j

The graph lives in Neo4j and the rules are Cypher queries. How depgraph gets a Neo4j is a
**provider**, chosen in this order:

1. `--neo4j-provider <name>`, else `$DEPGRAPH_NEO4J_PROVIDER`, else `store.provider` in `.depgraph.yml`
2. `external` when a URI is configured (`--neo4j` or `$NEO4J_URI`)
3. `container` when a container runtime answers
4. `local` (planned: a Neo4j installation on the machine run as a child process; not implemented yet)

If none is usable, depgraph stops with one line that says what each provider is missing.

### `container` (default): depgraph runs and cleans up Neo4j

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

### `external`: your own Neo4j (docker compose, a server, Aura)

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

## Commands

| command | description |
|---|---|
| `scan --root DIR [--reset] [--fail-on warn]` | full scan → evaluate rules → update CLAUDE.md (with `--fail-on`, exit 1 on findings; see [CI](#ci)) |
| `watch --root DIR [--no-lsp] [--no-ui]` | scan, then watch with fsnotify and update incrementally (tree-sitter, then LSP adds CALLS/REFERENCES) |
| `check --root DIR [--fail-on warn]` | evaluate rules against the graph already in Neo4j (updates CLAUDE.md) |
| `serve --root DIR [--addr 127.0.0.1:7777]` | browser UI over the graph already in Neo4j (no watching, CLAUDE.md untouched) |
| `mcp --root DIR` | stdio MCP server that shows the browser UI as an [MCP App](#mcp-app) (reads the graph already in Neo4j, CLAUDE.md untouched) |
| `init --root DIR [--dry-run]` | add a `languages` setting to `.depgraph.yml` for every directory with Python files whose files pin its Python version (asks on a terminal where they disagree; see [Frameworks](#frameworks-external-packages-in-layers)) |
| `parse --root DIR [files...]` | print the tree-sitter result as JSON, one file per line, without Neo4j (debugging) |
| `hook <session-start\|prompt>` | Claude Code hook: print findings as additional context (read-only, always exits 0) |
| `gate [files...] [--fail-on warn]` | quality-gate check ([claude-hook-gate](#quality-gate-claude-hook-gate)): apply the changed files, exit 1 on findings the session added |
| `projects list` / `projects rm <key>` / `projects prune [--dry-run]` | list / delete the projects in the database |
| `store ls` / `store down` / `store rm [<key>...] [--all]` | list / stop / delete the Neo4j containers and data volumes depgraph manages (container provider) |
| `schema` | print the JSON Schema of `.depgraph.yml` (see [Configuration](#configuration-depgraphyml)) |
| `version` / `--version` | print the version (`version` adds the commit, build date and Go toolchain) |

Flags (see `depgraph <command> --help`):

| flag | commands | default |
|---|---|---|
| `--root` | all | `.` |
| `--project` | all | `project` in `<root>/.depgraph.yml`, else the directory name of `--root` (`@<worktree>` added in a linked git worktree) |
| `--exclude` | all | none (comma-separated exclude patterns) |
| `--ignore-file` | all | `.depgraphignore` (relative to `--root`, empty to disable) |
| `--neo4j` / `--user` / `--pass` / `--db` | scan / check / watch / serve / mcp / hook / gate / projects | external Neo4j: `$NEO4J_URI` / `$NEO4J_USER` / `$NEO4J_PASSWORD` / `$NEO4J_DATABASE` (no URI: depgraph provides Neo4j; fallback user / password / database `neo4j` / `depgraph123` / `neo4j`) |
| `--neo4j-provider` | scan / check / watch / serve / mcp / hook / gate / projects | `$DEPGRAPH_NEO4J_PROVIDER`, else `store.provider`, else external when a URI is set, else container |
| `--container-cli` | scan / check / watch / serve / mcp / hook / gate / projects / store | `$DEPGRAPH_CONTAINER_CLI`, else `store.containerCli`, else the first of docker / podman / nerdctl that answers |
| `--persist` | scan / check / watch / serve / mcp | `store.persist`, else false (ephemeral) |
| `--claude-md` | scan / check / watch | `$DEPGRAPH_CLAUDE_MD` or `CLAUDE.md` (relative to `--root`, empty to disable) |
| `--reset` | scan / watch | false (clear this project's data before scanning; other projects are kept) |
| `--workers` | parse / scan / watch | `0` (files parsed in parallel during a full scan; 0 = GOMAXPROCS. Output order does not depend on it) |
| `--addr` | watch / serve | `$DEPGRAPH_ADDR` or `127.0.0.1:7777` |
| `--debounce` | watch | `400ms` |
| `--no-ui` / `--no-lsp` | watch | false |
| `--timeout` / `--debug` / `--format` | hook | `3s` / false / `json` (`text`: the context itself, for claude-hook-gate's `session_start`) |
| `--fail-on` | scan / check | none (exit 1 when there are findings of at least `error` / `warn` / `info`) |
| `--fail-on` / `--timeout` | gate | `warn` / `60s` |

### CI

`scan` and `check` exit 0 whatever they find, unless `--fail-on <error|warn|info>` is given: they
then exit 1 when the evaluation has findings of at least that severity (all of them, not only new
ones; the gate is for what a session added). For a CI job, with the container provider on a
runner that has Docker:

```bash
depgraph scan --root . --claude-md "" --fail-on warn   # --claude-md "": do not rewrite CLAUDE.md
```

`--fail-on error` fails only on import cycles and layer violations.

## Claude Code plugin

The repository is also a Claude Code plugin marketplace. The `depgraph` plugin wires the
[hooks](#claude-code-hooks) for you and adds two skills:

```text
/plugin marketplace add nonchan7720/depgraph
/plugin install depgraph@depgraph
```

| part | what it does |
|---|---|
| hooks | `depgraph hook session-start` / `prompt` at session start and on every prompt (as below). They do nothing without a depgraph binary (see below), when the project's `.claude/settings.json` runs `depgraph hook` itself, and (session start only) when claude-hook-gate's `session_start` runs it, so the context is never added twice |
| `/depgraph:setup [init\|update\|review]` | writes `.depgraph.yml` (layers from what the code shows, entry points, rule options), `.depgraphignore` and, with claude-hook-gate, the `depgraph gate` rule and `session_start` entry in `.claude/gate.yaml` |
| `/depgraph:fix [rule]` | fixes findings the way the code needs (move the import that points the wrong way, reuse instead of re-implement, wire up or remove unused code), not by silencing rules |

The plugin installs the binary of its own version (the release it was made with), like
claude-hook-gate does: the session-start hook downloads `depgraph_<os>_<arch>.tar.gz` and
`checksums.txt` from the GitHub release `v<version>`, checks the sha256 and extracts `depgraph` into
`${XDG_CACHE_HOME:-~/.cache}/depgraph/<version>/` (once per version; versions unused for 30 days are
removed). The prompt hook only uses a binary that is already there. The skills run it through
`plugin/scripts/depgraph`, a wrapper that installs it the same way. The binary is, in order:

1. `$DEPGRAPH_BIN` (for development, e.g. `bin/depgraph` of a checkout)
2. the cached binary (`$DEPGRAPH_CACHE_DIR` replaces the cache directory)
3. a download from the release (linux / darwin × amd64 / arm64; needs `curl` or `wget`, and
   `sha256sum` or `shasum`: nothing is installed without a verified checksum)
4. `depgraph` on `PATH` (offline, Windows)

When none works, the hooks do nothing and print one line on stderr with the reason. Keep
`depgraph watch --root <repo>` running in a terminal. It needs `depgraph` on `PATH` (see
[Install](#install)), and so do the claude-hook-gate entries below.

## Claude Code hooks

`depgraph hook <session-start|prompt>` feeds the findings already in Neo4j back into
Claude Code (read-only: it never scans and never writes CLAUDE.md).

- `session-start`: a compact summary — stats, then findings by severity (errors first);
  rules with many findings show the count and a few examples, the whole output is capped
  (about 4,000 characters) so even 50 `duplicate-symbol` findings do not flood the context.
  It also records the session's baseline (the findings it started with; kept on resume and compact)
  for [`depgraph gate`](#quality-gate-claude-hook-gate)
- `prompt`: only the findings that appeared or were resolved since the last injection in the
  same session; nothing when nothing changed. The state is kept per `session_id` in
  Neo4j next to the project's graph (`HookSession` nodes): the 20 newest sessions of a project, none
  older than 7 days, removed with the project (`projects rm`, `--reset`). Nothing is written into the
  repository (the state file of earlier versions, `.claude/.depgraph-hook-state.json`, is deleted)
- Root: `--root`, else `$CLAUDE_PROJECT_DIR`, else `cwd` of the hook input
- The project is resolved like `scan` does (`--project`, `.depgraph.yml`, directory name,
  `@<worktree>`); only that project's findings are injected
- It never blocks a prompt: when Neo4j is unreachable, the root's project has not been scanned
  (or its key belongs to another live root), or `--timeout` (default `3s`) expires, it prints nothing and exits 0
  (`--debug` tells why on stderr). Output is `hookSpecificOutput.additionalContext` JSON

To use it in another repository, keep `depgraph watch --root <repo>` running and add this to
that repository's `.claude/settings.json` (this repository uses `bin/depgraph` the same way):

```json
{
  "hooks": {
    "SessionStart": [
      { "hooks": [ { "type": "command", "timeout": 10,
        "command": "command -v depgraph >/dev/null && depgraph hook session-start; exit 0" } ] }
    ],
    "UserPromptSubmit": [
      { "hooks": [ { "type": "command", "timeout": 10,
        "command": "command -v depgraph >/dev/null && depgraph hook prompt; exit 0" } ] }
    ]
  }
}
```

The hook uses the same Neo4j as the other commands (see [Neo4j](#neo4j)): an external one
when `--neo4j` / `$NEO4J_URI` is set, else the running depgraph container of the root, found by
its labels. It never starts a container; with none running it prints nothing.

### Quality gate (claude-hook-gate)

`prompt` only runs when the user sends a prompt: during one long agent turn that rewrites many
files, nothing reaches the agent. `depgraph gate` closes that gap as a check of
[claude-hook-gate](https://github.com/nonchan7720/claude-hook-gate) (`.claude/gate.yaml`),
which runs commands after each edit and before the agent stops:

```yaml
rules:
  # depgraph: debt this session added (needs `depgraph watch`; passes without it)
  - match: ["**/*.go", "**/*.{ts,tsx,js,jsx,mjs,cjs}", "**/*.py"]
    timeout: 60
    run:
      - { name: depgraph, cmd: "command -v depgraph >/dev/null || exit 0; depgraph gate" }
```

- It applies the changed files (`$CLAUDE_GATE_FILES`, or the arguments) to the graph itself, as
  `watch` does, so it does not race `watch`'s debounce, then evaluates the rules and lists the findings
  of at least `--fail-on` (default `warn`) that the session (`$CLAUDE_SESSION_ID`) **added**
  since its baseline. With any it exits 1: the gate hands the list to the agent, and because a failing
  rule is rerun when the agent stops, **the agent cannot finish while the debt it added remains**
  (claude-hook-gate gives up after 5 blocked stops). Debt that was there when the session started never fails it.
- The baseline is recorded by `depgraph hook session-start`. Without one, the findings before the first
  gate run's changes are the baseline. With a claude-hook-gate that has the `session_start` phase, the
  whole integration lives in `gate.yaml` (instead of the `SessionStart` hook in `settings.json`):

  ```yaml
  session_start:
    - { name: depgraph, timeout: 10, cmd: "command -v depgraph >/dev/null || exit 0; depgraph hook session-start --format text" }
  ```

  `--format text` prints the summary itself (the gate injects stdout as is); the session comes from
  `$CLAUDE_SESSION_ID` when the hook input has none.
- `--fail-on error` fails only on new import cycles and layer violations; the other findings then
  still reach the agent through the `prompt` hook.
- It passes (exit 0, the reason on stderr) whenever it cannot tell: no running store (it never starts
  Neo4j), the root's project not scanned, no session, or `--timeout` (default `60s`) expired.
  It never writes CLAUDE.md. The root is `--root`, else the current directory's git worktree when it is another worktree of
  `$CLAUDE_PROJECT_DIR`'s repository (see below), else `$CLAUDE_PROJECT_DIR`, else the current directory.
- **Agents in git worktrees.** claude-hook-gate runs the rules of a file under `.claude/worktrees/<name>/`
  in that worktree, with `$CLAUDE_PROJECT_DIR` still the main tree and `$CLAUDE_GATE_FILES` relative to the
  worktree. The gate then checks the worktree, not the main tree, as its own project (`<base>@<name>`, see
  [Projects](#projects)) in the Neo4j of the main tree's `watch`, so one `depgraph watch` of the main tree is
  enough. Findings of the worktree reach only the agent working there; the main tree's project is not touched.
  - The worktree's graph is built from its base, the newest commit on its history it did not make itself (by
    its `HEAD` reflog; the exact committed tree, plus ignored files and submodules present in the checkout),
    and that is the session's baseline: what the agent changed or committed before the first run still counts
    as added, debt that was there before does not.
  - Every run then applies what git says may differ from the graph, besides `$CLAUDE_GATE_FILES`: files changed
    between the recorded commit and `HEAD` (commits, `git reset` / `rebase`) and uncommitted or untracked files
    whose content changed since they were applied. A file changed only by commits the worktree did not make
    (another agent's work merged or rebased in from upstream) is upstream: the debt it brings is added to the
    baselines of the worktree's sessions, as it is not this agent's. Everything else, including what the agent
    edited and committed in one step, counts as the agent's. When the worktree's base moves (a rebase or
    `pull --rebase` onto upstream), the findings of the new base are added to the baselines too, so debt that
    came with main is never the agent's, even in a file its replayed commit touched. A merge commit, conflict
    resolution included, is upstream (a conflicted file holds the merged branch's changes too; debt added only in a resolution is not
    reported). While a merge, cherry-pick, revert or
    rebase is in progress, the gate passes and says so: the incoming changes are staged as if the agent made
    them; it checks once the operation is committed.
  - The first build, absorbing upstream changes, and a delta that touches a file import resolution depends on
    (`go.mod`, `tsconfig.json`, `package.json`, ...) or more than 200 files, run in a background `depgraph` process under a lease on the
    project, so a large repository is never stuck behind the gate's `--timeout`. The gate waits for it as long
    as its timeout allows and otherwise passes, saying so on stderr; the next run checks.
  - Projects of removed worktrees of the same main tree are deleted after a build (`depgraph projects prune`
    deletes them too).

## Browser UI

- Packages are nodes, imports are arrows, laid out left to right by ELK (layered, orthogonal edges) in a Web Worker. Nodes and edges involved in cycles or layer violations are red, god packages orange
- Directories are folded into one node: click to drill down (breadcrumbs / Esc to go back), Shift+click to open it in place as a frame
- Nodes and frames can be dragged (whatever they land on is pushed aside); moved positions are remembered per view until `layout` is pressed. A minimap in the corner shows the whole graph
- Clicking a finding on the right highlights only the related nodes
- Clicking a node shows its files, symbols and importers / imports
- The `external` checkbox toggles external dependencies (`ext:*`); the search box filters
- During `watch` the graph redraws on every save. The layout is kept as long as the node set does not change
- Built from `web/` (Vite + TypeScript, Cytoscape + elkjs) into `pkg/interfaces/handler/ui/dist/` and embedded in the binary (`mise run build` runs `web:build` first). No CDN or web fonts: every dependency is pinned in `web/pnpm-lock.yaml`, and `mise run web:check-no-cdn` verifies the build loads nothing from a URL

API: `GET /api/graph` (packages, edges, findings, stats), `GET /api/package?key=...`, `GET /events` (SSE)

## MCP App

`depgraph mcp` is an MCP server (stdio) that shows the same browser UI inside the conversation of a host
that supports [MCP Apps](https://modelcontextprotocol.io/extensions/apps) (Claude, Claude Desktop, ...). Like
`serve` it reads the graph already in Neo4j: run `scan` or `watch` for the repository first.

```json
{
  "mcpServers": {
    "depgraph": { "command": "depgraph", "args": ["mcp", "--root", "/path/to/repo"] }
  }
}
```

(Claude Code: `claude mcp add depgraph -- depgraph mcp --root /path/to/repo`)

| tool | visible to | what it does |
|---|---|---|
| `show_dependency_graph` | model, app | re-runs the rules, answers a summary of the findings and opens the graph view (`ui://depgraph/graph.html`) |
| `graph_view` | app | the folded graph, same JSON as `GET /api/graph` |
| `package_detail` | app | one package, same JSON as `GET /api/package` |

- The view runs in the host's sandbox with no network access, so it is built as one self-contained HTML
  (`vite build --mode mcp-app` into `pkg/interfaces/handler/mcpapp/dist/`, embedded like the browser UI) and
  reads the graph through the app-only tools, which the host proxies to the server. ELK runs in the page
  instead of a Web Worker (the sandbox allows no worker script)
- There is no event stream: `refresh` in the header re-runs the rules and reloads, and so does the model
  calling `show_dependency_graph` again. `fullscreen` appears when the host offers it
- Logs go to stderr; stdout carries the protocol

## Graph schema

```
(Project {key, name, root, origin, base, worktree, lastScanAt})
(Package {project, key, name, external})
(File    {project, path, language, lines, updatedAt,
          scope, entry, internal, generated, uses, exports, keep})   # usage, for the unused-code rules
(Symbol  {project, name, kind, exported, line, endLine, language, file})

(Package)-[:CONTAINS]->(File)-[:DECLARES]->(Symbol)
(File)-[:IMPORTS {spec}]->(Package)
(Package)-[:IMPORTS]->(Package)        # recomputed from file-level imports
(Symbol)-[:CALLS|REFERENCES]->(Symbol) # added by LSP for changed files during watch
```

`project` is the project key; Package is unique by `(project, key)` and File by
`(project, path)`. `Package.key` is the directory path from the repository root (the root is `.`).
Imports outside the repository become a Package with key `ext:<package>` and `external=true`
(also per project; `elkjs/lib/elk-api` is the package `ext:elkjs`). An import that resolves to
nothing gets no Package and no edge (the project's compiler / type checker reports it).

The usage properties of a File are what the file itself says: the names it uses from which file or
package (`uses`), the names it exports under another name (`exports`), the names it keeps on purpose
(`keep`: a Python `__all__` entry, a decorated definition, an STI subclass), whether it is generated or vendored, and
why it is an entry point (`entry`). They are replaced with the file, so a `watch` update keeps them
correct; the unused-code rules join them across the project when they run.

## Import resolution

Imports are resolved the way each ecosystem resolves them, from the project's own configuration:

| language | how |
|---|---|
| TypeScript / JavaScript | esbuild's resolver: `tsconfig.json` / `jsconfig.json` (`paths`, `baseUrl`, `extends`), `package.json` (`exports`, `imports`, workspaces), `node_modules`, ESM `./x.js` written for `x.ts`. Workspace packages (`workspaces` of `package.json`, `pnpm-workspace.yaml`) resolve to their directory even before an install (a fresh clone, CI, the gate's worktrees); an installed package of the same name wins |
| Go | `go.mod` / `go.work` (nested modules, `use`, `replace`) |
| Python | the import system over the repository's package roots (the repo root, `src/`, `pyproject.toml` / `setup.cfg` package dirs); a directory without `__init__.py` is a namespace package and does not shadow the standard library |
| Ruby | `require_relative` relative to the file; `require` over the load path the repository implies (`lib/`, `spec/`, `test/` of the file's project, `lib/` of every gem of the repository). Rails code names other files by constant, not by `require`, so constants are dependencies too, resolved the way Zeitwerk loads them: every directory under `app/` (but `assets`, `javascript`, `views`) and its `concerns/`, and `lib/`, of the application, its engines and its packwerk packs (a directory with `app/` and a `Gemfile`, `*.gemspec` or `package.yml`, or whose `app/` has Rails' layout such as a Discourse plugin); `Admin::UsersController` is `admin/users_controller.rb` under one of them, looked up in the lexical nesting it is written in (`Admin::UsersController::User`, `Admin::User`, `User`). A constant Ruby or a gem defines is no dependency |

A relative path, an alias or a module of the project that points at nothing is unresolved, not
external, so it never shows up as a dependency. `watch` rescans when one of these configuration files changes.

## Rules

| rule | severity | description |
|---|---|---|
| `import-cycle` | error | import cycle between packages: for every import on a cycle of at most 8 packages, the shortest cycle through it (up to 50) |
| `module-cycle` | error | import cycle between the modules of a monorepo (Go modules, npm workspace packages, Python projects, Ruby applications / gems / engines / packwerk packs: directories with `go.mod`, `package.json`, `pyproject.toml`, `setup.py`, `setup.cfg`, `Gemfile`, `*.gemspec` or `package.yml`, plus [components](#monorepos-components)) through directories that do not form a cycle themselves (`packages/web/src/a -> packages/kit/src -> packages/web/src/b`). One import is listed per hop; a cycle `import-cycle` already reports is not repeated |
| `layer-violation` | error | a package imports a layer it must not import (`handler → repository`, ... or your own layers). Layers may name external packages, so a domain that imports a framework is one too (see [Frameworks](#frameworks-external-packages-in-layers)) |
| `concrete-dependency` | warn | a struct / class holds (field, constructor parameter) or builds an implementation of a layer it may use only through interfaces (`abstract:`), although that implementation satisfies an interface declared in the holder's package, its own package or the inner layer (see [Inside the layers](#inside-the-layers-dependency-inversion-and-leaking-types)) |
| `type-leak` | warn | the API of a layer (parameters and results of exported functions / methods, exported fields) exposes a struct or class of a layer it may use only through interfaces (`abstract:`) |
| `duplicate-code` | warn | the same logic in several places, whatever the names: identical, or a copy changed a little (see below) |
| `god-package` | warn | a hub: imported by 10 or more packages and importing 3 or more in-repo packages. A package many depend on that depends on nothing in the repository (a domain model) is stable, not a god package (Stable Dependencies Principle) |
| `duplicate-symbol` | warn | an exported function/class with the same name exists in several files (the files of one Go package count once: they can only share a name through build constraints; Ruby is left out, reopening a class is how Ruby extends it) |
| `unused-export` | warn | an exported declaration nothing in the project uses, not even its own file (Go: exported functions and types of non-main packages, not methods; TS/JS: `export`ed declarations, followed through re-exports and barrels; Python: public module-level functions and classes; Ruby: classes, modules with something in them and `Struct.new` / `Class.new` constants, never methods) |
| `unreachable-file` | warn | a TS/JS/Python/Ruby file no entry point reaches through imports; one finding per file nothing imports, listing the files only it leads to |
| `unreachable-package` | warn | a Go package no `main` package, test or public package of a library module imports |

### Unused code

`unused-export` and `unreachable-*` report what only the whole project shows: the compiler,
`go vet` / staticcheck, `tsc --noUnusedLocals`, eslint and ruff see an unused local or unexported
name, never an export nothing imports. A finding that is not true is a bug, so a file is an entry
point (reachable, its exports used from outside, never reported) whenever there is a sign that
something outside the import graph loads it:

- Go: `main` packages, `_test.go` files; a module without a `main` package is a library, and its
  packages outside `internal/` are its public API. Exported methods are never reported (an interface
  may need them), nor is a package that only holds documentation. Files a `//go:embed` embeds are used.
- TS/JS: `package.json` `main` / `module` / `browser` / `types` / `bin` / `exports` and the files its
  `scripts` run (`dist/` paths are mapped back to their sources with `tsconfig.json`), `<script src>`
  of HTML pages and the scripts page templates name, config files (`*.config.*`, `.*rc.*`), tests,
  stories, type tests, `testdata/`, `.d.ts`, `scripts/` `bin/` `tools/` `docs/` `public/` `static/`,
  `examples/` and benchmarks, `index` / `main` / `server` / `app` / `cli` at the package root or
  `src/`, file-system routes (`app/`, `pages/`, `routes/` of Next.js, Remix, Gatsby, Expo Router,
  ...), Docusaurus theme components, React Email templates, files MDX files import. An import of a
  package the project does not declare (a bundler or site-generator alias such as `@site/...`) is
  matched against the project's paths instead of being dropped. A package that uses Vue, Svelte,
  Astro, Qwik, Marko, Ember or auto-imports is skipped: its component files are not parsed.
  shadcn/ui's copied component kit is vendored code and not reported.
  In a monorepo, a `private` workspace package another package of the repository depends on is never
  published, so only the repository uses it: its `main` / `exports` / `types` and `index` files stay
  entry points for reachability, but their exports are reported when no other package uses them
  (`bin` and `scripts` files are still run from outside). Published packages and private apps nothing
  depends on keep their entries' exports.
- Python: `__main__.py`, a `__main__` guard, `conftest.py`, `test_*.py`, `tests/`, migrations,
  `setup.py` / `manage.py` / `wsgi.py` / `asgi.py`, the targets of `pyproject.toml` / `setup.cfg`
  scripts and entry points, and in a Django project its autodiscovered modules (`models`, `admin`,
  `apps`, `urls`, `views`, `wagtail_hooks`, ...). `__init__.py` is never unreachable. Names in
  `__all__` and decorated definitions (routes, fixtures, tasks) are kept.
- Ruby: a constant is used where it is named (`User`, `Admin::Role`, a superclass, `include` /
  `extend` of a concern, `"Admin::User"` / `class_name:` strings and symbols, `%w[CsvImporter]`,
  `delegated_type types:`, `"Importers::#{kind}".constantize` uses everything under `Importers`), by
  associations (`has_many :comments` uses `Comment`, singularized with ActiveSupport's rules; a name
  derived from a symbol also matches an acronym class such as `APIKey`), by
  `validates :email, email: true` (`EmailValidator`) and by the constants the code and attributes of
  ERB / Haml / Slim views name (`.jbuilder`, `.builder` and `.ruby` files under `views/` are Ruby), by
  the constants `config/` YAML (`validator: "ColorListValidator"`) and extensionless Ruby scripts
  (`#!/usr/bin/env ruby`) name, `"#{module_parent_name}::#{kind}".constantize` (every constant of the
  enclosing namespace) and `has_one :"#{name}_search_data"` (every constant ending with `SearchData`);
  a glob of Ruby files (`Dir["lib/patches/*.rb"]`) uses every file under its directory; `require` makes a file reachable, and
  Zeitwerk loads the file of a namespace (`admin.rb`, even `module Vacuum; end`) for its constants.
  What Rails loads by convention is an entry point: `app/controllers`, `jobs`, `mailers`, `channels`,
  `helpers`, `mailboxes`, `views` (also `.jbuilder`, `.rss.ruby`), `workers`, and the classes tools
  infer from a model's name (`policies`, `serializers`, `decorators`, `dashboards`, `inputs`, `admin`);
  `config/`, `db/`, `bin/`, `script/`, `lib/tasks`, `lib/generators`, `Rakefile`, `config.ru`, `*.rake`,
  `sorbet/`, dot-directories (`.skills/`), `bundler/inline` scripts, Discourse plugins (`plugin.rb`, and
  their `assets/javascripts` for the Ember resolver), the application's abstract `ApplicationRecord`, tests (`spec/`, `test/`, `*_spec.rb`, `*_test.rb`), scripts (`#!`, `if __FILE__ == $0`),
  dotfiles (`.active_record_doctor.rb`) and the files RuboCop / Standard / haml-lint configurations
  `require`. Concerns are not entry points, wherever they live: one nothing includes is reported. A
  gem's `lib/` is its public API. A packwerk pack's public path (`public_path` of `package.yml`,
  `app/public/` by default) is reachable, but packs are used by the application only, so a class of
  it nothing uses is still reported. A class or module that reopens a library's namespace (`Paperclip`,
  `Chewy`, `ActiveRecord`, `String`, ... : Ruby, Rails and the gems of `Gemfile` / `Gemfile.lock`) is
  loaded by that library by name and never reported, nor are STI subclasses in `models/` (loaded from
  the `type` column), subclasses of `Rails::Railtie` / `Rails::Engine` / generators, classes that
  register or prepend `self`, a concern's `ClassMethods`, and modules that only hold other classes (namespaces).
  The JavaScript of a Rails application starts where its asset tool says: `app/assets/` (Sprockets),
  the files directly in `app/javascript/` (jsbundling, importmap), `config/importmap.rb` pins and
  `pin_all_from`, Vite Ruby (`config/vite.json`, else `app/javascript/entrypoints`,
  `app/frontend/entrypoints`) and Shakapacker / Webpacker (`source_path` / `source_entry_path`, else
  `app/javascript/packs`).
- Everywhere: files and modules that Dockerfiles, Procfiles, Makefiles, compose files, CI workflows,
  `mise.toml` and shell scripts run (`node dist/server.js`, `python -m pkg.tool`,
  `uvicorn app.main:app`), and paths or dotted module names in strings (`new URL('./worker.ts', ...)`,
  `"app.tasks.send"`, `importlib.import_module(f"app.plugins.{name}")`, `` import(`./locales/${l}`) ``),
  and source file names in strings of any language (`"render.js"` in a Go program that bundles it).
- A file whose header says it is generated is not reported.

When a file is loaded in a way depgraph cannot see (a plugin loaded by name, a file run by an
external tool), put a `depgraph:entry` comment in it (`// depgraph:entry`, `# depgraph:entry`). A
library's public API that its own repository does not use is left out with `ignore`, and entry points
are added with `entries`, in `.depgraph.yml` (path globs over files and Go package directories, shared
by the three rules; see below). An export only its own file uses is not reported (the
export is unneeded, but that is style: Go handlers registered in the same file, for example).

Layers, forbidden imports, severities and thresholds are set in `.depgraph.yml` (next section).

### Configuration (`.depgraph.yml`)

Put `.depgraph.yml` in the scanned root to declare the architecture. Every key is optional;
`scan`, `check`, `watch`, `serve` and `hook` read it, and `watch` reloads it when it changes
(a change of `project` needs a restart).

Editors complete and check the file with its JSON Schema,
[`schema/depgraph.schema.json`](schema/depgraph.schema.json) (also printed by `depgraph schema`).
With yaml-language-server (VS Code's YAML extension, Neovim, Zed, ...), put this line at the top;
use a release tag instead of `main` to match the depgraph you run:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/nonchan7720/depgraph/main/schema/depgraph.schema.json
```

The schema is generated from the rule registry and the decoder (`mise run schema`; a test fails when it is
stale), so it accepts exactly what depgraph accepts.

```yaml
project: my-repo            # project key (see Projects)

layers:                     # layer name -> package path globs
  domain: ["pkg/domain/**"]
  services: ["pkg/services/**"]
  infrastructure: ["pkg/infrastructure/**"]
  interfaces: ["pkg/interfaces/**", "cmd/**"]
  frameworks: ["ext:*", "!ext:github.com/google/uuid"]   # third-party packages (see below)

forbidden:                  # layer -> layers it must not import
  domain: [infrastructure, services, interfaces, frameworks]
  services: [interfaces]
  infrastructure: [interfaces, services]

abstract:                   # layer -> layers it may use only through interfaces
  services: [infrastructure]

rules:                      # per rule: enabled, severity (error | warn | info), options
  import-cycle: {severity: error}
  layer-violation: {severity: error}
  god-package: {severity: warn, fanIn: 10, fanOut: 3}
  duplicate-symbol: {enabled: false}
  duplicate-code: {minTokens: 50, similarity: 0.8}   # body size and fingerprint similarity
  # duplicate-symbol: {ignore: [main, New, "Test*", "*Handler"]}  # names not checked (replaces the defaults)
  unused-export: {ignore: ["pkg/api/**"]}            # paths not reported (a library's public API)
  unreachable-file: {entries: ["plugins/**"]}       # entry points depgraph cannot see
  # entries / ignore are shared by unused-export, unreachable-file and unreachable-package

ignore: ["**/testdata/**"]  # packages left out of every rule

store:                      # how Neo4j is provided (see Neo4j); flags and env take precedence
  persist: false
  provider: container
  containerCli: docker
```

- **Package globs** match the package key (the directory from the root, `.` for the root) by
  whole path segments: `**` is any number of segments, `*` and `?` stay within one segment.
  `pkg/domain/**` matches `pkg/domain` and everything below it; `**/db/**` matches `db` and
  `internal/db/sql` but not `pkg/feedback`.
- **Name patterns** (`duplicate-symbol`'s `ignore`) match the whole symbol name: `*` is any run
  of characters, `?` one character, everything else is literal. `New` is that name only,
  `Test*` every name starting with `Test` (a prefix), `*Handler` every name ending in `Handler`.
- A package may be in several layers. An import is a violation only when it crosses the pair:
  it is not reported when both packages are in the forbidden layer, or both in the importing one.
- `layers` replaces the default layers, and with them the default forbidden and abstract pairs;
  `forbidden` or `abstract` alone refers to the default layers.
- `ignore` excludes packages from the findings (cycles through them, fan-in from them, ...);
  unlike `.depgraphignore` they are still scanned and shown in the UI.
- Decoding is strict: an unknown key, rule, option, severity or a `forbidden` entry naming an
  undefined layer is an error that names the file, instead of a setting silently ignored.

Without `.depgraph.yml` the defaults are the layers `controller`, `db`, `domain`, `handler`,
`infra`, `repository` and `usecase`, each matching a path segment of that name (plus
`controllers`, `handlers`, `infrastructure`, `repositories`, `usecases`), with the forbidden
pairs `handler → repository / infra / db`, `controller → repository / db`,
`domain → infra / handler / repository` and `usecase → handler`, and the abstract pairs
`usecase → infra / db`.
This repository's own [`.depgraph.yml`](.depgraph.yml) is an example.

### Frameworks: external packages in layers

A layer pattern that starts with `ext:` matches packages outside the repository, so `forbidden`
keeps a domain free of frameworks:

| pattern | matches |
|---|---|
| `ext:gorm.io/gorm` | that package and what is under it (`gorm.io/gorm/clause`; in Python imports also its submodules: `ext:sqlalchemy` matches `sqlalchemy.orm`, while `lodash.debounce` is not under `ext:lodash`), the standard library included (`ext:net/http`, `ext:database/sql`) |
| `ext:*`, `ext:github.com/aws/*`, `ext:@nestjs/*` | packages known to be third-party, by a pattern (`*` is any run of characters, `/` included; `?` one character). See below for how an import is known to be third-party |
| `!<pattern>` | leaves packages out of the layer (`"!ext:github.com/google/uuid"`, `"!pkg/domain/legacy/**"`) |

```yaml
layers:
  domain: ["internal/domain/**"]
  frameworks: ["ext:*", "!ext:github.com/google/uuid", "!ext:github.com/shopspring/decimal"]
  http: ["ext:net/http"]
forbidden:
  domain: [frameworks, http]   # internal/domain -> ext:gorm.io/gorm (domain must not import frameworks)
```

A wildcard matches only an import known to be third-party. That is decided from the project's own
files and the resolution, never by a list of names shipped with depgraph, and nothing is executed:

| language | third-party | not third-party |
|---|---|---|
| Go | a module the importing module's `go.mod` (or its `go.work`) requires or replaces | a path whose first element has no dot (the go command's rule for the standard library) |
| TypeScript / JavaScript | resolved into `node_modules`, or declared in a `package.json` of the file or above it when not installed | a Node built-in, as esbuild resolves it (`punycode` is the built-in, `punycode/` the installed package) |
| Python | not the standard library of the version `languages.python` names, by the typeshed copy of the project's type checker (an external import is the standard library or an installed package) | in that version's standard library |
| Ruby | not classified yet | |

Each import records its class and why (`depgraph parse` shows them). An import a wildcard would match
if it were third-party but that could not be classified is not reported as a violation, and not dropped
either: `external-unclassified` (info) lists them per language with the reason.

**Python: `languages`.** The standard library depends on the Python version, and a repository often pins
it in several places (`.python-version`, `mise.toml`, a Dockerfile, ...), not always the same way. depgraph
does not choose: name the file that pins the version, relative to the file's directory (recommended), or
the version itself. A monorepo whose parts run different versions (an application on 3.12, a library kept
on 3.9 for its users) sets it per component, or in the part's own `.depgraph.yml`; a file uses the setting
of the deepest directory holding it.

```yaml
languages:
  python: {versionFile: .python-version}       # the root
components:
  lib/foo:
    languages:
      python: {versionFile: .python-version}   # lib/foo/.python-version
  "services/*":                                # each service's own file
    languages:
      python: {versionFile: Dockerfile}
```

- Version files: `.python-version`, `.tool-versions`, `mise.toml` (`.mise.toml`, `.config/mise.toml`,
  `mise.local.toml`), `pyproject.toml` (`[tool.pyright] pythonVersion`, `[tool.mypy] python_version`;
  `requires-python` is a range and does not count), `pyrightconfig.json`, `mypy.ini` / `setup.cfg`, a
  Dockerfile with a single `FROM python:<version>...`. Only an exact version is accepted: `latest`,
  `system`, `3`, ranges, several versions, a multi-stage Dockerfile or an `ARG` are errors, not guesses.
- The file may be a symlink (a dotfiles manager), also to a file outside the repository. It is read
  without following it into a FIFO, a device or a pseudo file, up to 64 KiB, parsed strictly, and only its
  relative path is ever shown (never its content).
- A directory between a file and its setting that pins another version (`lib/foo/.python-version` under
  a root setting), or a `mise.<env>.toml` that sets python, leaves that file's imports unclassified with
  the reason: set `languages.python` for that part instead of letting depgraph pick.
- The standard library of the version comes from typeshed's `stdlib/VERSIONS` in the project's own type
  checker, `node_modules/pyright` or `node_modules/basedpyright` from the setting's directory up to the
  root (several copies must agree on a module), or the directory `stdlib:` names. depgraph keeps no list
  of its own.
- Imports are classified when the rules are evaluated, so a change of the setting needs no rescan.
- `depgraph init` writes these settings: for every directory with Python files and a file that pins
  its version, it names that file (the files of one directory that agree: `.python-version` first, a
  Dockerfile last). Where they disagree it asks on a terminal and otherwise leaves the directory out; it
  keeps the rest of `.depgraph.yml` and the settings already there, warns about a version file that is a
  symlink out of the repository (a checkout without its target cannot classify) and about a missing
  typeshed copy. `--dry-run` prints the result.

### Inside the layers: dependency inversion and leaking types

The direction of imports is the Dependency Rule, but a use case may import the package of its
repository (it declares the port there, or uses its types) and still hold the PostgreSQL implementation
instead of the port, or return the ORM row from its API. `abstract:` names, per layer, the layers it may
use only through interfaces; the import itself stays allowed:

```yaml
abstract:
  usecase: [infra, db]          # the default, over the default layers
```

depgraph reads the members of every type and function (Go, TypeScript, Python): the types of fields,
constructor parameters (Go `New*`, `constructor`, `__init__`), parameters and results, and where a type of
another package is built (`&infra.T{}`, `new T()`, `T()`).

- **concrete-dependency**: a struct / class of `usecase` holds or builds an implementation of `infra`
  (a struct or class with behaviour: not an interface, an abstract class, a Protocol / ABC, a dataclass /
  pydantic model / Enum, or a struct whose only methods are `String`, `MarshalJSON`, `Scan`, ...), **and**
  that implementation satisfies an interface declared in the holder's package, the implementation's
  package or the inner layer: a Go struct with every method of the interface, a TypeScript / Python class
  that says it implements / extends it. Without such an interface nothing is reported: that the code
  should have one is a design call, not a fact. Collections, test files and the composition root (a
  layer not in the pair: `cmd/`, `main`) are left out.

  ```text
  internal/usecase depends on the concrete type internal/infra.PostgresOrders
  (internal/usecase/order.go:13 OrderUsecase.repo, and 1 more) although it implements
  internal/usecase.OrderRepository: hold the interface (usecase must depend on infra through an interface)
  ```
- **type-leak**: the API of `usecase` (parameters and results of exported functions and methods,
  exported fields holding data) names a struct or class of `infra`, so callers of the use case depend on
  the infrastructure's types. Interfaces are fine; constructor parameters and held implementations are
  concrete-dependency's.

A component takes `abstract` like `forbidden`, within its directory.

### Monorepos (`components`)

In a monorepo each part has its own architecture, often in another language: `pkg/` in Go,
`lib/auth` in Go and `lib/ui` in TypeScript, an npm package per directory under `packages/`.
Declare each part as a **component**, with layers relative to its directory, in one of two ways:

```yaml
# .depgraph.yml at the root
components:
  "packages/*":             # a pattern: one component per directory (packages/web, packages/kit, ...)
    layers:
      api: ["src/api/**"]
      ui: ["src/components/**"]
    forbidden:
      api: [ui]
  lib/ui:
    forbidden:              # forbidden alone: the default layers, inside lib/ui
      handler: [repository]
```

```yaml
# lib/auth/.depgraph.yml: the same file works with --root lib/auth
layers:
  domain: ["domain/**"]
  adapter: ["adapter/**"]
forbidden:
  domain: [adapter]
ignore: ["**/testdata/**"]                 # lib/auth/**/testdata/**
rules:
  unused-export: {ignore: ["api/**"]}       # lib/auth/api/**
```

- A component's forbidden pairs apply to imports between two packages of the same directory:
  `packages/web/src/api -> packages/web/src/components` is reported as
  `(packages/web: api must not import ui)`, an import from `packages/web` into `packages/kit` is not.
  The language does not matter: the layers are directories.
- The default layers are a guess from directory names, so they are not applied inside a component
  (nor to an import into one). Layers the root declares (`layers:`) are the repository's own
  architecture and apply everywhere, across components too (`apps` must not import each other).
- A `.depgraph.yml` below the root is read with the root's (`scan`, `check`, `watch`, `hook`, `gate`;
  `watch` reloads it when it changes). Its `layers`, `forbidden`, `components`, `ignore` and the
  `ignore` / `entries` of the unused-code rules are relative to its directory. A directory's own file
  takes precedence over a pattern of `components:` that matches it; declaring a directory both in
  `components:` and in its own file is an error. `project` and `store` apply only where it is the
  root, and so do rule switches, severities and thresholds: those are printed as a warning, not applied.
  Files in directories the scan skips (`node_modules`, `.git`, `.depgraphignore`, ...) are not read.
- A component directory is a path below the root or a pattern with `*` and `?` (no `**`).

### Duplicated code

For every function, method and class, depgraph keeps the shape of its body: the syntax tokens with
comments dropped and every identifier and literal masked, so a renamed copy has the same shape. Copies
are grouped when their shapes are identical, or when the winnowed fingerprints of their token 4-grams
(Schleimer et al., *Winnowing*) are at least 80% the same (Jaccard), which catches a copy with a
statement added or changed. Bodies under 50 tokens are ignored, as are fingerprints shared by more
than 10 functions (error handling and loops every function has) and test code (`_test.go`, `*.test.*`,
`*.spec.*`, `test_*.py`, `tests/`, ...), whose table tests look alike on purpose. This is what
compilers and linters do not report: two working functions that do the same thing.

To query by hand:

```cypher
// import cycles
MATCH p=(a:Package {project: 'my-repo'})-[:IMPORTS*2..8]->(a) WHERE a.external=false
RETURN [n IN nodes(p) | n.key] AS cycle LIMIT 20;

// highest fan-in
MATCH (p:Package {project: 'my-repo'})<-[:IMPORTS]-(q) WHERE p.external=false
RETURN p.key, count(q) AS fanIn ORDER BY fanIn DESC LIMIT 10;
```

## Architecture

```
main.go                              entry point (just calls cmd.Execute())
pkg/domain/graph/                    language-agnostic graph model, Repository / Resolver interfaces
pkg/domain/rules/                    rules, Config (layers, package globs), Finding / diff, Repository interface
pkg/domain/project/                  Project, key rules / move-or-conflict decision, Repository interface
pkg/services/analysis/               scan, apply changes, evaluate rules, LSP refinement
pkg/infrastructure/repository/graph/ Neo4j writes and queries
pkg/infrastructure/repository/rules/ rule evaluation in Cypher
pkg/infrastructure/repository/project/ Project nodes
pkg/infrastructure/config/           .depgraph.yml (project key, rule configuration, store settings; nested files of a monorepo)
pkg/infrastructure/neo4jprovider/    how Neo4j is provided: Provider contract, selection, external
  container/                         Neo4j in a docker / podman / nerdctl container (lifecycle, labels, cleanup)
pkg/infrastructure/workspace/        project identity of a root (.depgraph.yml, git origin / worktree)
pkg/infrastructure/resolver/         repository walk + exclude patterns
  treesitter/                        default: structural analysis (imports / declarations, gotreesitter)
  imports/                           import resolution per ecosystem (esbuild, go.mod / go.work, Python import system, Ruby require / Zeitwerk)
  lsp/                               refinement with language servers (callHierarchy / references)
pkg/infrastructure/watch/            fsnotify + debounce
pkg/infrastructure/claudemd/         CLAUDE.md managed section
pkg/interfaces/cmd/                  CLI (cobra: parse / scan / check / watch / serve / mcp / hook / gate / projects / store)
pkg/interfaces/handler/ui/           HTTP server + SSE + embedded UI (web/ build output)
pkg/interfaces/handler/mcpapp/       MCP server: the UI as an MCP App (single-file web/ build, app-only tools)
web/                                 browser UI source (Vite + TypeScript, Cytoscape + elkjs)
plugin/                              Claude Code plugin (hooks, skills; .claude-plugin/marketplace.json lists it)
pkg/interfaces/handler/hook/         Claude Code hook and quality gate (summary / diff / findings added in a session)
```

The analysis backend is pluggable through `graph.Resolver`. Priority: treesitter → lsp → scip.

## Roadmap

- [x] LSP integration (watch only): ask `textDocument/references` / `callHierarchy` for symbols of changed files and add `CALLS` edges
- [ ] `Commit` nodes for time-series analysis (dependency growth per AI session)
- [ ] SCIP index import (batch precise analysis for CI)
- [x] Multiple projects in one database (`--project`, `.depgraph.yml` `project:`, `depgraph projects`)
- [x] Rule configuration in `.depgraph.yml` (layers, forbidden imports, severities, ignore)
- [x] Monorepos: layers per component (`components:`, a `.depgraph.yml` per directory)
- [x] MCP server showing the browser UI as an MCP App (`depgraph mcp`)
- [ ] MCP tools to query the graph directly (dependents, unused code, duplicates) from Claude Code / Cursor
- [x] depgraph provides Neo4j itself in a container (docker / podman / nerdctl), ephemeral or persistent
- [ ] `local` Neo4j provider: run a Neo4j installation (`NEO4J_HOME` or `neo4j` on `PATH`) as a child process, data in a per-run directory, loopback only

## Releasing

Releases are automated with [release-please](https://github.com/googleapis/release-please) and
[GoReleaser](https://goreleaser.com) (`.github/workflows/release.yml`).

1. Merge PRs into `main` with [Conventional Commits](https://www.conventionalcommits.org/) titles
   (while < 1.0: `feat:` / `fix:` / `perf:` → patch, `feat!:` / `BREAKING CHANGE:` → minor).
   PRs are squash-merged, so the PR title is the commit message.
2. release-please keeps a release PR open that bumps `release-please/manifest.json`,
   `pkg/version/version.go` and `CHANGELOG.md`. The first release is `v0.1.0`.
3. Merge the release PR. release-please tags `vX.Y.Z` and creates the GitHub release, and
   GoReleaser builds the six archives and `checksums.txt` and attaches them.

Other pushes to `main` (and to `release/vN` maintenance branches) publish a prerelease
`v<current version>-dev.<short sha>`. To try the release build locally (writes `dist/`,
publishes nothing): `mise run release:snapshot`. `mise run ci` runs `goreleaser check`.

The workflow needs a GitHub App installed on the repository with Contents, Pull requests and
Issues write access, and the repository secrets `RELEASE_PLEASE_CLIENT_ID` (its client ID) and
`RELEASE_PLEASE_APP_PRIVATE_PEM` (its private key). An App token is used instead of
`GITHUB_TOKEN` so that the release PR runs CI.

## License

Apache-2.0 (see [LICENSE](LICENSE) and [NOTICE](NOTICE)). Neo4j itself is not bundled: depgraph connects to your own Neo4j or runs
the official Neo4j Community image (GPLv3) with your container runtime.
