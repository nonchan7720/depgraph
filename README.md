# depgraph

English | [日本語](README_ja.md)

A tool that loads the dependency graph of AI-written code into Neo4j, visualizes it, checks architectural rules and writes the results back into `CLAUDE.md`.
During vibe coding it updates the graph every time a file is rewritten and immediately detects "a new import cycle", "a layer violation" or "a duplicated function", feeding that back into the next prompt.

Supported languages (tree-sitter): Go / TypeScript / JavaScript / Python

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
| `version` / `--version` | print the version (`version` adds the commit, build date and Go toolchain) |

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
(File    {project, path, language, lines, updatedAt,
          scope, entry, generated, uses, exports, keep})   # usage, for the unused-code rules
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
(`keep`: a Python `__all__` entry, a decorated definition), whether it is generated or vendored, and
why it is an entry point (`entry`). They are replaced with the file, so a `watch` update keeps them
correct; the unused-code rules join them across the project when they run.

## Import resolution

Imports are resolved the way each ecosystem resolves them, from the project's own configuration:

| language | how |
|---|---|
| TypeScript / JavaScript | esbuild's resolver: `tsconfig.json` / `jsconfig.json` (`paths`, `baseUrl`, `extends`), `package.json` (`exports`, `imports`, workspaces), `node_modules`, ESM `./x.js` written for `x.ts` |
| Go | `go.mod` / `go.work` (nested modules, `use`, `replace`) |
| Python | the import system over the repository's package roots (the repo root, `src/`, `pyproject.toml` / `setup.cfg` package dirs); a directory without `__init__.py` is a namespace package and does not shadow the standard library |

A relative path, an alias or a module of the project that points at nothing is unresolved, not
external, so it never shows up as a dependency. `watch` rescans when one of these configuration files changes.

## Rules

| rule | severity | description |
|---|---|---|
| `import-cycle` | error | import cycle between packages |
| `layer-violation` | error | a package imports a layer it must not import (`handler → repository`, ... or your own layers) |
| `duplicate-code` | warn | the same logic in several places, whatever the names: identical, or a copy changed a little (see below) |
| `god-package` | warn | imported by 10 or more packages |
| `duplicate-symbol` | warn | an exported function/class with the same name exists in several files |
| `unused-export` | warn | an exported declaration nothing in the project uses, not even its own file (Go: exported functions and types of non-main packages, not methods; TS/JS: `export`ed declarations, followed through re-exports and barrels; Python: public module-level functions and classes) |
| `unreachable-file` | warn | a TS/JS/Python file no entry point reaches through imports; one finding per file nothing imports, listing the files only it leads to |
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
- Python: `__main__.py`, a `__main__` guard, `conftest.py`, `test_*.py`, `tests/`, migrations,
  `setup.py` / `manage.py` / `wsgi.py` / `asgi.py`, the targets of `pyproject.toml` / `setup.cfg`
  scripts and entry points, and in a Django project its autodiscovered modules (`models`, `admin`,
  `apps`, `urls`, `views`, `wagtail_hooks`, ...). `__init__.py` is never unreachable. Names in
  `__all__` and decorated definitions (routes, fixtures, tasks) are kept.
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

```yaml
project: my-repo            # project key (see Projects)

layers:                     # layer name -> package path globs
  domain: ["pkg/domain/**"]
  services: ["pkg/services/**"]
  infrastructure: ["pkg/infrastructure/**"]
  interfaces: ["pkg/interfaces/**", "cmd/**"]

forbidden:                  # layer -> layers it must not import
  domain: [infrastructure, services, interfaces]
  services: [interfaces]
  infrastructure: [interfaces, services]

rules:                      # per rule: enabled, severity (error | warn | info), options
  import-cycle: {severity: error}
  layer-violation: {severity: error}
  god-package: {severity: warn, fanIn: 10}
  duplicate-symbol: {enabled: false}
  duplicate-code: {minTokens: 50, similarity: 0.8}   # body size and fingerprint similarity
  # duplicate-symbol: {ignore: [main, init, New]}   # names not checked (replaces the defaults)
  unused-export: {ignore: ["pkg/api/**"]}            # paths not reported (a library's public API)
  unreachable-file: {entries: ["plugins/**"]}       # entry points depgraph cannot see
  # entries / ignore are shared by unused-export, unreachable-file and unreachable-package

ignore: ["**/testdata/**"]  # packages left out of every rule
```

- **Package globs** match the package key (the directory from the root, `.` for the root) by
  whole path segments: `**` is any number of segments, `*` and `?` stay within one segment.
  `pkg/domain/**` matches `pkg/domain` and everything below it; `**/db/**` matches `db` and
  `internal/db/sql` but not `pkg/feedback`.
- A package may be in several layers. An import is a violation only when it crosses the pair:
  it is not reported when both packages are in the forbidden layer, or both in the importing one.
- `layers` replaces the default layers, and with them the default forbidden pairs; `forbidden`
  alone refers to the default layers.
- `ignore` excludes packages from the findings (cycles through them, fan-in from them, ...);
  unlike `.depgraphignore` they are still scanned and shown in the UI.
- Decoding is strict: an unknown key, rule, option, severity or a `forbidden` entry naming an
  undefined layer is an error that names the file, instead of a setting silently ignored.

Without `.depgraph.yml` the defaults are the layers `controller`, `db`, `domain`, `handler`,
`infra`, `repository` and `usecase`, each matching a path segment of that name (plus
`controllers`, `handlers`, `infrastructure`, `repositories`, `usecases`), with the forbidden
pairs `handler → repository / infra / db`, `controller → repository / db`,
`domain → infra / handler / repository` and `usecase → handler`.
This repository's own [`.depgraph.yml`](.depgraph.yml) is an example.

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
pkg/infrastructure/config/           .depgraph.yml (project key, rule configuration)
pkg/infrastructure/workspace/        project identity of a root (.depgraph.yml, git origin / worktree)
pkg/infrastructure/resolver/         repository walk + exclude patterns
  treesitter/                        default: structural analysis (imports / declarations, gotreesitter)
  imports/                           import resolution per ecosystem (esbuild, go.mod / go.work, Python import system)
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
- [x] Rule configuration in `.depgraph.yml` (layers, forbidden imports, severities, ignore)
- [ ] MCP server mode (query directly from Claude Code / Cursor)

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

Apache-2.0 (planned). Neo4j itself is not bundled; depgraph connects to your own Neo4j.
