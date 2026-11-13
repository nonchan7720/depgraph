# depgraph

English | [日本語](README_ja.md)

depgraph makes **AI-written code production-ready**. It finds the debt coding agents leave
behind, the kind the project's own compiler, type checker and linters do not report, and feeds
it back to the agent while it is still working.

It loads the dependency graph of the repository into Neo4j, evaluates architectural rules on it
every time a file is rewritten, and puts the result where the agent reads it: the next prompt,
`CLAUDE.md`, and a gate the agent cannot finish past.

## What it finds

| debt | rules |
|---|---|
| **Code nothing uses**: exports, files and packages no entry point reaches, dependencies nothing imports | `unused-export`, `unreachable-file`, `unreachable-package`, `unused-dependency` |
| **Duplicated code**: the same logic re-implemented, also under other names | `duplicate-code`, `duplicate-symbol` |
| **Cycles** between packages, modules, files and functions | `import-cycle`, `module-cycle`, `file-cycle`, `function-cycle` |
| **Broken layers**: a forbidden import, an implementation held instead of its interface, a type leaking through an API | `layer-violation`, `concrete-dependency`, `type-leak` |
| **Structure that rots**: hubs everything depends on, units with several responsibilities, functions that know too much, wrappers that add nothing | `god-package`, `low-cohesion`, `wide-function`, `pass-through` |

What a compiler or linter already reports (an import that does not resolve, a type error, an
unused local) is left to them. A finding that is not true is treated as a bug: depgraph reads
what the code says and does not guess. Every rule is described in [Rules](docs/rules.md).

## Quick start

You need the `depgraph` binary ([Install](docs/install.md)) and a container runtime: Docker,
Podman or nerdctl. No compose file and no Neo4j installation.

```bash
depgraph watch --root /path/to/your/repo
```

This:

1. starts Neo4j in a container (10-20 s; the image, about 1 GB, is pulled once),
2. scans the repository and prints the findings,
3. watches for changes and re-analyzes only the files that changed,
4. serves a browser UI at http://127.0.0.1:7777 that updates live,
5. keeps the `<!-- depgraph:start -->` ... `<!-- depgraph:end -->` section of the repository's
   `CLAUDE.md` up to date (hand-written content is kept).

When `watch` exits, however it exits, the container is removed with its data; `--persist` keeps
the data ([Neo4j store](docs/neo4j.md)).

For a single run, for example in CI:

```bash
depgraph scan --root . --claude-md "" --fail-on warn   # exit 1 on findings of at least warn
```

## Use it with a coding agent

Keep `depgraph watch` running, then:

| | what the agent gets | |
|---|---|---|
| **Plugin** (Claude Code, Codex) | the hooks below, plus `/depgraph:setup` and `/depgraph:fix` | [Coding agents](docs/agents.md) |
| **Hooks** | a summary of the findings at session start, and on every prompt only what appeared or was resolved since | [Hooks](docs/agents.md#claude-code-hooks) |
| **Quality gate** | after each edit, the debt *this session* added; the agent cannot finish while it remains | [Quality gate](docs/agents.md#quality-gate-claude-hook-gate) |
| **MCP App** | the graph view inside the conversation | [MCP App](docs/ui.md#mcp-app) |

```text
/plugin marketplace add nonchan7720/depgraph
/plugin install depgraph@depgraph
```

## How it works

```
 source files        resolver             graph store        rules              findings
 Go, TS / JS,   ──►  tree-sitter     ──►  Neo4j         ──►  Cypher        ──►  CLAUDE.md section
 Python, Ruby        + import             (one graph                            hooks / gate  (the agent)
                     resolution           per project)                          browser UI / MCP App
                                                                                exit code     (CI)
```

Each file is parsed with tree-sitter and its imports are resolved the way its ecosystem resolves
them. The result is stored per file in Neo4j, so a change replaces one file's part of the graph.
Before the rules run, the names the files use are resolved across the project into a linked
graph (calls between functions, loads between files, types and their uses), and every rule is a
Cypher query over it. [Architecture](docs/architecture.md) explains each step, what Neo4j
is used for, and why the store is Neo4j.

## Supported languages

Each page says what depgraph reads in that language and what the rules mean there.

| language | |
|---|---|
| Go | [docs/languages/go.md](docs/languages/go.md) |
| TypeScript / JavaScript | [docs/languages/typescript.md](docs/languages/typescript.md) |
| Python | [docs/languages/python.md](docs/languages/python.md) |
| Ruby (Rails, engines, packwerk packs) | [docs/languages/ruby.md](docs/languages/ruby.md) |

## Documentation

| page | what is in it |
|---|---|
| [Install](docs/install.md) | release binaries, building from source |
| [Commands](docs/commands.md) | every command and flag, `.depgraphignore`, CI |
| [Rules](docs/rules.md) | every rule, what counts as unused, how copies are found, exempting a function |
| [Configuration](docs/configuration.md) | `.depgraph.yml`: layers, forbidden imports, severities, frameworks, monorepos |
| [Coding agents](docs/agents.md) | Claude Code and Codex plugins, hooks, the quality gate, git worktrees |
| [Browser UI and MCP App](docs/ui.md) | the graph view, in a browser or inside the conversation |
| [Neo4j store and projects](docs/neo4j.md) | how depgraph gets a Neo4j (container or your own), persistence, several repositories in one database |
| [Architecture](docs/architecture.md) | how the features are built, the graph schema, the Neo4j features used, why Neo4j and not SQLite |
| [Development](docs/development.md) | roadmap, releasing |

## License

Apache-2.0 (see [LICENSE](LICENSE) and [NOTICE](NOTICE)). Neo4j itself is not bundled: depgraph connects to your own Neo4j or runs
the official Neo4j Community image (GPLv3) with your container runtime.
