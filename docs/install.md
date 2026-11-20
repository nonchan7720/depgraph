# Install

English | [日本語](ja/install.md) · [README](../README.md)

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
