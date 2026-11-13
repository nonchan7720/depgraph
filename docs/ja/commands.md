# コマンド

[English](../commands.md) | 日本語 · [README](../../README_ja.md)

| command | 説明 |
|---|---|
| `scan --root DIR [--reset] [--fail-on warn]` | フルスキャン → ルール評価 → CLAUDE.md 更新（`--fail-on` を付けると Findings があれば exit 1。[CI](#ci) を参照） |
| `watch --root DIR [--no-lsp] [--no-ui]` | scan 後、fsnotify で監視して差分更新（tree-sitter → LSP で CALLS/REFERENCES を追加） |
| `check --root DIR [--fail-on warn]` | 投入済みグラフに対してルール評価だけ実行（CLAUDE.md 更新） |
| `serve --root DIR [--addr 127.0.0.1:7777]` | 投入済みグラフをブラウザ UI で表示（監視なし、CLAUDE.md は更新しない） |
| `mcp --root DIR` | ブラウザ UI を [MCP App](ui.md#mcp-app) として表示する stdio の MCP サーバー（投入済みグラフを読む、CLAUDE.md は更新しない） |
| `init --root DIR [--dry-run]` | Python のファイルがあり、バージョンを固定するファイルを持つディレクトリごとに、`.depgraph.yml` へ `languages` 設定を追加する（ファイル同士が食い違う場合は端末なら尋ねる。[フレームワーク](configuration.md#フレームワークレイヤーに外部パッケージ)参照） |
| `parse --root DIR [files...]` | Neo4j なしで tree-sitter の解析結果を JSON（1 行 1 ファイル）で出力（デバッグ用） |
| `hook <session-start\|prompt>` | Claude Code / Codex の hook。Findings を追加文脈として出力（読むだけ、常に exit 0） |
| `codex install-hooks [--repo\|--to FILE] [--print] [--remove]` | プラグインの Codex 用 hook を Codex の `hooks.json` に写す（[Codex プラグイン](agents.md#codex-プラグイン) を参照） |
| `gate [files...] [--fail-on warn]` | 品質ゲートのチェック（[claude-hook-gate](agents.md#品質ゲートclaude-hook-gate)）。変更ファイルを反映し、セッションで増えた Findings があれば exit 1 |
| `projects list` / `projects rm <key>` / `projects prune [--dry-run]` | DB 内のプロジェクトの一覧・削除 |
| `store ls` / `store down` / `store rm [<key>...] [--all]` | depgraph が管理する Neo4j コンテナとデータボリュームの一覧・停止・削除（container プロバイダー） |
| `schema` | `.depgraph.yml` の JSON Schema を出力（[設定](configuration.md) 参照） |
| `version` / `--version` | バージョンを表示（`version` はコミット・ビルド日時・Go ツールチェーンも表示） |

フラグ（`depgraph <command> --help` で一覧）:

| flag | 対象 | 既定値 |
|---|---|---|
| `--root` | 全コマンド | `.` |
| `--project` | 全コマンド | `<root>/.depgraph.yml` の `project`、無ければ `--root` のディレクトリ名（linked worktree では `@<worktree 名>` を付加） |
| `--exclude` | 全コマンド | なし（カンマ区切りの除外パターン） |
| `--ignore-file` | 全コマンド | `.depgraphignore`（`--root` 基準、空で無効） |
| `--neo4j` / `--user` / `--pass` / `--db` | scan / check / watch / serve / mcp / hook / gate / projects | 外部 Neo4j: `$NEO4J_URI` / `$NEO4J_USER` / `$NEO4J_PASSWORD` / `$NEO4J_DATABASE`（URI 未設定時は depgraph が Neo4j を用意。ユーザー / パスワード / DB の未設定時 `neo4j` / `depgraph123` / `neo4j`） |
| `--neo4j-provider` | scan / check / watch / serve / mcp / hook / gate / projects | `$DEPGRAPH_NEO4J_PROVIDER`、なければ `store.provider`、なければ URI があれば external、なければ container |
| `--container-cli` | scan / check / watch / serve / mcp / hook / gate / projects / store | `$DEPGRAPH_CONTAINER_CLI`、なければ `store.containerCli`、なければ docker / podman / nerdctl で最初に応答したもの |
| `--persist` | scan / check / watch / serve / mcp | `store.persist`、なければ false（エフェメラル） |
| `--claude-md` | scan / check / watch | `$DEPGRAPH_CLAUDE_MD` または `CLAUDE.md`（`--root` 基準、空で無効） |
| `--reset` | scan / watch | false（スキャン前にこのプロジェクトのデータを消去。他のプロジェクトは残る） |
| `--workers` | parse / scan / watch | `0`（フルスキャンで並列に解析するファイル数。0 は GOMAXPROCS。出力順はこの値に依存しない） |
| `--addr` | watch / serve | `$DEPGRAPH_ADDR` または `127.0.0.1:7777` |
| `--debounce` | watch | `400ms` |
| `--no-ui` / `--no-lsp` | watch | false |
| `--timeout` / `--debug` / `--format` | hook | `3s` / false / `json`（`text`: 文脈そのものを出力。claude-hook-gate の `session_start` 用） |
| `--fail-on` | scan / check | なし（`error` / `warn` / `info` 以上の Findings があれば exit 1） |
| `--fail-on` / `--timeout` | gate | `warn` / `60s` |
| `--plugin-root` / `--to` / `--repo` / `--print` / `--remove` | codex install-hooks | `$PLUGIN_ROOT`、`$CLAUDE_PLUGIN_ROOT` か `.` / `$CODEX_HOME/hooks.json` / false / false / false |

## パスの除外: `.depgraphignore`

1 行 1 パターン、`#` でコメント。`/` を含まないパターンはあらゆる深さのファイル名・ディレクトリ名に一致します
（`*.gen.go`、`tmp*`）。`/` を含むとルートからのパスです（`internal/ent`、`internal/**/testdata`）。先頭の `/` はルート
に固定します。除外されたパスはスキャンされません。

次のディレクトリ名は、どの深さでも常にスキップされます: `.git`、`node_modules`、`vendor`、`dist`、`build`、`.venv`、
`venv`、`__pycache__`、`target`、`.next`、`.idea`、`.vscode`、`coverage`、`.cache`、`.worktrees`、`.claude`。
ソースがこれらの中にある場合（`src/build/startup/` など）は、`.gitignore` と同じ `!` パターンで解除します。

```
!src/build     # このディレクトリだけ（src/build/node_modules はスキップのまま）
!build         # build という名前のすべてのディレクトリ
!/build        # ルートの build だけ
*.gen.go
!keep.gen.go   # 先のパターンで除外したファイルを戻す
```

パターンは順に適用され、最後に一致したものが勝ちます。組み込みの名前は先頭にあるものとして扱います。`!` パターンは
そのパス自身に一致し、その配下には一致しません。`.gitignore` と同様に、除外されたディレクトリの中は戻せません。
`\!name` は先頭が `!` の名前そのものに一致します。

## CI

`scan` と `check` は、何が見つかっても exit 0 です。`--fail-on <error|warn|info>` を付けると、その重大度以上の
Findings があれば exit 1 になります（増えた分だけでなくすべて。セッションで増えた分を見るのは gate です）。
Docker のあるランナーなら、container プロバイダーで CI のジョブにできます:

```bash
depgraph scan --root . --claude-md "" --fail-on warn   # --claude-md "": CLAUDE.md を書き換えない
```

`--fail-on error` にすると、循環 import とレイヤー違反でだけ失敗します。
