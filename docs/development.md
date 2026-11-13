# Development

English | [日本語](ja/development.md) · [README](../README.md)

For people working on depgraph itself. The design is in [Architecture](architecture.md).

## Working on the repository

Tools are pinned in `mise.toml` and run through mise:

```bash
mise install          # Go, node, pnpm, golangci-lint, goreleaser, the language servers
mise run neo4j        # the docker compose Neo4j the tests and `mise run watch` / `scan` use
mise run ci           # vet, lint, tests, the web UI's tests and build, license and release checks
```

`mise run ci` must pass before a pull request is opened. The tests need the compose Neo4j
(`NEO4J_URI` is set in `mise.toml`).

To try depgraph on a generated sample repository, run `mise run demo`. It watches the sample as
the project `depgraph-demo` in the compose Neo4j (UI http://127.0.0.1:7778); its `--reset` clears
only that project, so other projects are never touched. Remove it with
`bin/depgraph projects rm depgraph-demo`.

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
