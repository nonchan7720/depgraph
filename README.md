# depgraph

English | [日本語](README_ja.md)

A tool that loads the dependency graph of AI-written code into Neo4j, visualizes it, checks architectural rules and writes the results back into `CLAUDE.md`.
During vibe coding it updates the graph every time a file is rewritten and immediately detects "a new import cycle", "a layer violation" or "a duplicated function", feeding that back into the next prompt.

Supported languages (tree-sitter): Go / TypeScript / JavaScript / Python

Parsing uses [gotreesitter](https://github.com/odvcencio/gotreesitter), a pure-Go tree-sitter runtime, so depgraph needs no C toolchain: it builds with `CGO_ENABLED=0` and cross-compiles to any `GOOS`/`GOARCH` with plain `go build`.
Builds use the tags in `GOFLAGS` of `mise.toml` (`gotreesitter_no_copyleft` and `grammar_subset_<language>`), which keep the GPL/MPL grammars that gotreesitter also vendors out of the binary; `mise run check:no-copyleft` verifies it. Release builds should use the same tags.

## Quick start

```bash
# 1. Start Neo4j (neo4j / depgraph123)
docker compose up -d

# 2. Build
mise run build   # or: mise exec -- go build -o bin/depgraph .  (applies the env of mise.toml)

# 3. Watch the target repository
./bin/depgraph watch --root /path/to/your/repo
```

After the initial full scan, depgraph watches for file changes and re-analyzes only what changed.
A browser UI starts at http://127.0.0.1:7777 and the graph and findings update live (SSE).
Findings are printed to stderr, and the `<!-- depgraph:start -->` ... `<!-- depgraph:end -->`
section of `/path/to/your/repo/CLAUDE.md` is updated (hand-written content is kept).

Neo4j Browser: http://localhost:7474

To try it on a generated sample repository, run `mise run demo`. It watches the sample as
the project `depgraph-demo` in the same Neo4j (UI http://127.0.0.1:7778); its `--reset` clears
only that project, so other projects are never touched. Remove it with
`bin/depgraph projects rm depgraph-demo`.

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
| `scan --root DIR [--reset]` | full scan → evaluate rules → update CLAUDE.md |
| `watch --root DIR [--no-lsp] [--no-ui]` | scan, then watch with fsnotify and update incrementally (tree-sitter, then LSP adds CALLS/REFERENCES) |
| `check --root DIR` | evaluate rules against the graph already in Neo4j (updates CLAUDE.md) |
| `serve --root DIR [--addr 127.0.0.1:7777]` | browser UI over the graph already in Neo4j (no watching, CLAUDE.md untouched) |
| `parse --root DIR [files...]` | print the tree-sitter result as JSON, one file per line, without Neo4j (debugging) |
| `hook <session-start\|prompt>` | Claude Code hook: print findings as additional context (read-only, always exits 0) |
| `projects list` / `projects rm <key>` / `projects prune [--dry-run]` | list / delete the projects in the database |

Flags (see `depgraph <command> --help`):

| flag | commands | default |
|---|---|---|
| `--root` | all | `.` |
| `--project` | all | `project` in `<root>/.depgraph.yml`, else the directory name of `--root` (`@<worktree>` added in a linked git worktree) |
| `--exclude` | all | none (comma-separated exclude patterns) |
| `--ignore-file` | all | `.depgraphignore` (relative to `--root`, empty to disable) |
| `--neo4j` / `--user` / `--pass` / `--db` | scan / check / watch / serve / hook / projects | `$NEO4J_URI` / `$NEO4J_USER` / `$NEO4J_PASSWORD` / `$NEO4J_DATABASE` (fallback `bolt://localhost:7687` / `neo4j` / `depgraph123` / `neo4j`) |
| `--claude-md` | scan / check / watch | `$DEPGRAPH_CLAUDE_MD` or `CLAUDE.md` (relative to `--root`, empty to disable) |
| `--reset` | scan / watch | false (clear this project's data before scanning; other projects are kept) |
| `--workers` | parse / scan / watch | `0` (files parsed in parallel during a full scan; 0 = GOMAXPROCS. Output order does not depend on it) |
| `--addr` | watch / serve | `$DEPGRAPH_ADDR` or `127.0.0.1:7777` |
| `--debounce` | watch | `400ms` |
| `--no-ui` / `--no-lsp` | watch | false |
| `--timeout` / `--debug` | hook | `3s` / false |

## Claude Code hooks

`depgraph hook <session-start|prompt>` feeds the findings already in Neo4j back into
Claude Code (read-only: it never scans and never writes CLAUDE.md).

- `session-start`: a compact summary — stats, then findings by severity (errors first);
  rules with many findings show the count and a few examples, the whole output is capped
  (about 4,000 characters) so even 50 `duplicate-symbol` findings do not flood the context
- `prompt`: only the findings that appeared or were resolved since the last injection in the
  same session; nothing when nothing changed. State is kept per `session_id` in
  `<root>/.claude/.depgraph-hook-state.json` (add it to `.gitignore`)
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

The Neo4j connection comes from `--neo4j` etc. or `$NEO4J_URI` etc. (default `bolt://localhost:7687`).

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

## Graph schema

```
(Project {key, name, root, origin, base, worktree, lastScanAt})
(Package {project, key, name, external})
(File    {project, path, language, lines, updatedAt})
(Symbol  {project, name, kind, exported, line, endLine, language, file})

(Package)-[:CONTAINS]->(File)-[:DECLARES]->(Symbol)
(File)-[:IMPORTS {spec}]->(Package)
(Package)-[:IMPORTS]->(Package)        # recomputed from file-level imports
(Symbol)-[:CALLS|REFERENCES]->(Symbol) # added by LSP for changed files during watch
```

`project` is the project key; Package is unique by `(project, key)` and File by
`(project, path)`. `Package.key` is the directory path from the repository root (the root is `.`).
Imports outside the repository become a Package with key `ext:<spec>` and `external=true`
(also per project).

## Rules

| rule | severity | description |
|---|---|---|
| `import-cycle` | error | import cycle between packages |
| `layer-violation` | error | forbidden pairs such as `handler → repository` (change in `rules.DefaultConfig`) |
| `god-package` | warn | imported by 10 or more packages |
| `duplicate-symbol` | warn | an exported function/class with the same name exists in several files |

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
pkg/domain/rules/                    Finding / Config / diff, Repository interface
pkg/domain/project/                  Project, key rules / move-or-conflict decision, Repository interface
pkg/services/analysis/               scan, apply changes, evaluate rules, LSP refinement
pkg/infrastructure/repository/graph/ Neo4j writes and queries
pkg/infrastructure/repository/rules/ rule evaluation in Cypher
pkg/infrastructure/repository/project/ Project nodes
pkg/infrastructure/workspace/        .depgraph.yml and git (origin, worktree) of a root
pkg/infrastructure/resolver/         repository walk + exclude patterns
  treesitter/                        default: structural analysis (imports / declarations, gotreesitter)
  lsp/                               refinement with language servers (callHierarchy / references)
pkg/infrastructure/watch/            fsnotify + debounce
pkg/infrastructure/claudemd/         CLAUDE.md managed section
pkg/interfaces/cmd/                  CLI (cobra: parse / scan / check / watch / serve / hook / projects)
pkg/interfaces/handler/ui/           HTTP server + SSE + embedded UI (web/ build output)
web/                                 browser UI source (Vite + TypeScript, Cytoscape + elkjs)
pkg/interfaces/handler/hook/         Claude Code hook (summary / diff / state file)
```

The analysis backend is pluggable through `graph.Resolver`. Priority: treesitter → lsp → scip.

## Roadmap

- [x] LSP integration (watch only): ask `textDocument/references` / `callHierarchy` for symbols of changed files and add `CALLS` edges
- [ ] `Commit` nodes for time-series analysis (dependency growth per AI session)
- [ ] SCIP index import (batch precise analysis for CI)
- [x] Multiple projects in one database (`--project`, `.depgraph.yml` `project:`, `depgraph projects`)
- [ ] Rule configuration in `.depgraph.yml` (only `project:` is read today)
- [ ] MCP server mode (query directly from Claude Code / Cursor)

## License

Apache-2.0 (planned). Neo4j itself is not bundled; depgraph connects to your own Neo4j.
