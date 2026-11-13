# Commands

English | [日本語](ja/commands.md) · [README](../README.md)

| command | description |
|---|---|
| `scan --root DIR [--reset] [--fail-on warn]` | full scan → evaluate rules → update CLAUDE.md (with `--fail-on`, exit 1 on findings; see [CI](#ci)) |
| `watch --root DIR [--no-lsp] [--no-ui]` | scan, then watch with fsnotify and update incrementally (tree-sitter, then LSP adds CALLS/REFERENCES) |
| `check --root DIR [--fail-on warn]` | evaluate rules against the graph already in Neo4j (updates CLAUDE.md) |
| `serve --root DIR [--addr 127.0.0.1:7777]` | browser UI over the graph already in Neo4j (no watching, CLAUDE.md untouched) |
| `mcp --root DIR` | stdio MCP server that shows the browser UI as an [MCP App](ui.md#mcp-app) (reads the graph already in Neo4j, CLAUDE.md untouched) |
| `init --root DIR [--dry-run]` | add a `languages` setting to `.depgraph.yml` for every directory with Python files whose files pin its Python version (asks on a terminal where they disagree; see [Frameworks](configuration.md#frameworks-external-packages-in-layers)) |
| `parse --root DIR [files...]` | print the tree-sitter result as JSON, one file per line, without Neo4j (debugging) |
| `hook <session-start\|prompt>` | Claude Code / Codex hook: print findings as additional context (read-only, always exits 0) |
| `codex install-hooks [--repo\|--to FILE] [--print] [--remove]` | copy the plugin's Codex hooks into a Codex `hooks.json` (see [Codex plugin](agents.md#codex-plugin)) |
| `gate [files...] [--fail-on warn]` | quality-gate check ([claude-hook-gate](agents.md#quality-gate-claude-hook-gate)): apply the changed files, exit 1 on findings the session added |
| `projects list` / `projects rm <key>` / `projects prune [--dry-run]` | list / delete the projects in the database |
| `store ls` / `store down` / `store rm [<key>...] [--all]` | list / stop / delete the Neo4j containers and data volumes depgraph manages (container provider) |
| `schema` | print the JSON Schema of `.depgraph.yml` (see [Configuration](configuration.md)) |
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
| `--plugin-root` / `--to` / `--repo` / `--print` / `--remove` | codex install-hooks | `$PLUGIN_ROOT`, `$CLAUDE_PLUGIN_ROOT` or `.` / `$CODEX_HOME/hooks.json` / false / false / false |

## Excluding paths: `.depgraphignore`

One pattern per line, `#` comments. A pattern without `/` matches a file or directory name at any
depth (`*.gen.go`, `tmp*`); with `/` it is a path from the root (`internal/ent`,
`internal/**/testdata`); a leading `/` anchors it at the root. Excluded paths are not scanned.

These directory names are always skipped, at any depth: `.git`, `node_modules`, `vendor`, `dist`,
`build`, `.venv`, `venv`, `__pycache__`, `target`, `.next`, `.idea`, `.vscode`, `coverage`,
`.cache`, `.worktrees`, `.claude`. When source lives in one of them (`src/build/startup/`), lift it
with a `!` pattern, as in `.gitignore`:

```
!src/build     # only this directory (src/build/node_modules stays skipped)
!build         # every directory named build
!/build        # only the root build
*.gen.go
!keep.gen.go   # re-include a file an earlier pattern excluded
```

Patterns apply in order and the last match wins; the built-in names count as coming first. A `!`
pattern matches the path itself, not what is below it. As in `.gitignore`, nothing inside an
excluded directory can be re-included. `\!name` matches a literal leading `!`.

## CI

`scan` and `check` exit 0 whatever they find, unless `--fail-on <error|warn|info>` is given: they
then exit 1 when the evaluation has findings of at least that severity (all of them, not only new
ones; the gate is for what a session added). For a CI job, with the container provider on a
runner that has Docker:

```bash
depgraph scan --root . --claude-md "" --fail-on warn   # --claude-md "": do not rewrite CLAUDE.md
```

`--fail-on error` fails only on import cycles and layer violations.
