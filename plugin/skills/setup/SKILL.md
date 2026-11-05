---
name: setup
description: Set depgraph up in the current project, or review an existing setup. depgraph finds the debt AI agents leave behind that compilers and linters do not report (exports and files nothing uses, the same logic written twice, import cycles, layer violations, god packages) and feeds it back to Claude Code while it works. Writes .depgraph.yml (architecture layers, rule options, entry points), .depgraphignore (generated / vendored / fixture paths) and, when the project uses claude-hook-gate (.claude/gate.yaml), the depgraph gate rule and session_start entry. Use when the user asks to install, set up, configure, tune or review depgraph, its layers or rules, or its quality gate.
argument-hint: "[init|update|review]"
---

# depgraph setup

Arguments: `$ARGUMENTS`. `init` writes the files that do not exist yet (ask before replacing
one); `update` changes the existing ones for what the user describes, or for what the code now
shows; `review` reports what the current setup does and what to change, without editing.
Without an argument: `init` when `.depgraph.yml` does not exist, else `update`.

depgraph's README is the reference for every key and rule:
https://github.com/nonchan7720/depgraph#readme (`depgraph --help` and `depgraph schema` print the
flags and the JSON Schema of `.depgraph.yml` of the installed version).

## 1. The binary

Run `depgraph version`. When it is missing, or older than this plugin's version, tell the user how
to install it and stop there unless they ask you to do it. The plugin does not install it.

- GitHub Releases (the only distributed binaries): download `depgraph_<os>_<arch>.tar.gz` (`.zip`
  on Windows; os linux / darwin / windows, arch amd64 / arm64) and `checksums.txt` from
  https://github.com/nonchan7720/depgraph/releases, verify, put it on `PATH`:

  ```bash
  sha256sum --ignore-missing -c checksums.txt   # macOS: shasum -a 256 --ignore-missing -c checksums.txt
  tar xzf depgraph_linux_amd64.tar.gz && install depgraph ~/.local/bin/
  ```

- Neo4j is provided by depgraph itself in a container (Docker, Podman or nerdctl); nothing else
  is installed. An external Neo4j works too (`NEO4J_URI`).

## 2. Discover (read, do not guess)

- Languages depgraph analyses: Go, TypeScript / JavaScript, Python. Note the module / package
  roots (`go.mod`, `package.json`, `tsconfig.json`, `pyproject.toml`).
- The architecture the code already has: top-level source directories and what imports what
  (`depgraph parse --root . <files>` prints the imports of files as JSON without Neo4j). Layers
  are only worth declaring when the directories show them (`domain`, `usecase`, `handler`,
  `infra`, ...). Never invent an architecture; ask when the intent is unclear.
- Generated code (headers such as `Code generated ... DO NOT EDIT`, `ent/`, `*_pb.go`,
  `*.gen.ts`), vendored code, test fixtures with code that does not build (`testdata/`), git
  worktrees checked out inside the repository (`.claude/worktrees/` is skipped already).
- Entry points depgraph cannot see (plugins loaded by name, files an external tool runs, a
  library's public API the repository itself does not use). depgraph already treats `main`
  packages, tests, `package.json` entries and scripts, framework routes, Dockerfiles, CI
  workflows, Makefiles and many more as entry points (README "Unused code"); only add what it
  cannot know.

## 3. `.depgraph.yml`

Every key is optional; write only what differs from the defaults. Start with the schema line so
editors complete and check it, with the tag of the installed version:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/nonchan7720/depgraph/v<version>/schema/depgraph.schema.json
layers:                     # layer name -> package path globs (whole path segments; ** any depth)
  domain: ["internal/domain/**"]
  usecase: ["internal/usecase/**"]
  infrastructure: ["internal/infra/**"]
  interfaces: ["cmd/**", "internal/handler/**"]
forbidden:                  # layer -> layers it must not import
  domain: [usecase, infrastructure, interfaces]
  usecase: [interfaces]
rules:
  unused-export: {ignore: ["pkg/api/**"]}        # a library's public API
  unreachable-file: {entries: ["plugins/**"]}    # loaded by name at run time
ignore: ["**/testdata/**"]  # packages left out of every rule (still scanned)
```

- `layers` replaces the default layers and their forbidden pairs; `forbidden` alone refers to the
  defaults (`controller`, `db`, `domain`, `handler`, `infra`, `repository`, `usecase`).
- Decoding is strict: an unknown key, rule, option or layer name is an error that names the file.
- Prefer a `depgraph:entry` comment (`// depgraph:entry`, `# depgraph:entry`) in the file itself for
  a single entry point; `entries` / `ignore` are for path globs.
- Do not disable a rule to make findings go away. Disabling is for a rule that does not apply to
  the project (e.g. `duplicate-symbol` where the same names are the convention), and say why in a
  comment.

## 4. `.depgraphignore`

One pattern per line, `#` comments. A pattern without `/` matches a file or directory name at any
depth (`*.gen.go`); with `/` it is a path from the root (`internal/ent`, `internal/**/testdata`).
Ignored paths are not scanned at all (unlike `ignore` in `.depgraph.yml`). Put generated and
vendored code and fixtures here, never code people write.

## 5. Claude Code integration

- The plugin's hooks add the findings at session start and the changes since at every prompt.
  They stay quiet when the project's `.claude/settings.json` runs `depgraph hook` itself; if it
  does, offer to remove those entries so the plugin's hooks take over.
- When the project has `.claude/gate.yaml` (claude-hook-gate), add the gate rule, matched on the
  project's languages, so debt the session adds blocks the agent from finishing, and the
  session_start entry (the plugin then leaves session start to it):

  ```yaml
  session_start:
    - { name: depgraph, timeout: 10, cmd: "command -v depgraph >/dev/null || exit 0; depgraph hook session-start --format text" }

  rules:
    - match: ["**/*.go", "**/*.{ts,tsx,js,jsx,mjs,cjs}", "**/*.py"]   # only the languages the project has
      timeout: 60
      run:
        - { name: depgraph, cmd: "command -v depgraph >/dev/null || exit 0; depgraph gate" }
  ```

  Keep the existing rules and their order; add, do not rewrite. `depgraph gate --fail-on error`
  fails only on cycles and layer violations, if the user wants a softer gate. It also checks
  agents that work in git worktrees under `.claude/worktrees/` (no extra setup).
- Without claude-hook-gate, do not create `gate.yaml` unless the user asks for a quality gate.

## 6. Run it

depgraph reads the graph from Neo4j, which `depgraph watch` keeps up to date. Tell the user to run
it in a terminal of their own (it keeps running and starts Neo4j in a container, about 1 GB the
first time):

```bash
depgraph watch --root .
```

It prints the findings, serves a browser UI at http://127.0.0.1:7777 and keeps the
`<!-- depgraph:start -->` section of `CLAUDE.md` up to date. Do not start it in the background of
this session. When it is running, a `depgraph check --claude-md ""` prints the current findings;
read them to verify the configuration (a layer that matches nothing, entry points still reported)
and adjust.

## Review

For `review`, report: what each key of `.depgraph.yml` does, settings that match nothing or repeat
defaults, generated or vendored code that is scanned, entry points reported as unused, and whether
the hooks and the gate are wired. Edit nothing.
