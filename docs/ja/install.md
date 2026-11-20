# インストール

[English](../install.md) | 日本語 · [README](../../README_ja.md)

**GitHub Releases（推奨・唯一の配布形態）。** [Releases](https://github.com/nonchan7720/depgraph/releases) から
OS / CPU に合うアーカイブ `depgraph_<os>_<arch>.tar.gz`（Windows は `.zip`。linux / darwin / windows × amd64 / arm64）を
ダウンロードし、`checksums.txt` で検証してから `depgraph` を `PATH` に置きます。各アーカイブには
`LICENSE`、`NOTICE`、`THIRD_PARTY_NOTICES.md` も入っています。

```bash
sha256sum --ignore-missing -c checksums.txt   # macOS: shasum -a 256 --ignore-missing -c checksums.txt
tar xzf depgraph_linux_amd64.tar.gz && install depgraph ~/.local/bin/
depgraph --version
```

`v<version>-dev.<commit>` という名前のリリースは、`main` への push ごとに作られるプレリリースです。

**ソースから。** リポジトリを clone し、`mise.toml` で固定したツールチェーン（Go / node / pnpm）でビルドします
（`web/` のブラウザ UI もビルドしてバイナリに埋め込みます）。

```bash
git clone https://github.com/nonchan7720/depgraph.git && cd depgraph
mise install && mise run build   # bin/depgraph ができる
```

構文解析には pure Go の tree-sitter 実装 [gotreesitter](https://github.com/odvcencio/gotreesitter) を使うため、C コンパイラは不要です。`CGO_ENABLED=0` でビルドでき、`go build` だけで任意の `GOOS`/`GOARCH` にクロスビルドできます。
ビルドには `mise.toml` の `GOFLAGS` のタグ（`gotreesitter_no_copyleft` と `grammar_subset_<言語>`）を使い、gotreesitter が同梱する GPL/MPL の文法をバイナリに含めません。`mise run check:no-copyleft` で検査します。リリースビルド（goreleaser）も同じ `GOFLAGS` を使い、各バイナリに同じ検査をかけます。
