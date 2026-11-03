# depgraph

[English](README.md) | 日本語

AIが書いたコードの依存関係を Neo4j に入れて可視化・ルール検査し、結果を `CLAUDE.md` に自動反映するツール。
バイブコーディング中にファイルが書き換わるたびにグラフを更新し、「循環依存が増えた」「レイヤー違反が入った」「同名関数が複製された」を即座に検出して次のプロンプトに戻します。

対応言語（tree-sitter）: Go / TypeScript / JavaScript / Python

構文解析には pure Go の tree-sitter 実装 [gotreesitter](https://github.com/odvcencio/gotreesitter) を使うため、C コンパイラは不要です。`CGO_ENABLED=0` でビルドでき、`go build` だけで任意の `GOOS`/`GOARCH` にクロスビルドできます。
ビルドには `mise.toml` の `GOFLAGS` のタグ（`gotreesitter_no_copyleft` と `grammar_subset_<言語>`）を使い、gotreesitter が同梱する GPL/MPL の文法をバイナリに含めません。`mise run check:no-copyleft` で検査します。リリースビルドも同じタグを使ってください。

## Quick start

```bash
# 1. Neo4j を起動（neo4j / depgraph123）
docker compose up -d

# 2. ビルド
mise run build   # または mise exec -- go build -o bin/depgraph .（mise.toml の env が付く）

# 3. 対象リポジトリを監視
./bin/depgraph watch --root /path/to/your/repo
```

初回フルスキャン後、ファイル変更を監視して差分だけ再解析します。
同時に http://127.0.0.1:7777 でブラウザ UI が立ち上がり、グラフと違反一覧がライブ更新されます（SSE）。
検出結果は標準エラーに出力され、`/path/to/your/repo/CLAUDE.md` の
`<!-- depgraph:start -->` 〜 `<!-- depgraph:end -->` 区間が更新されます（手書き部分は保持）。

Neo4j Browser: http://localhost:7474

自動生成のサンプルリポジトリで試すときは `mise run demo` を使います。同じ Neo4j の別プロジェクト
`depgraph-demo` として監視します（UI http://127.0.0.1:7778）。`--reset` で消えるのはそのプロジェクトだけなので、
他のプロジェクトのデータには触れません。消すときは `bin/depgraph projects rm depgraph-demo` です。

## Projects

1 つの Neo4j DB で複数のリポジトリを管理します。スキャン結果はすべて **プロジェクト** に属し、
クエリ・`--reset`・UI はそのプロジェクトに限定されます。プロジェクト key の決定順:

1. `--project <key>`
2. `<root>/.depgraph.yml` の `project: <key>`
3. `--root` のディレクトリ名

起動場所やマシンが変わっても同じリポジトリは同じ key になります。git の linked worktree では
key が `<base>@<worktree 名>` になり（base の既定はメイン worktree のディレクトリ名）、worktree 同士が上書きし合いません。

`(:Project)` ノードに最後の root と正規化した `remote.origin.url` を記録します。同じ key に別の root が来たとき、
worktree 名が一致し、かつ origin が一致するか前の root がもう存在しなければ **移動** とみなして root を更新します。
それ以外は衝突としてエラーにし、`--project` か `.depgraph.yml` で名前を分けるよう案内します。

```bash
depgraph projects list               # key, base, worktree, root, 最終スキャン, ファイル数
depgraph projects rm <key>           # プロジェクトとそのデータを全削除
depgraph projects prune [--dry-run]  # root が存在しなくなったプロジェクトを削除
```

## Commands

| command | 説明 |
|---|---|
| `scan --root DIR [--reset]` | フルスキャン → ルール評価 → CLAUDE.md 更新 |
| `watch --root DIR [--no-lsp] [--no-ui]` | scan 後、fsnotify で監視して差分更新（tree-sitter → LSP で CALLS/REFERENCES を追加） |
| `check --root DIR` | 投入済みグラフに対してルール評価だけ実行（CLAUDE.md 更新） |
| `serve --root DIR [--addr 127.0.0.1:7777]` | 投入済みグラフをブラウザ UI で表示（監視なし、CLAUDE.md は更新しない） |
| `parse --root DIR [files...]` | Neo4j なしで tree-sitter の解析結果を JSON（1 行 1 ファイル）で出力（デバッグ用） |
| `hook <session-start\|prompt>` | Claude Code の hook。Findings を追加文脈として出力（読むだけ、常に exit 0） |
| `projects list` / `projects rm <key>` / `projects prune [--dry-run]` | DB 内のプロジェクトの一覧・削除 |

フラグ（`depgraph <command> --help` で一覧）:

| flag | 対象 | 既定値 |
|---|---|---|
| `--root` | 全コマンド | `.` |
| `--project` | 全コマンド | `<root>/.depgraph.yml` の `project`、無ければ `--root` のディレクトリ名（linked worktree では `@<worktree 名>` を付加） |
| `--exclude` | 全コマンド | なし（カンマ区切りの除外パターン） |
| `--ignore-file` | 全コマンド | `.depgraphignore`（`--root` 基準、空で無効） |
| `--neo4j` / `--user` / `--pass` / `--db` | scan / check / watch / serve / hook / projects | `$NEO4J_URI` / `$NEO4J_USER` / `$NEO4J_PASSWORD` / `$NEO4J_DATABASE`（未設定時 `bolt://localhost:7687` / `neo4j` / `depgraph123` / `neo4j`） |
| `--claude-md` | scan / check / watch | `$DEPGRAPH_CLAUDE_MD` または `CLAUDE.md`（`--root` 基準、空で無効） |
| `--reset` | scan / watch | false（スキャン前にこのプロジェクトのデータを消去。他のプロジェクトは残る） |
| `--workers` | parse / scan / watch | `0`（フルスキャンで並列に解析するファイル数。0 は GOMAXPROCS。出力順はこの値に依存しない） |
| `--addr` | watch / serve | `$DEPGRAPH_ADDR` または `127.0.0.1:7777` |
| `--debounce` | watch | `400ms` |
| `--no-ui` / `--no-lsp` | watch | false |
| `--timeout` / `--debug` | hook | `3s` / false |

## Claude Code hooks

`depgraph hook <session-start|prompt>` は Neo4j に入っている解析結果を Claude Code の
文脈へ戻す（読むだけ。スキャンも CLAUDE.md の更新もしない）。

- `session-start`：統計と Findings の要約（severity 順、error を先に）。件数の多いルールは
  件数と代表例数件に圧縮し、全体を約 4,000 文字に収める（`duplicate-symbol` が 50 件あっても溢れない）
- `prompt`：同じセッションで前回注入してから新規・解消した Findings だけ。変化が無ければ何も出さない。
  前回状態は `session_id` ごとに `<root>/.claude/.depgraph-hook-state.json` に保存（`.gitignore` に追加すること）
- root は `--root`、無ければ `$CLAUDE_PROJECT_DIR`、無ければ hook 入力の `cwd`
- プロジェクトは `scan` と同じ規則（`--project`・`.depgraph.yml`・ディレクトリ名・`@<worktree>`）で決め、
  そのプロジェクトの Findings だけを注入する
- プロンプトは絶対に止めない：Neo4j に繋がらない、root のプロジェクトが未スキャン（または key が別の root のもの）、
  `--timeout`（既定 `3s`）切れのときは何も出さず exit 0（理由は `--debug` で stderr に出る）。
  出力は `hookSpecificOutput.additionalContext` の JSON

他のリポジトリで使うには `depgraph watch --root <repo>` を動かしたまま、そのリポジトリの
`.claude/settings.json` に次を追加する（このリポジトリは同じ形で `bin/depgraph` を呼んでいる）:

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

Neo4j の接続先は `--neo4j` 等か `$NEO4J_URI` 等（既定 `bolt://localhost:7687`）。

## Browser UI

- パッケージ = ノード、import = 矢印。ELK（layered・直角エッジ、Web Worker で計算）で左→右に配置。循環・レイヤー違反に関わるノードと辺は赤、god package は橙
- ディレクトリは 1 ノードに折りたたみ。クリックで中に入り（パンくず / Esc で戻る）、Shift+クリックでその場に枠として展開
- ノードや枠はドラッグで移動でき（重なった要素は押し出す）、位置は `layout` を押すまで表示ごとに保持。右下のミニマップで全体を表示
- 右の finding をクリックすると関係するノードだけハイライト
- ノードをクリックするとファイル一覧・シンボル・import 元/先を表示
- `external` チェックで外部依存（`ext:*`）の表示切替、検索ボックスでフィルタ
- `watch` 中はファイル保存のたびに自動で再描画。ノード集合が変わらない限りレイアウトは保持
- `web/`（Vite + TypeScript、Cytoscape + elkjs）を `pkg/interfaces/handler/ui/dist/` にビルドしてバイナリに埋め込み（`mise run build` が先に `web:build` を実行）。CDN・Web フォントは使わず、依存はすべて `web/pnpm-lock.yaml` で固定。`mise run web:check-no-cdn` でビルド成果物が URL から何も読み込まないことを検査

API: `GET /api/graph`（パッケージ・辺・findings・stats）、`GET /api/package?key=...`、`GET /events`（SSE）

## Graph schema

```
(Project {key, name, root, origin, base, worktree, lastScanAt})
(Package {project, key, name, external})
(File    {project, path, language, lines, updatedAt})
(Symbol  {project, name, kind, exported, line, endLine, language, file})

(Package)-[:CONTAINS]->(File)-[:DECLARES]->(Symbol)
(File)-[:IMPORTS {spec}]->(Package)
(Package)-[:IMPORTS]->(Package)        # file-level から再計算
(Symbol)-[:CALLS|REFERENCES]->(Symbol) # watch 中に LSP が変更ファイルについて追加
```

`project` はプロジェクト key。Package は `(project, key)`、File は `(project, path)` で一意です。
`Package.key` はリポジトリルートからのディレクトリパス（ルートは `.`）。
リポジトリ外への import は `ext:<spec>` という key の `external=true` な Package になります（これもプロジェクトごと）。

## Rules

| rule | severity | 内容 |
|---|---|---|
| `import-cycle` | error | パッケージ間の循環 import |
| `layer-violation` | error | `handler → repository` などの禁止ペア（`rules.DefaultConfig` で変更） |
| `god-package` | warn | 10 パッケージ以上から import されている |
| `duplicate-symbol` | warn | 同名の exported function/class が複数ファイルに存在 |

手で叩くなら:

```cypher
// 循環依存
MATCH p=(a:Package {project: 'my-repo'})-[:IMPORTS*2..8]->(a) WHERE a.external=false
RETURN [n IN nodes(p) | n.key] AS cycle LIMIT 20;

// 扇入れ上位
MATCH (p:Package {project: 'my-repo'})<-[:IMPORTS]-(q) WHERE p.external=false
RETURN p.key, count(q) AS fanIn ORDER BY fanIn DESC LIMIT 10;
```

## Architecture

```
main.go                              エントリポイント（cmd.Execute() を呼ぶだけ）
pkg/domain/graph/                    言語非依存のグラフモデル、Repository / Resolver interface
pkg/domain/rules/                    Finding / Config / 差分、Repository interface
pkg/domain/project/                  Project、key 決定・移動/衝突判定、Repository interface
pkg/services/analysis/               スキャン・差分適用・ルール評価・LSP 精密化のサービス
pkg/infrastructure/repository/graph/ Neo4j 投入・クエリ
pkg/infrastructure/repository/rules/ Cypher ルール評価
pkg/infrastructure/repository/project/ Project ノード
pkg/infrastructure/workspace/        root の .depgraph.yml と git（origin・worktree）
pkg/infrastructure/resolver/         リポジトリ走査 + 除外パターン
  treesitter/                        デフォルト: 構造解析（import / 宣言、gotreesitter）
  lsp/                               言語サーバー（callHierarchy / references）による精密化
pkg/infrastructure/watch/            fsnotify + デバウンス
pkg/infrastructure/claudemd/         CLAUDE.md の管理区間更新
pkg/interfaces/cmd/                  CLI（cobra: parse / scan / check / watch / serve / hook / projects）
pkg/interfaces/handler/ui/           HTTP サーバー + SSE + 埋め込み UI（web/ のビルド成果物）
web/                                 ブラウザ UI のソース（Vite + TypeScript、Cytoscape + elkjs）
pkg/interfaces/handler/hook/         Claude Code hook（要約・差分・状態ファイル）
```

解析バックエンドは `graph.Resolver` で差し替え可能。優先順は treesitter → lsp → scip。

## Roadmap

- [x] LSP 連携（watch のみ）: 変更ファイルのシンボルに `textDocument/references` / `callHierarchy` を問い合わせ、`CALLS` エッジを追加
- [ ] `Commit` ノードで時系列分析（AIセッションごとの依存増加量）
- [ ] SCIP インデックス取り込み（CI 向けバッチ精密解析）
- [x] 1 DB で複数プロジェクト（`--project`、`.depgraph.yml` の `project:`、`depgraph projects`）
- [ ] `.depgraph.yml` でのルール設定（今は `project:` のみ読む）
- [ ] MCP サーバーモード（Claude Code / Cursor から直接問い合わせ）

## License

Apache-2.0（予定）。Neo4j 本体は同梱しません。ユーザー自身の Neo4j に接続するツールです。
