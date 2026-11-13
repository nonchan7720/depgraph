# 開発

[English](../development.md) | 日本語 · [README](../../README_ja.md)

depgraph 自体を開発する人向けのページです。設計は[アーキテクチャ](architecture.md)にあります。

## リポジトリでの作業

ツールは `mise.toml` で固定していて、mise 経由で実行します。

```bash
mise install          # Go、node、pnpm、golangci-lint、goreleaser、言語サーバー
mise run neo4j        # テストと `mise run watch` / `scan` が使う docker compose の Neo4j
mise run ci           # vet、lint、テスト、web UI のテストとビルド、ライセンスとリリース設定の検査
```

Pull Request を開く前に `mise run ci` が通っている必要があります。テストには compose の Neo4j が必要です
（`NEO4J_URI` は `mise.toml` で設定しています）。

自動生成のサンプルリポジトリで試すときは `mise run demo` を使います。compose の Neo4j の別プロジェクト
`depgraph-demo` として監視します（UI http://127.0.0.1:7778）。`--reset` で消えるのはそのプロジェクトだけなので、
他のプロジェクトのデータには触れません。消すときは `bin/depgraph projects rm depgraph-demo` です。

## Roadmap

- [x] LSP 連携（watch のみ）: 変更ファイルのシンボルに `textDocument/references` / `callHierarchy` を問い合わせ、`CALLS` エッジを追加
- [ ] `Commit` ノードで時系列分析（AIセッションごとの依存増加量）
- [ ] SCIP インデックス取り込み（CI 向けバッチ精密解析）
- [x] 1 DB で複数プロジェクト（`--project`、`.depgraph.yml` の `project:`、`depgraph projects`）
- [x] `.depgraph.yml` でのルール設定（レイヤー・禁止 import・severity・ignore）
- [x] モノレポ: コンポーネントごとのレイヤー（`components:`、ディレクトリごとの `.depgraph.yml`）
- [x] ブラウザ UI を MCP App として表示する MCP サーバー（`depgraph mcp`）
- [ ] グラフを直接問い合わせる MCP ツール（依存元・未使用コード・重複）を Claude Code / Cursor から
- [x] depgraph 自身がコンテナ（docker / podman / nerdctl）で Neo4j を用意（エフェメラル / 永続）
- [ ] `local` Neo4j プロバイダー: インストール済みの Neo4j（`NEO4J_HOME` か `PATH` の `neo4j`）を子プロセスで起動。データは実行ごとのディレクトリ、ループバックのみ

## リリース手順

リリースは [release-please](https://github.com/googleapis/release-please) と
[GoReleaser](https://goreleaser.com) で自動化しています（`.github/workflows/release.yml`）。

1. [Conventional Commits](https://www.conventionalcommits.org/ja/) 形式のタイトルで PR を `main` にマージする
   （1.0 未満の間は `feat:` / `fix:` / `perf:` → patch、`feat!:` / `BREAKING CHANGE:` → minor）。
   squash マージなので PR タイトルがコミットメッセージになります。
2. release-please が `release-please/manifest.json`、`pkg/version/version.go`、`CHANGELOG.md` を
   更新するリリース PR を開いたまま保ちます。初回リリースは `v0.1.0` です。
3. リリース PR をマージする。release-please が `vX.Y.Z` のタグと GitHub Release を作り、
   GoReleaser が 6 種のアーカイブと `checksums.txt` をビルドして添付します。

それ以外の `main`（および保守ブランチ `release/vN`）への push では、プレリリース
`v<現在のバージョン>-dev.<短い SHA>` を公開します。リリースビルドを手元で試すには
`mise run release:snapshot`（`dist/` に出力、公開はしない）。`mise run ci` は `goreleaser check` を実行します。

ワークフローには、このリポジトリにインストールした GitHub App（Contents / Pull requests / Issues の write）と、
リポジトリ secrets `RELEASE_PLEASE_CLIENT_ID`（App の Client ID）・`RELEASE_PLEASE_APP_PRIVATE_PEM`（秘密鍵）が必要です。
リリース PR で CI が走るよう、`GITHUB_TOKEN` ではなく App のトークンを使います。
