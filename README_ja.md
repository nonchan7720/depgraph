# depgraph

[English](README.md) | 日本語

depgraph は、**AI が書いたコードを本番に出せる状態にする**ためのツールです。コーディングエージェントが
残していく負債のうち、プロジェクト自身のコンパイラ・型チェッカー・linter が報告しないものを見つけ、
エージェントがまだ作業している間に戻します。

リポジトリの依存グラフを Neo4j に入れ、ファイルが書き換わるたびにアーキテクチャのルールを評価し、結果を
エージェントが読む場所に置きます。次のプロンプト、`CLAUDE.md`、そして「負債を残したままでは作業を
終えられない」品質ゲートです。

## 何を見つけるか

| 負債 | ルール |
|---|---|
| **誰も使っていないコード**：どのエントリポイントからも届かない export・ファイル・パッケージ | `unused-export`、`unreachable-file`、`unreachable-package` |
| **重複コード**：同じロジックの再実装（名前が違っていても） | `duplicate-code`、`duplicate-symbol` |
| **循環**：パッケージ・モジュール・ファイル・関数の間の循環 | `import-cycle`、`module-cycle`、`file-cycle`、`function-cycle` |
| **壊れたレイヤー**：禁止された import、インターフェースではなく実装を保持、API から漏れる型 | `layer-violation`、`concrete-dependency`、`type-leak` |
| **劣化していく構造**：すべてが依存するハブ、複数の責務を持つ単位、知りすぎている関数、何も足さないラッパー | `god-package`、`low-cohesion`、`wide-function`、`pass-through` |

コンパイラや linter が既に報告するもの（解決できない import、型エラー、未使用のローカル変数）は、それらに
任せます。事実でない検出結果はバグとして扱います。depgraph はコードに書かれていることを読み、推測しません。
各ルールの説明は[ルール](docs/ja/rules.md)にあります。

## Quick start

必要なものは、`depgraph` のバイナリ（[インストール](docs/ja/install.md)）とコンテナランタイム（Docker・Podman・
nerdctl のいずれか）だけです。compose ファイルも Neo4j のインストールも要りません。

```bash
depgraph watch --root /path/to/your/repo
```

これで次のことが起きます。

1. Neo4j をコンテナで起動する（10〜20 秒。約 1 GB のイメージの取得は最初の 1 回だけ）
2. リポジトリをスキャンし、検出結果を出力する
3. 変更を監視し、変わったファイルだけを再解析する
4. http://127.0.0.1:7777 でブラウザ UI を提供する（ライブ更新）
5. リポジトリの `CLAUDE.md` の `<!-- depgraph:start -->` 〜 `<!-- depgraph:end -->` 区間を最新に保つ
   （手書きの部分は残します）

`watch` が終了すると、終了の仕方を問わず、コンテナはデータごと削除されます。データを残すには `--persist` を
付けます（[Neo4j ストア](docs/ja/neo4j.md)）。

CI などで 1 回だけ実行する場合:

```bash
depgraph scan --root . --claude-md "" --fail-on warn   # warn 以上の検出結果があれば exit 1
```

## コーディングエージェントと一緒に使う

`depgraph watch` を動かしたまま、次のものを使います。

| | エージェントに届くもの | |
|---|---|---|
| **プラグイン**（Claude Code・Codex） | 下の hook と、`/depgraph:setup`・`/depgraph:fix` | [コーディングエージェント連携](docs/ja/agents.md) |
| **hook** | セッション開始時に検出結果の要約、以後はプロンプトごとに「増えたもの・解消されたもの」だけ | [hook](docs/ja/agents.md#claude-code-hooks) |
| **品質ゲート** | 編集のたびに、**このセッションが**増やした負債。残っている間はエージェントが作業を終えられない | [品質ゲート](docs/ja/agents.md#品質ゲートclaude-hook-gate) |
| **MCP App** | 会話の中に表示されるグラフビュー | [MCP App](docs/ja/ui.md#mcp-app) |

```text
/plugin marketplace add nonchan7720/depgraph
/plugin install depgraph@depgraph
```

## 仕組み

```
 ソースファイル       resolver             グラフストア        ルール             検出結果（Findings）
 Go, TS / JS,   ──►  tree-sitter     ──►  Neo4j         ──►  Cypher        ──►  CLAUDE.md の管理区間
 Python, Ruby        + import の解決       （プロジェクト                         hook / gate   （エージェントへ）
                                           ごとに 1 グラフ）                      ブラウザ UI / MCP App
                                                                                終了コード    （CI）
```

各ファイルを tree-sitter で解析し、import はそのエコシステム自身の方法で解決します。結果はファイル単位で
Neo4j に保存するので、変更があればグラフのうちそのファイルの部分だけを置き換えます。ルールの評価前に、
各ファイルが使っている名前をプロジェクト全体で解決して「リンクグラフ」（関数間の呼び出し、ファイル間の
読み込み、型とその使用箇所など）にし、すべてのルールをそのグラフに対する Cypher クエリとして実行します。
各ステップの詳細、Neo4j を何に使っているか、なぜストアが Neo4j なのかは
[アーキテクチャ](docs/ja/architecture.md)にあります。

## 対応言語

言語ごとに、depgraph がそこから何を読み、ルールがその言語で何を意味するかのページがあります。

| 言語 | |
|---|---|
| Go | [docs/languages/ja/go.md](docs/languages/ja/go.md) |
| TypeScript / JavaScript | [docs/languages/ja/typescript.md](docs/languages/ja/typescript.md) |
| Python | [docs/languages/ja/python.md](docs/languages/ja/python.md) |
| Ruby（Rails・engine・packwerk の pack） | [docs/languages/ja/ruby.md](docs/languages/ja/ruby.md) |

## ドキュメント

| ページ | 内容 |
|---|---|
| [インストール](docs/ja/install.md) | リリースバイナリ、ソースからのビルド |
| [コマンド](docs/ja/commands.md) | 全コマンドとフラグ、`.depgraphignore`、CI |
| [ルール](docs/ja/rules.md) | 全ルール、未使用の判定、複製の見つけ方、関数を対象外にする方法 |
| [設定](docs/ja/configuration.md) | `.depgraph.yml`：レイヤー、禁止 import、severity、フレームワーク、モノレポ |
| [コーディングエージェント連携](docs/ja/agents.md) | Claude Code と Codex のプラグイン、hook、品質ゲート、git worktree |
| [ブラウザ UI と MCP App](docs/ja/ui.md) | グラフビュー（ブラウザ、または会話の中） |
| [Neo4j ストアとプロジェクト](docs/ja/neo4j.md) | Neo4j の用意の仕方（コンテナ / 自前）、永続化、1 つのデータベースに複数リポジトリ |
| [アーキテクチャ](docs/ja/architecture.md) | 各機能の実現方法、グラフスキーマ、使っている Neo4j の機能、なぜ SQLite ではなく Neo4j なのか |
| [開発](docs/ja/development.md) | ロードマップ、リリース手順 |

## License

Apache-2.0（[LICENSE](LICENSE)・[NOTICE](NOTICE) 参照）。Neo4j 本体は同梱しません。ユーザー自身の Neo4j に接続するか、
ユーザーのコンテナランタイムで公式の Neo4j Community イメージ（GPLv3）を実行します。
