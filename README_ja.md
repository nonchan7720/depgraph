# depgraph

[English](README.md) | 日本語

AIが書いたコードの依存関係を Neo4j に入れて可視化・ルール検査し、結果を `CLAUDE.md` に自動反映するツール。
バイブコーディング中にファイルが書き換わるたびにグラフを更新し、「循環依存が増えた」「レイヤー違反が入った」「同名関数が複製された」を即座に検出して次のプロンプトに戻します。

対応言語（tree-sitter）。言語ごとに、depgraph がそこから何を読み、ルールがその言語で何を意味するかのページがあります:

| 言語 | |
|---|---|
| Go | [docs/languages/ja/go.md](docs/languages/ja/go.md) |
| TypeScript / JavaScript | [docs/languages/ja/typescript.md](docs/languages/ja/typescript.md) |
| Python | [docs/languages/ja/python.md](docs/languages/ja/python.md) |
| Ruby（Rails・engine・packwerk の pack） | [docs/languages/ja/ruby.md](docs/languages/ja/ruby.md) |

## インストール

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

## Quick start

必要なもの: depgraph（Install 参照）とコンテナランタイム（Docker・Podman・nerdctl のいずれか。
[Neo4j](#neo4j) 参照）。compose ファイルも Neo4j のインストールも不要です。

```bash
depgraph watch --root /path/to/your/repo
```

depgraph が Neo4j をコンテナで起動し（起動ごとに初回 10〜20 秒。約 1 GB のイメージの取得は最初の 1 回だけ）、
リポジトリをスキャンしたあとファイル変更を監視して差分だけ再解析します。
同時に http://127.0.0.1:7777 でブラウザ UI が立ち上がり、グラフと違反一覧がライブ更新されます（SSE）。
検出結果は標準エラーに出力され、`/path/to/your/repo/CLAUDE.md` の
`<!-- depgraph:start -->` 〜 `<!-- depgraph:end -->` 区間が更新されます（手書き部分は保持）。
`watch` が終了すると、理由を問わず（クラッシュでも）コンテナはデータごと削除されます。データを残したいときは `--persist` を付けます（[Neo4j](#neo4j) 参照）。

自動生成のサンプルリポジトリで試すときは `mise run demo` を使います。下記の docker compose の Neo4j の別プロジェクト
`depgraph-demo` として監視します（UI http://127.0.0.1:7778）。`--reset` で消えるのはそのプロジェクトだけなので、
他のプロジェクトのデータには触れません。消すときは `bin/depgraph projects rm depgraph-demo` です。

## Neo4j

グラフは Neo4j に入り、ルールは Cypher クエリです。depgraph が Neo4j をどう用意するかは **プロバイダー** で、
次の順に決まります。

1. `--neo4j-provider <name>`、なければ `$DEPGRAPH_NEO4J_PROVIDER`、なければ `.depgraph.yml` の `store.provider`
2. URI が設定されていれば（`--neo4j` か `$NEO4J_URI`）`external`
3. コンテナランタイムが応答すれば `container`
4. `local`（予定: マシンにインストールされた Neo4j を子プロセスとして起動。未実装）

どれも使えないときは、プロバイダーごとに何が足りないかを 1 行で示して止まります。

### `container`（既定）: depgraph が Neo4j を起動・後片付けする

対応するコンテナ CLI: **docker**（Docker Desktop / Engine、OrbStack、Colima、dockerd の Rancher Desktop）、
**podman**（rootless を含む。macOS / Windows では `podman machine` が起動していること）、
**nerdctl**（containerd: containerd の Rancher Desktop、Finch、Lima）。
nerdctl なしの素の containerd（`ctr`）はポートを公開できないため対応しません。
CLI は `--container-cli`（名前かパス）、なければ `$DEPGRAPH_CONTAINER_CLI`、なければ `.depgraph.yml` の
`store.containerCli`、なければ docker・podman・nerdctl の順に `PATH` にあり `<cli> version` に 2 秒以内に応答した最初のものです。
CLI はユーザーの環境変数のまま実行するので `DOCKER_HOST`・docker context・`CONTAINER_HOST` が効きます。
ポートはデーモン側のループバックに公開するため、デーモンはこのマシン上にある必要があります。

実行するコマンド（固定の引数列。シェルは使いません）:

```
<cli> run -d --rm --name depgraph-<project>-<hash> --label depgraph.managed=1 --label depgraph.root=<hash> ...
          -p 127.0.0.1::7687 -e NEO4J_AUTH -e NEO4J_server_memory_heap_max__size=512m ...
          --tmpfs /logs --tmpfs /data docker.io/library/neo4j:5.26.32@sha256:249a1ec6...
```

- イメージは `docker-compose.yml` と CI と同じリリース・同じダイジェストです。
- Bolt は `127.0.0.1` だけにランダムなポートで公開します。パスワードはコンテナごとにランダムで、CLI には
  環境変数で渡し（値なしの `-e NEO4J_AUTH`）、コマンドラインには載せません。各コマンドは root のストアを
  ラベルで探し、ポートとパスワードを `<cli> inspect` で読みます。ディスクには何も書きません。
- メモリ: ヒープ初期 128 MiB / 最大 512 MiB、ページキャッシュ 64 MiB、トランザクションログの事前確保なし。
  実測で待機時 約 330 MiB、このリポジトリのスキャン後 約 480 MiB。
- 起動するコマンド: `watch`・`scan`・`check` は root の稼働中のストアを使い、なければ起動します
  （`watch` なしの `scan` / `check` は自分だけのストアを起動します）。`serve` と `projects` は
  稼働中のもの（と停止中の永続ストア）だけを使います。コンテナを起動したプロセスが終了時にそれを止め、
  他のプロセスが起動したストアはそのプロセスに任せます。Claude Code の hook は稼働中のものを探すだけで、
  何も起動せず、なければ何も出力しません。

**コンテナを起動したプロセスが、どう終わっても必ずそれを止めます。** 通常終了、ctrl-c / SIGTERM、
どの goroutine での panic、`os.Exit`、SIGKILL、端末を閉じた場合のいずれでもです。depgraph は `run` / `start` の直後に
自分自身を小さな watchdog 子プロセスとして起動します（`depgraph __container-watchdog --cli <cli> --id <container> --mode rm|stop`。
固定の引数列でシェルは使いません）。子プロセスは独自のセッション / プロセスグループで動き、SIGINT / SIGTERM / SIGHUP を無視して
親とつながるパイプを待ちます。パイプは親がどんな理由で終わっても閉じるので、そのときにそのコンテナだけを停止または削除して終了します。
通常終了では先に後片付けが済むので、watchdog には何も残っていません。リーパーコンテナもネットワークポートも使いません。
最後の手段として（電源断など）、同じマシンで次に `watch` / `scan` / `check` を実行したときに、所有プロセスがいない
エフェメラルな残骸を削除します（所有 PID とホスト ID のラベル）。触れるのは `depgraph.managed=1` のラベルがあるものだけです。

**エフェメラル（既定）。** データはコンテナ内の tmpfs にあり、停止するとデータごと削除されます。

**永続（オプトイン）。** `--persist`（scan / check / watch / serve）か `.depgraph.yml`:

```yaml
store:
  persist: true          # データをプロジェクト key ごとの名前付きボリュームに残す
  # provider: container  # external | container
  # containerCli: podman # docker | podman | nerdctl | パス
```

残るのは **データ** で、プロセスではありません。データはボリューム `depgraph-<project>-<hash>` に残ります。
コンテナ（`--rm` なし）は起動したプロセスの終了時に停止し、次の `watch` / `scan` / `check` が同じコンテナを再び起動します
（同じパスワード。約 5 秒で、ストアは作り直しません）。`serve` と `projects` も停止中の永続コンテナを起動します。
hook が使えるのは `watch` の実行中です。
永続データが黙って溜まることはありません:

```bash
depgraph store ls                     # depgraph が管理するコンテナとボリュームをすべて表示
depgraph store down [--root DIR]      # root のストアを止める（エフェメラル: 削除、永続: 停止してデータは保持）
depgraph store rm [--root DIR]        # root のプロジェクトのコンテナとデータボリュームを削除
depgraph store rm <project-key>...    # 指定したプロジェクトの分を削除
depgraph store rm --all               # すべてのプロジェクトの分を削除
```

永続ボリュームのコンテナを depgraph 以外で削除するとパスワードが失われます。その場合 depgraph は
`depgraph store rm` でボリュームを消すよう案内します（次のスキャンでグラフは作り直せます）。

### `external`: 自分で用意した Neo4j（docker compose、サーバー、Aura）

`--neo4j bolt://host:7687`（と `--user`・`--pass`・`--db`）か `NEO4J_URI`（と `NEO4J_USER`・`NEO4J_PASSWORD`・
`NEO4J_DATABASE`）を設定します。depgraph は接続するだけで、起動・停止・削除は一切しません。
このリポジトリの `docker-compose.yml` がその例です:

```bash
docker compose up -d                  # bolt://localhost:7687 の Neo4j（neo4j / depgraph123）、ブラウザ http://localhost:7474
NEO4J_URI=bolt://localhost:7687 depgraph watch --root /path/to/your/repo
```

このリポジトリでは `mise.toml` が `NEO4J_URI`（と compose のユーザー / パスワード）を設定するので、
`mise run watch` / `scan` などとテストは compose の Neo4j を使います（`mise run neo4j` で起動）。
mise のシェルからコンテナプロバイダーを使うときは `mise run watch:container` か `--neo4j-provider container` を使います。

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
| `scan --root DIR [--reset] [--fail-on warn]` | フルスキャン → ルール評価 → CLAUDE.md 更新（`--fail-on` を付けると Findings があれば exit 1。[CI](#ci) を参照） |
| `watch --root DIR [--no-lsp] [--no-ui]` | scan 後、fsnotify で監視して差分更新（tree-sitter → LSP で CALLS/REFERENCES を追加） |
| `check --root DIR [--fail-on warn]` | 投入済みグラフに対してルール評価だけ実行（CLAUDE.md 更新） |
| `serve --root DIR [--addr 127.0.0.1:7777]` | 投入済みグラフをブラウザ UI で表示（監視なし、CLAUDE.md は更新しない） |
| `mcp --root DIR` | ブラウザ UI を [MCP App](#mcp-app) として表示する stdio の MCP サーバー（投入済みグラフを読む、CLAUDE.md は更新しない） |
| `init --root DIR [--dry-run]` | Python のファイルがあり、バージョンを固定するファイルを持つディレクトリごとに、`.depgraph.yml` へ `languages` 設定を追加する（ファイル同士が食い違う場合は端末なら尋ねる。[フレームワーク](#フレームワークレイヤーに外部パッケージ)参照） |
| `parse --root DIR [files...]` | Neo4j なしで tree-sitter の解析結果を JSON（1 行 1 ファイル）で出力（デバッグ用） |
| `hook <session-start\|prompt>` | Claude Code / Codex の hook。Findings を追加文脈として出力（読むだけ、常に exit 0） |
| `codex install-hooks [--repo\|--to FILE] [--print] [--remove]` | プラグインの Codex 用 hook を Codex の `hooks.json` に写す（[Codex プラグイン](#codex-プラグイン) を参照） |
| `gate [files...] [--fail-on warn]` | 品質ゲートのチェック（[claude-hook-gate](#品質ゲートclaude-hook-gate)）。変更ファイルを反映し、セッションで増えた Findings があれば exit 1 |
| `projects list` / `projects rm <key>` / `projects prune [--dry-run]` | DB 内のプロジェクトの一覧・削除 |
| `store ls` / `store down` / `store rm [<key>...] [--all]` | depgraph が管理する Neo4j コンテナとデータボリュームの一覧・停止・削除（container プロバイダー） |
| `schema` | `.depgraph.yml` の JSON Schema を出力（[設定](#設定depgraphyml) 参照） |
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

### CI

`scan` と `check` は、何が見つかっても exit 0 です。`--fail-on <error|warn|info>` を付けると、その重大度以上の
Findings があれば exit 1 になります（増えた分だけでなくすべて。セッションで増えた分を見るのは gate です）。
Docker のあるランナーなら、container プロバイダーで CI のジョブにできます:

```bash
depgraph scan --root . --claude-md "" --fail-on warn   # --claude-md "": CLAUDE.md を書き換えない
```

`--fail-on error` にすると、循環 import とレイヤー違反でだけ失敗します。

## Claude Code プラグイン

このリポジトリは Claude Code のプラグインマーケットプレイスでもあります。`depgraph` プラグインは
[hook](#claude-code-hooks) を設定し、スキルを 2 つ追加します:

```text
/plugin marketplace add nonchan7720/depgraph
/plugin install depgraph@depgraph
```

| 構成 | 内容 |
|---|---|
| hook | セッション開始時とプロンプトごとに `depgraph hook session-start` / `prompt`（下記）。depgraph のバイナリが無いとき（後述）、プロジェクトの `.claude/settings.json` が自前で `depgraph hook` を実行しているとき、（セッション開始のみ）claude-hook-gate の `session_start` が実行しているときは何もしないので、文脈が二重に入りません |
| `/depgraph:setup [init\|update\|review]` | `.depgraph.yml`（コードから読み取れるレイヤー、エントリポイント、ルールのオプション）と `.depgraphignore`、claude-hook-gate を使っていれば `.claude/gate.yaml` の `depgraph gate` ルールと `session_start` を書きます |
| `/depgraph:fix [rule]` | Findings をルールで黙らせるのではなく、コードに合った形で直します（逆向きの import を移す、作り直さず再利用する、未使用コードはつなぐか消す） |

プラグインは claude-hook-gate と同じように、自分のバージョン（作られたリリース）のバイナリをインストールします。
セッション開始の hook が GitHub リリース `v<version>` から `depgraph_<os>_<arch>.tar.gz` と `checksums.txt` を取得し、
sha256 を照合してから `depgraph` を `${XDG_CACHE_HOME:-~/.cache}/depgraph/<version>/` に展開します（バージョンごとに 1 回。
30 日使われていないバージョンは消します）。プロンプトの hook は既にあるバイナリを使うだけです。スキルは同じ方法でインストールする
ラッパー `plugin/scripts/depgraph` から実行します。使うバイナリは次の順です:

1. `$DEPGRAPH_BIN`（開発用。例: チェックアウトの `bin/depgraph`）
2. キャッシュ済みのバイナリ（`$DEPGRAPH_CACHE_DIR` でキャッシュの場所を変えられます）
3. リリースからのダウンロード（linux / darwin × amd64 / arm64。`curl` か `wget`、`sha256sum` か `shasum` が必要。
   チェックサムを照合できなければ何も入れません）
4. `PATH` 上の `depgraph`（オフライン、Windows）

どれも使えなければ hook は何もせず、理由を標準エラーに 1 行出します。`depgraph watch --root <repo>` はターミナルで
動かしておいてください（`PATH` 上の `depgraph` が必要です。[インストール](#インストール) を参照）。下記の claude-hook-gate の
設定も同様です。

## Codex プラグイン

同じ `plugin/` ディレクトリは [Codex](https://developers.openai.com/codex) のプラグインでもあり、
hook とスキルも同じです（`.agents/plugins/marketplace.json` に登録）。マーケットプレイスを追加し、
Codex の `/plugins` から `depgraph` をインストールします:

```text
codex plugin marketplace add nonchan7720/depgraph
```

Codex の build によっては、プラグインに同梱された hook を実行しません（プラグインは入りますが、
解析結果がセッションに届きません）。一度だけ hook を `~/.codex/hooks.json` に写してください。
このコマンドは先にプラグインも更新します（`codex plugin marketplace upgrade depgraph`、
`codex plugin add depgraph@depgraph`）:

```text
curl -fsSL https://raw.githubusercontent.com/nonchan7720/depgraph/main/plugin/codex/setup.sh | bash
```

そのあと Codex で `/hooks` を開き、hook を信頼（trust）してください。信頼していない hook を Codex は
実行しません。信頼はハッシュで記録されるため、プラグインを更新したらこのコマンドをもう一度実行し
（写しはバージョンのディレクトリを指しています）、改めて信頼してください。

| 部品 | 内容 |
|---|---|
| `plugin/codex/hooks.json` | hook 本体: `scripts/hook.sh session-start codex` / `prompt codex`。Claude Code と同じ `depgraph hook` を実行します（Codex も同じ `hookSpecificOutput.additionalContext` を読みます） |
| `plugin/codex/setup.sh` | 上のコマンド: プラグインを更新し（`--no-update` で省略。`DEPGRAPH_CODEX_BIN` で `codex` を指定）、`${CODEX_HOME:-~/.codex}/plugins/cache/depgraph/depgraph/` から最新のバージョンを探して（`DEPGRAPH_PLUGIN_DIR` で別のプラグインディレクトリを指定）、残りの引数を付けてその `install-hooks.sh` を実行します（`... \| bash -s -- --repo`）。管理された hook のみのポリシーの下か `--system` 付きでは、代わりに `/etc/codex/hooks.json` に書きます（下記） |
| `plugin/codex/install-hooks.sh` | このプラグインのバイナリで `depgraph codex install-hooks` を実行: hook を `$CODEX_HOME/hooks.json`（既定）、`<repo>/.codex/hooks.json`（`--repo [--root <dir>]`。Codex が信頼したプロジェクトでだけ動きます）、任意のファイル（`--to <file>`）に写します。`--print` は書かずに出力、`--remove` は取り除きます。ファイル内の他の hook は残り、再実行すると自分の分だけを置き換えます |
| スキル | `setup` と `fix`。Claude Code と同じです（Codex には `CLAUDE_PLUGIN_ROOT` が無いので、スキルは自分のパスからラッパーを探します） |

- ルート: Codex はプロジェクトのディレクトリを渡さないので、セッションの `cwd` がルートです。
  `depgraph watch --root` が扱うディレクトリで Codex を起動してください。
- 文脈は二重に入りません: Codex は見つけた hook をすべて実行します。スクリプトが存在する写しが
  `~/.codex/hooks.json`、`<repo>/.codex/hooks.json`、`/etc/codex/hooks.json` のどれかにあれば、
  プラグイン同梱の hook は黙ります（プラグインの hook を飛ばす build でも動く写しの側が働きます）。
  プロジェクト・ユーザー・システムの Codex の `hooks.json` / `config.toml` が `depgraph hook` を自分で
  実行しているときは両方とも黙り、claude-hook-gate の `session_start`（`.agents/gate.yaml` か
  `.claude/gate.yaml`）が実行しているときはセッション開始の分が黙ります。
- 管理された hook のみ（管理者の `requirements.toml` に `allow_managed_hooks_only = true`）: Codex は
  ユーザー・プロジェクト・プラグインの hook を飛ばしますが、システム層の `/etc/codex/hooks.json` の hook は
  信頼の確認なしで実行します。これも同じ `setup.sh` のコマンドで済みます: `/etc/codex/requirements.toml` に
  このポリシーがあれば、hook を `/etc/codex/hooks.json` に入れ（マージした内容は自分の権限で作り、置くとき
  だけ `sudo` を実行します。パスワードを聞かれます）、`~/.codex/hooks.json` の写しは外して 1 つにします。
  ポリシーが見えない場所（MDM のプロファイル、ChatGPT のワークスペース）から来ているときは `--system` を
  付けてください（`... | bash -s -- --system`）。`--remove` でシステムのファイルから取り除きます。管理者権限が
  なければ、管理者に渡すコマンドを表示します。hook は自分の `~/.codex` にあるプラグインを指すので、自分の
  セッションで動きます。同じマシンの他のユーザーは各自で `setup.sh` を実行してください

- 下記の品質ゲートは、claude-hook-gate の Codex プラグインでも同じように動きます（`.agents/gate.yaml`、
  次に `.claude/gate.yaml` を読みます）。

## Claude Code hooks

`depgraph hook <session-start|prompt>` は Neo4j に入っている解析結果を Claude Code の
文脈へ戻す（読むだけ。スキャンも CLAUDE.md の更新もしない）。

- `session-start`：統計と Findings の要約（severity 順、error を先に）。件数の多いルールは
  件数と代表例数件に圧縮し、全体を約 4,000 文字に収める（`duplicate-symbol` が 50 件あっても溢れない）。
  あわせて [`depgraph gate`](#品質ゲートclaude-hook-gate) の基準として、セッション開始時の Findings を記録する
  （resume・compact では最初の記録を保つ）
- `prompt`：同じセッションで前回注入してから新規・解消した Findings だけ。変化が無ければ何も出さない。
  前回状態は `session_id` ごとに Neo4j のプロジェクトのグラフと並べて保存する（`HookSession` ノード）。
  1 プロジェクトにつき新しい 20 セッションまで、7 日より古いものは残さず、プロジェクトと一緒に消える（`projects rm`・`--reset`）。
  リポジトリには何も書かない（以前のバージョンの状態ファイル `.claude/.depgraph-hook-state.json` は削除する）
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

hook は他のコマンドと同じ Neo4j を使います（[Neo4j](#neo4j) 参照）。`--neo4j` / `$NEO4J_URI` があれば外部の Neo4j、
なければ root の稼働中の depgraph コンテナをラベルで探して使います。コンテナを起動することはなく、
稼働中のものがなければ何も出力しません。

### 品質ゲート（claude-hook-gate）

`prompt` はユーザーがプロンプトを送ったときにしか動きません。エージェントが 1 ターンで多くのファイルを
書き換える間は、何も届きません。`depgraph gate` はこの穴を埋めるもので、編集のたびとエージェントの終了前に
コマンドを実行する [claude-hook-gate](https://github.com/nonchan7720/claude-hook-gate)（`.claude/gate.yaml`）の
チェックとして使います:

```yaml
rules:
  # depgraph: このセッションで増えた負債（depgraph watch が必要。動いていなければ通す）
  - match: ["**/*.go", "**/*.{ts,tsx,js,jsx,mjs,cjs}", "**/*.py"]
    timeout: 60
    run:
      - { name: depgraph, cmd: "command -v depgraph >/dev/null || exit 0; depgraph gate" }
```

- 変更ファイル（`$CLAUDE_GATE_FILES`、または引数）を `watch` と同じやり方で自分でグラフに反映してから
  ルールを評価するので、`watch` のデバウンスを待ちません。セッション（`$CLAUDE_SESSION_ID`）の基準から
  **増えた** `--fail-on` 以上（既定 `warn`）の Findings を一覧にし、1 件でもあれば exit 1 にします。
  gate はその一覧をエージェントに渡し、失敗した rule はエージェントの終了時に再実行されるので、
  **自分が増やした負債が残っている間、エージェントは終われません**（claude-hook-gate は 5 回止めたら諦めます）。
  セッション開始時からあった負債では失敗しません。
- 基準は `depgraph hook session-start` が記録します。無いときは、最初の gate 実行で変更を反映する前の
  Findings を基準にします。`session_start` フェーズのある claude-hook-gate なら、`settings.json` の
  `SessionStart` hook の代わりに `gate.yaml` だけで連携できます:

  ```yaml
  session_start:
    - { name: depgraph, timeout: 10, cmd: "command -v depgraph >/dev/null || exit 0; depgraph hook session-start --format text" }
  ```

  `--format text` は要約そのものを出力します（gate は標準出力をそのまま注入するため）。hook 入力に
  セッションが無いときは `$CLAUDE_SESSION_ID` を使います。
- `--fail-on error` にすると、新しい循環 import とレイヤー違反でだけ失敗します。それ以外の Findings は
  `prompt` の hook でエージェントに届きます。
- 判断できないときは通します（exit 0、理由は標準エラー）: 稼働中のストアが無い（Neo4j は起動しません）、
  root のプロジェクトが未スキャン、セッションが無い、`--timeout`（既定 `60s`）切れ。
  CLAUDE.md は更新しません。root は `--root`、なければカレントディレクトリの git worktree（`$CLAUDE_PROJECT_DIR` と同じ
  リポジトリの別の worktree のとき。下記）、なければ `$CLAUDE_PROJECT_DIR`、なければカレントディレクトリです。
- **git worktree で動くエージェント。** claude-hook-gate は `.claude/worktrees/<name>/` 配下のファイルのルールを
  その worktree で実行します（`$CLAUDE_PROJECT_DIR` はメインツリーのまま、`$CLAUDE_GATE_FILES` は worktree からの
  相対パス）。gate はこのときメインツリーではなく worktree を、別プロジェクト（`<base>@<name>`。[Projects](#projects) 参照）
  としてメインツリーの `watch` の Neo4j 上で検査するので、`depgraph watch` はメインツリーの 1 つで足ります。
  worktree の Findings はそこで動くエージェントにだけ届き、メインツリーのプロジェクトには触れません。
  - worktree のグラフはその base（履歴のうち worktree 自身が作っていない最新のコミット。`HEAD` の reflog で判定。
    コミットされたツリーそのもので、チェックアウトにある ignore 対象ファイルとサブモジュールも含む）から作り、それを
    セッションの baseline にします。初回の実行より前にエージェントが変えた分・コミットした分も追加として数え、
    もともとあった負債は数えません。
  - 以後は毎回、`$CLAUDE_GATE_FILES` に加えて git 上でグラフと違いうるものを反映します: 記録したコミットと `HEAD` の
    間で変わったファイル（コミット、`git reset` / `rebase`）と、前回反映したときから内容が変わった未コミット・未追跡の
    ファイル。worktree 自身が作っていないコミット（上流で他のエージェントの作業がマージされ、それを merge / rebase した
    など）だけで変わったファイルは上流の変更とし、それで入ってきた負債はその worktree のセッションの baseline に加えます
    （このエージェントの負債ではないため）。それ以外は、編集とコミットを 1 度にしたものも含めてエージェントの変更です。worktree の base が動いたとき（上流への rebase や
    `pull --rebase`）は新しい base の Findings も baseline に加えるので、main から来た負債は、エージェントの
    コミットが載り直したファイルの中にあってもエージェントのものになりません。マージコミットはコンフリクトの解決も含めて上流の変更とします
    （コンフリクトしたファイルには取り込んだ側の変更も入っているため。解決の中だけで入った負債は報告しません）。merge・cherry-pick・revert・rebase の途中は、
    取り込み中の変更がエージェントの変更のようにステージされているので、gate は理由を出して通し、コミット後に検査します。
  - 初回のビルド、上流の変更の取り込み、import 解決が依存するファイル（`go.mod`・`tsconfig.json`・`package.json` など）を含むか 200 ファイルを
    超える差分は、プロジェクトのリースを取ったバックグラウンドの `depgraph` プロセスで行うので、大きなリポジトリでも
    gate の `--timeout` で止まったままにはなりません。gate はタイムアウトの範囲で完了を待ち、間に合わなければ
    理由を標準エラーに出して通します（次の実行で検査します）。
  - 同じメインツリーの削除済み worktree のプロジェクトは、ビルドの後に消します（`depgraph projects prune` でも消せます）。

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

## MCP App

`depgraph mcp` は MCP サーバー（stdio）で、[MCP Apps](https://modelcontextprotocol.io/extensions/apps) に対応したホスト（Claude、Claude Desktop など）の
会話内に、ブラウザ UI と同じ画面を表示します。`serve` と同じく Neo4j に投入済みのグラフを読むので、先にそのリポジトリを
`scan` か `watch` してください。

```json
{
  "mcpServers": {
    "depgraph": { "command": "depgraph", "args": ["mcp", "--root", "/path/to/repo"] }
  }
}
```

（Claude Code: `claude mcp add depgraph -- depgraph mcp --root /path/to/repo`）

| tool | 見えるもの | 内容 |
|---|---|---|
| `show_dependency_graph` | モデル・アプリ | ルールを再評価し、Findings の要約を返してグラフ画面（`ui://depgraph/graph.html`）を開く |
| `graph_view` | アプリ | 折りたたんだグラフ。`GET /api/graph` と同じ JSON |
| `package_detail` | アプリ | 1 パッケージの詳細。`GET /api/package` と同じ JSON |

- 画面はネットワークに出られないホストのサンドボックスで動くため、1 ファイルに自己完結した HTML としてビルドし
  （`vite build --mode mcp-app` で `pkg/interfaces/handler/mcpapp/dist/` に出力、ブラウザ UI と同じくバイナリに埋め込み）、
  グラフはホストがサーバーへ中継するアプリ専用ツールで読みます。サンドボックスは Worker スクリプトを許さないので、ELK はページ内で実行
- イベントストリームは無く、ヘッダーの `refresh` でルールを再評価して再読込（モデルが `show_dependency_graph` を再度呼んだときも同様）。
  ホストが対応していれば `fullscreen` ボタンを表示
- ログは stderr、stdout はプロトコル専用

## Graph schema

```
(Project {key, name, root, origin, base, worktree, lastScanAt})
(Package {project, key, name, external})
(File    {project, path, language, lines, updatedAt,
          scope, entry, internal, generated, uses, exports, keep})   # 未使用コード系ルール用の usage
(Symbol  {project, name, kind, exported, line, endLine, language, file})

(Package)-[:CONTAINS]->(File)-[:DECLARES]->(Symbol)
(File)-[:IMPORTS {spec}]->(Package)
(Package)-[:IMPORTS]->(Package)        # file-level から再計算
(Symbol)-[:CALLS|REFERENCES]->(Symbol) # watch 中に LSP が変更ファイルについて追加
```

`project` はプロジェクト key。Package は `(project, key)`、File は `(project, path)` で一意です。
`Package.key` はリポジトリルートからのディレクトリパス（ルートは `.`）。
リポジトリ外への import は `ext:<パッケージ>` という key の `external=true` な Package になります（これもプロジェクトごと。`elkjs/lib/elk-api` はパッケージ `ext:elkjs`）。
どこにも解決できない import は Package も辺も作りません（プロジェクトのコンパイラ / 型チェックが報告します）。

File の usage プロパティはそのファイル自身から分かることだけです: どのファイル / パッケージのどの名前を使うか（`uses`）、別名で export している名前（`exports`）、意図して残す名前（`keep`: Python の `__all__`、デコレータ付き定義、STI のサブクラス）、生成コード / vendored か、エントリポイントである理由（`entry`）。
ファイルと一緒に置き換わるので `watch` の差分更新でも正しく保たれ、未使用コード系ルールが評価時にプロジェクト全体で突き合わせます。

## Import resolution

import は各エコシステム自身の解決方法で、プロジェクトの設定を読んで解決します。

| 言語 | 方法 | 詳細 |
|---|---|---|
| TypeScript / JavaScript | esbuild の resolver: `tsconfig.json` / `jsconfig.json`、`package.json`（`exports`・`imports`・workspaces。インストール前も）、`node_modules` | [TypeScript / JavaScript](docs/languages/ja/typescript.md#import-の解決) |
| Go | `go.mod` / `go.work`（入れ子モジュール・`use`・`replace`） | [Go](docs/languages/ja/go.md#import-の解決) |
| Python | リポジトリのパッケージルート（ルート・`src/`・`pyproject.toml` / `setup.cfg` のパッケージディレクトリ）上の import システム | [Python](docs/languages/ja/python.md#import-の解決) |
| Ruby | リポジトリから分かるロードパス上の `require` / `require_relative` と、Zeitwerk と同じ規則での定数（アプリケーション・engine・packwerk の pack） | [Ruby](docs/languages/ja/ruby.md#import-の解決) |

相対パス・エイリアス・プロジェクト内モジュールが何も指していない import は、外部ではなく未解決として扱い、依存には現れません。`watch` はこれらの設定ファイルが変わると再スキャンします。

## Rules

| rule | severity | 内容 |
|---|---|---|
| `import-cycle` | error | パッケージ間の循環 import。8 パッケージ以下の循環に乗る import ごとに、それを通る最短の循環（最大 50 件）。Go のテストファイルだけが行っている import は数えません。外部テストパッケージ（`package x_test`）はそのディレクトリのパッケージとは別のパッケージで、`install -> v1` と `v1/defaults_test.go -> install` は Go でビルドできるためです |
| `module-cycle` | error | モノレポのモジュール間の import 循環（Go モジュール・npm のワークスペースパッケージ・Python プロジェクト・Ruby のアプリケーション / gem / engine / packwerk の pack。`go.mod`・`package.json`・`pyproject.toml`・`setup.py`・`setup.cfg`・`Gemfile`・`*.gemspec`・`package.yml` のあるディレクトリと[コンポーネント](#モノレポcomponents)）。ディレクトリ同士では循環にならないもの（`packages/web/src/a -> packages/kit/src -> packages/web/src/b`）も検出します。1 ホップごとに import を 1 つ示し、`import-cycle` が報告済みの循環は重ねて報告しません |
| `layer-violation` | error | import してはいけないレイヤーを import している（`handler → repository` など、または独自レイヤー）。レイヤーには外部パッケージも書けるので、フレームワークを import するドメインも違反になる（[フレームワーク](#フレームワークレイヤーに外部パッケージ)参照） |
| `concrete-dependency` | warn | インターフェース経由でしか使ってはいけないレイヤー（`abstract:`）の実装を、struct / class が保持（フィールド・コンストラクタ引数）または生成している。しかもその実装は、保持側のパッケージ・実装自身のパッケージ・内側のレイヤーで宣言されたインターフェースを満たしている（[レイヤーの内側](#レイヤーの内側依存性逆転と型の漏洩)参照） |
| `type-leak` | warn | レイヤーの API（公開関数・メソッドの引数と戻り値、公開フィールド）が、インターフェース経由でしか使ってはいけないレイヤー（`abstract:`）の struct / class を露出している |
| `duplicate-code` | warn | 名前に関係なく、同じロジックが複数箇所にある（完全に同じ、または少し手を加えた複製。下記） |
| `god-package` | warn | ハブ：10 パッケージ以上から import され、かつリポジトリ内の 3 パッケージ以上を import している。多くから依存されてもリポジトリ内の何にも依存しないパッケージ（ドメインモデルなど）は安定しており、god package ではない（安定依存の原則） |
| `low-cohesion` | info | Go のパッケージ（ファイル単位）や TypeScript / Python のモジュール（トップレベルの宣言単位）が、内部で何もつながっていない部分に分かれ、しかも部分ごとに使う側が別（2 つの部分を使う側が無い）：1 か所に複数の責務がある。サイズの上限は linter の仕事で、誰がどの部分を使うかはプロジェクト全体を見て初めて分かる。テスト・生成ファイル・エントリポイント・まるごと使われる単位（dot import、`export *`、値として使われる名前空間）・データだけの単位（利用側ごとに 1 つずつ使われるエンティティ / DTO：振る舞いの無い型、データクラス、定数、コンストラクタ）は対象外 |
| `duplicate-symbol` | warn | 同名の exported function/class が複数ファイルに存在（1 つの Go パッケージのファイルは 1 か所と数える。同名が両立するのは build constraint で分けたときだけのため。Ruby は対象外: クラスの再オープンは Ruby の拡張方法そのもの） |
| `unused-export` | warn | プロジェクト内の誰も（自ファイルでさえ）使っていない export（Go: main 以外のパッケージの exported な関数と型。メソッドは対象外 / TS・JS: `export` された宣言。re-export・barrel を辿る / Python: モジュール直下の public な関数とクラス / Ruby: クラス、中身のあるモジュール、`Struct.new` / `Class.new` の定数。メソッドは対象外） |
| `unreachable-file` | warn | どのエントリポイントからも import で辿れない TS/JS/Python/Ruby ファイル。誰も import していないファイルごとに 1 件で、そのファイル経由でしか辿れないファイルを併記 |
| `unreachable-package` | warn | `main` パッケージ・テスト・ライブラリモジュールの公開パッケージのどれからも import されない Go パッケージ |

### 未使用コード

`unused-export` と `unreachable-*` はプロジェクト全体を見て初めて分かるものだけを報告します。コンパイラ・`go vet` / staticcheck・`tsc --noUnusedLocals`・eslint・ruff が見るのは未使用のローカル変数や非公開の名前で、誰も import しない export は見えません。
誤検知はバグなので、import グラフの外から読み込まれる兆候があるファイルはエントリポイント（到達可能で、export は外から使われる扱い。報告しない）とします。

- 言語ごとの、エントリポイント・使用とみなすもの・報告するもの・意図して残すもの:
  - [Go](docs/languages/ja/go.md#未使用コード): `main` パッケージ、テスト、ライブラリモジュールの公開パッケージ。
  - [TypeScript / JavaScript](docs/languages/ja/typescript.md#未使用コード): `package.json` のエントリと scripts、HTML ページ、設定ファイル、テスト、ファイルベースルーティング、private なワークスペースパッケージ。
  - [Python](docs/languages/ja/python.md#未使用コード): `__main__`、テスト、`pyproject.toml` / `setup.cfg` の scripts・entry points、Django の自動検出されるモジュール、`__all__`。
  - [Ruby](docs/languages/ja/ruby.md#未使用コード): 使用としての定数（関連・ビュー・実行時に組み立てる名前も）、Rails の規約、gem、packwerk の public path、Concern、ライブラリの名前空間。
- 共通: Dockerfile・Procfile・Makefile・compose・CI ワークフロー・`mise.toml`・シェルスクリプトが実行するファイルやモジュール（`node dist/server.js`・`python -m pkg.tool`・`uvicorn app.main:app`）、文字列中のパスやドット区切りモジュール名（`new URL('./worker.ts', ...)`・`"app.tasks.send"`・`importlib.import_module(f"app.plugins.{name}")`・`` import(`./locales/${l}`) ``）、あらゆる言語の文字列中のソースファイル名（JS をバンドルする Go プログラムの `"render.js"`）。
- ヘッダで生成コードと宣言しているファイルは報告しません。

depgraph から見えない方法で読み込まれるファイル（名前で読み込むプラグイン、外部ツールが実行するファイル）には `depgraph:entry` コメント（`// depgraph:entry`・`# depgraph:entry`）を書いてください。自リポジトリでは使わないライブラリの公開 API は `.depgraph.yml` の `ignore` で除外でき、エントリポイントは `entries` で追加できます（ファイルと Go パッケージディレクトリに対するパスの glob。3 つのルールで共通。後述）。自ファイル内でだけ使われている export は報告しません（export は不要ですが、同じファイルで登録する Go のハンドラなど、スタイルの範囲のため）。

レイヤー・禁止 import・severity・閾値は `.depgraph.yml` で設定します（次節）。

### 設定（`.depgraph.yml`）

スキャンする root に `.depgraph.yml` を置いてアーキテクチャを宣言します。キーはすべて省略可能です。
`scan`・`check`・`watch`・`serve`・`hook` が読み、`watch` は変更されると読み直します
（`project` の変更は再起動が必要）。

エディタは JSON Schema [`schema/depgraph.schema.json`](schema/depgraph.schema.json)（`depgraph schema` でも出力）で
補完と検査ができます。yaml-language-server（VS Code の YAML 拡張、Neovim、Zed など）では先頭に次の行を書きます。
使っている depgraph に合わせるなら `main` の代わりにリリースのタグを指定します:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/nonchan7720/depgraph/main/schema/depgraph.schema.json
```

スキーマはルールの定義とデコーダから生成しています（`mise run schema`。古いままだとテストが失敗）。
depgraph が受け付けるものとスキーマが受け付けるものは一致します。

```yaml
project: my-repo            # プロジェクト key（Projects 参照）

layers:                     # レイヤー名 -> パッケージパスの glob
  domain: ["pkg/domain/**"]
  services: ["pkg/services/**"]
  infrastructure: ["pkg/infrastructure/**"]
  interfaces: ["pkg/interfaces/**", "cmd/**"]
  frameworks: ["ext:*", "!ext:github.com/google/uuid"]   # サードパーティのパッケージ（下記）

forbidden:                  # レイヤー -> import してはいけないレイヤー
  domain: [infrastructure, services, interfaces, frameworks]
  services: [interfaces]
  infrastructure: [interfaces, services]

abstract:                   # レイヤー -> インターフェース経由でしか使ってはいけないレイヤー
  services: [infrastructure]


rules:                      # ルールごと: enabled、severity（error | warn | info）、オプション
  import-cycle: {severity: error}
  layer-violation: {severity: error}
  god-package: {severity: warn, fanIn: 10, fanOut: 3}
  duplicate-symbol: {enabled: false}
  duplicate-code: {minTokens: 50, similarity: 0.8}   # 本体の大きさと指紋の類似度
  # duplicate-symbol: {ignore: [main, New, "Test*", "*Handler"]}  # 対象外の名前（デフォルトを置き換える）
  unused-export: {ignore: ["pkg/api/**"]}            # 報告しないパス（ライブラリの公開 API など）
  unreachable-file: {entries: ["plugins/**"]}       # depgraph から見えないエントリポイント
  # entries / ignore は unused-export・unreachable-file・unreachable-package で共通

ignore: ["**/testdata/**"]  # すべてのルールから除外するパッケージ

store:                      # Neo4j の用意の仕方（Neo4j 参照）。フラグと環境変数が優先
  persist: false
  provider: container
  containerCli: docker
```

- **パッケージ glob** はパッケージ key（root からのディレクトリ、root は `.`）にパスセグメント単位で
  マッチします。`**` は任意個のセグメント、`*` と `?` は 1 セグメント内だけ。
  `pkg/domain/**` は `pkg/domain` とその配下すべて、`**/db/**` は `db` や `internal/db/sql` にマッチし、
  `pkg/feedback` にはマッチしません。
- **名前のパターン**（`duplicate-symbol` の `ignore`）はシンボル名全体に一致します。`*` は任意の文字列、
  `?` は 1 文字で、それ以外はそのままの文字です。`New` はその名前だけ、`Test*` は `Test` で始まる名前（前方一致）、
  `*Handler` は `Handler` で終わる名前に一致します。
- 1 つのパッケージが複数のレイヤーに属してもかまいません。違反になるのはペアをまたぐ import だけで、
  両方が禁止される側のレイヤー、または両方が import する側のレイヤーに属するなら報告しません。
- `layers` を書くとデフォルトのレイヤーを置き換え、デフォルトの禁止ペアと abstract ペアもなくなります。
  `forbidden` や `abstract` だけを書いた場合はデフォルトのレイヤーを参照します。
- `ignore` のパッケージは検出結果から外れます（それを通る循環、それからの扇入れなど）。
  `.depgraphignore` と違い、スキャンはされ UI にも表示されます。
- デコードは厳密です。未知のキー・ルール・オプション・severity や、未定義のレイヤーを指す
  `forbidden` は、黙って無視せずファイル名つきのエラーにします。

`.depgraph.yml` が無いときのデフォルトは、レイヤー `controller`・`db`・`domain`・`handler`・`infra`・
`repository`・`usecase`（それぞれ同名のパスセグメント。加えて `controllers`・`handlers`・
`infrastructure`・`repositories`・`usecases`）と、禁止ペア `handler → repository / infra / db`、
`controller → repository / db`、`domain → infra / handler / repository`、`usecase → handler`、
abstract ペア `usecase → infra / db` です。
このリポジトリ自身の [`.depgraph.yml`](.depgraph.yml) が例になります。

### フレームワーク：レイヤーに外部パッケージ

`ext:` で始まるレイヤーパターンはリポジトリ外のパッケージにマッチするので、
`forbidden` でドメインをフレームワークから切り離せます:

| パターン | マッチするもの |
|---|---|
| `ext:gorm.io/gorm` | そのパッケージと配下（`gorm.io/gorm/clause`。Python の import ではサブモジュールも: `ext:sqlalchemy` は `sqlalchemy.orm` にマッチ。一方 `lodash.debounce` は `ext:lodash` の配下ではない）。標準ライブラリも含む（`ext:net/http`、`ext:database/sql`） |
| `ext:*`、`ext:github.com/aws/*`、`ext:@nestjs/*` | サードパーティと分かっているパッケージをパターンで指定（`*` は `/` を含む任意の文字列、`?` は 1 文字）。サードパーティかどうかの判定は下記 |
| `!<パターン>` | レイヤーから除外する（`"!ext:github.com/google/uuid"`、`"!pkg/domain/legacy/**"`） |

```yaml
layers:
  domain: ["internal/domain/**"]
  frameworks: ["ext:*", "!ext:github.com/google/uuid", "!ext:github.com/shopspring/decimal"]
  http: ["ext:net/http"]
forbidden:
  domain: [frameworks, http]   # internal/domain -> ext:gorm.io/gorm (domain must not import frameworks)
```

ワイルドカードがマッチするのは、サードパーティと分かっている import だけです。判定はプロジェクト自身のファイルと解決結果から行い、
depgraph が持つ名前の一覧は使わず、何も実行しません:

| 言語 | サードパーティ | サードパーティではない |
|---|---|---|
| Go | import 元モジュールの `go.mod`（または `go.work`）が require / replace しているモジュール | 先頭要素にドットを含まないパス（go コマンドの標準ライブラリの規則） |
| TypeScript / JavaScript | `node_modules` に解決されたもの。未インストールなら、そのファイルか上位の `package.json` が宣言しているもの | esbuild が組み込みに解決した Node の組み込みモジュール（`punycode` は組み込み、`punycode/` はインストール済みパッケージ） |
| Python | `languages.python` が指すバージョンの標準ライブラリに無いもの。判定にはプロジェクトの型チェッカーが持つ typeshed を使う（外部の import は標準ライブラリかインストール済みのパッケージのどちらか） | そのバージョンの標準ライブラリにあるもの |
| Ruby | `Gemfile` が求める gem（バンドルの `Gemfile.lock` の `DEPENDENCIES`）の名前に一致する `require` や定数（トップレベルの名前で判定：`ActiveRecord::Base` の `ActiveRecord`） | それ以外は不明。`ext:` パターンに書く価値のある gem と定数は `external-unclassified` が一覧にする（[詳細](docs/languages/ja/ruby.md#サードパーティの判定レイヤーの-ext)） |

import ごとに判定結果と根拠を記録します（`depgraph parse` で見られます）。サードパーティならワイルドカードにマッチするのに判定できなかった
import は、違反としては報告せず、黙って捨てることもしません。`external-unclassified`（info）が言語ごとに根拠つきで一覧にします。

**Python：`languages`。** 標準ライブラリは Python のバージョンで変わり、リポジトリはバージョンを複数の場所
（`.python-version`、`mise.toml`、Dockerfile など）で、必ずしも同じでなく指定しています。depgraph はどれかを選びません。
バージョンを固定しているファイルを、そのファイルのディレクトリからの相対パスで指定するか（推奨）、バージョンそのものを書きます。
部分ごとにバージョンが違うモノレポ（アプリは 3.12、利用者のために 3.9 に据え置くライブラリ、など）は、コンポーネントごと、
またはその部分の `.depgraph.yml` に書きます。ファイルには、そのファイルを含む最も深いディレクトリの設定が適用されます。

```yaml
languages:
  python: {versionFile: .python-version}       # ルート
components:
  lib/foo:
    languages:
      python: {versionFile: .python-version}   # lib/foo/.python-version
  "services/*":                                # 各サービス自身のファイル
    languages:
      python: {versionFile: Dockerfile}
```

- バージョンファイル：`.python-version`、`.tool-versions`、`mise.toml`（`.mise.toml`、`.config/mise.toml`、`mise.local.toml`）、
  `pyproject.toml`（`[tool.pyright] pythonVersion`、`[tool.mypy] python_version`。`requires-python` は範囲なので使わない）、
  `pyrightconfig.json`、`mypy.ini` / `setup.cfg`、`FROM python:<バージョン>...` が 1 つだけの Dockerfile。
  厳密なバージョンだけを受け付けます。`latest`、`system`、`3`、範囲、複数のバージョン、マルチステージの Dockerfile、`ARG` は
  推測せずエラーにします。
- ファイルはシンボリックリンクでもかまいません（dotfiles の管理など。リポジトリ外を指してもよい）。FIFO・デバイス・疑似ファイルには
  たどらず、64 KiB まで読み、厳密に解釈し、表示するのは相対パスだけです（中身は決して表示しません）。
- ファイルとその設定の間にあるディレクトリが別のバージョンを固定している場合（ルートの設定の下の `lib/foo/.python-version` など）や、
  python を指定する `mise.<env>.toml` がある場合、そのファイルの import は理由つきで「判定できない」になります。
  depgraph に選ばせず、その部分の `languages.python` を書いてください。
- そのバージョンの標準ライブラリは、プロジェクト自身の型チェッカーが持つ typeshed の `stdlib/VERSIONS` から読みます。
  設定のディレクトリからルートまでの `node_modules/pyright` または `node_modules/basedpyright`（複数あればモジュールごとに一致が必要）、
  または `stdlib:` で指定したディレクトリです。depgraph 自身は一覧を持ちません。
- import は評価のときに判定するので、設定を変えても再スキャンは要りません。
- `depgraph init` がこの設定を書きます。Python のファイルがあり、バージョンを固定するファイルを持つディレクトリごとに、そのファイルを指定します
  （1 つのディレクトリのファイルが一致していれば `.python-version` を最優先、Dockerfile を最後に選ぶ）。食い違う場合は端末なら尋ね、
  端末でなければそのディレクトリを外します。`.depgraph.yml` の他の内容と既存の設定は残し、リポジトリ外を指すシンボリックリンクの
  バージョンファイル（リンク先の無い checkout では判定できない）と、typeshed が無いことを警告します。`--dry-run` は結果を表示するだけです。

### レイヤーの内側：依存性逆転と型の漏洩

import の向きは依存性ルールそのものですが、ユースケースはリポジトリのパッケージを import してよい（ポートをそこで宣言している、
その型を使う）うえで、ポートではなく PostgreSQL の実装を保持したり、ORM の行を API から返したりできてしまいます。
`abstract:` はレイヤーごとに、インターフェース経由でしか使ってはいけないレイヤーを指定します。import 自体は許可されたままです:

```yaml
abstract:
  usecase: [infra, db]          # デフォルト（デフォルトのレイヤーに対して）
```

depgraph はすべての型と関数のメンバーを読みます（Go・TypeScript・Python）。フィールド、コンストラクタ引数（Go の `New*`、
`constructor`、`__init__`）、引数、戻り値の型と、別パッケージの型を生成している箇所（`&infra.T{}`、`new T()`、`T()`）です。

- **concrete-dependency**：`usecase` の struct / class が `infra` の実装を保持または生成していて、**かつ**その実装が、
  保持側のパッケージ・実装のパッケージ・内側のレイヤーで宣言されたインターフェースを満たしている場合に報告します。
  実装とは振る舞いを持つ struct / class です（インターフェース、抽象クラス、Protocol / ABC、dataclass / pydantic モデル / Enum、
  `String`・`MarshalJSON`・`Scan` などしか持たない struct は除く）。満たしているとは、Go ならインターフェースのメソッドを
  すべて持つこと、TypeScript / Python なら implements / extends で宣言していることです。そうしたインターフェースが無ければ
  報告しません（インターフェースを作るべきかは設計判断で、事実ではないため）。コレクション、テストファイル、
  composition root（ペアに入っていないレイヤー：`cmd/`、`main`）は対象外です。

  ```text
  internal/usecase depends on the concrete type internal/infra.PostgresOrders
  (internal/usecase/order.go:13 OrderUsecase.repo, and 1 more) although it implements
  internal/usecase.OrderRepository: hold the interface (usecase must depend on infra through an interface)
  ```
- **type-leak**：`usecase` の API（公開関数・メソッドの引数と戻り値、データを持つ公開フィールド）が `infra` の struct / class を
  名指ししていて、ユースケースの呼び出し側がインフラの型に依存してしまう状態を報告します。インターフェースは問題ありません。
  コンストラクタ引数と保持している実装は concrete-dependency の対象です。

コンポーネントでも `forbidden` と同じく、ディレクトリ内で `abstract` を書けます。Ruby には読める型が無く、インターフェースも
ダックタイピングだけなので、どちらのルールも Ruby には適用しません（`low-cohesion` も同様。部分がメソッドになり、動的ディスパッチで
呼ばれるため）。depgraph は推測しません。

**アップグレード時の注意**: この 2 つのルールはデフォルトで有効（warn）で、`.depgraph.yml` に `layers:` がない場合（設定なし、または `forbidden:` だけ）は abstract ペア `usecase → infra / db` も有効です。デフォルトのレイヤーに当てはまるプロジェクトでは、アップグレード後にレポート・`CLAUDE.md`・フックに新しい指摘が出ます。以前の動作のままにするには `abstract: {}` を書くか、ルールを無効にします（`rules: {concrete-dependency: {enabled: false}}`）。`low-cohesion` も info として有効です（`--fail-on warn` では失敗しません）。

### モノレポ（`components`）

モノレポでは部分ごとにアーキテクチャが違い、言語も違うことがよくあります。`pkg/` は Go、
`lib/auth` は Go で `lib/ui` は TypeScript、`packages/` の下はディレクトリごとに npm パッケージ、といった構成です。
それぞれを **コンポーネント** として、そのディレクトリからの相対パスでレイヤーを宣言します。書き方は 2 通りです:

```yaml
# root の .depgraph.yml
components:
  "packages/*":             # パターン: マッチしたディレクトリごとに 1 コンポーネント（packages/web、packages/kit、...）
    layers:
      api: ["src/api/**"]
      ui: ["src/components/**"]
    forbidden:
      api: [ui]
  lib/ui:
    forbidden:              # forbidden だけ: lib/ui の中でデフォルトのレイヤーを使う
      handler: [repository]
```

```yaml
# lib/auth/.depgraph.yml: 同じファイルが --root lib/auth でもそのまま使える
layers:
  domain: ["domain/**"]
  adapter: ["adapter/**"]
forbidden:
  domain: [adapter]
ignore: ["**/testdata/**"]                 # lib/auth/**/testdata/**
rules:
  unused-export: {ignore: ["api/**"]}       # lib/auth/api/**
```

- コンポーネントの禁止ペアは、同じディレクトリに属する 2 つのパッケージ間の import にだけ適用します。
  `packages/web/src/api -> packages/web/src/components` は `(packages/web: api must not import ui)` として
  報告し、`packages/web` から `packages/kit` への import は報告しません。言語は関係ありません（レイヤーはディレクトリ単位です）。
- デフォルトのレイヤーはディレクトリ名からの推測なので、コンポーネントの中（とコンポーネントへの import）には
  適用しません。root で宣言したレイヤー（`layers:`）はリポジトリ自体のアーキテクチャなので、
  コンポーネントをまたいで全体に適用します（`apps` 同士は import しない、など）。
- root より下にある `.depgraph.yml` は root のものと一緒に読みます（`scan`・`check`・`watch`・`hook`・`gate`。
  `watch` は変更されると読み直します）。その `layers`・`forbidden`・`components`・`ignore` と、未使用コード系ルールの
  `ignore` / `entries` は、そのディレクトリからの相対パスです。`components:` のパターンにマッチするディレクトリに
  専用のファイルがあれば、そちらを優先します。同じディレクトリを `components:` と専用ファイルの両方で宣言するとエラーです。
  `project` と `store` は、そのディレクトリを root にしたときだけ効きます。ルールの有効 / 無効・severity・閾値も同じで、
  これらは適用せずに警告を表示します。スキャン対象外のディレクトリ（`node_modules`・`.git`・`.depgraphignore` など）の
  ファイルは読みません。
- コンポーネントのディレクトリは root からのパスか、`*` と `?` を使ったパターンです（`**` は使えません）。

### 重複コード

depgraph は関数・メソッド・クラスごとに本体の「形」を持ちます。構文トークンからコメントを除き、識別子と
リテラルをすべて伏せたもので、名前を変えた複製は同じ形になります。形が同じもの、またはトークン 4-gram の
winnowing 指紋（Schleimer ら *Winnowing*）が 80% 以上一致する（Jaccard）ものを複製としてまとめるので、文を
足したり変えたりした複製も見つかります。50 トークン未満の本体、10 を超える関数に共通する指紋（どの関数にも
あるエラー処理やループ）、テストコード（`_test.go`・`*.test.*`・`*.spec.*`・`test_*.py`・`tests/` など。テーブル
テストは意図的に似ている）は対象外です。コンパイラや linter が報告しない「同じことをする、動いている 2 つの関数」を見つけるためのルールです。

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
pkg/domain/rules/                    ルール、Config（レイヤー・パッケージ glob）、Finding / 差分、Repository interface
pkg/domain/project/                  Project、key 決定・移動/衝突判定、Repository interface
pkg/services/analysis/               スキャン・差分適用・ルール評価・LSP 精密化のサービス
pkg/infrastructure/repository/graph/ Neo4j 投入・クエリ
pkg/infrastructure/repository/rules/ Cypher ルール評価
pkg/infrastructure/repository/project/ Project ノード
pkg/infrastructure/config/           .depgraph.yml（プロジェクト key・ルール設定・ストア設定。モノレポのサブディレクトリの設定）
pkg/infrastructure/neo4jprovider/    Neo4j の用意の仕方: Provider の契約、選択、external
  container/                         docker / podman / nerdctl のコンテナの Neo4j（ライフサイクル・ラベル・後片付け）
pkg/infrastructure/workspace/        root のプロジェクト識別（.depgraph.yml、git の origin・worktree）
pkg/infrastructure/resolver/         リポジトリ走査 + 除外パターン
  treesitter/                        デフォルト: 構造解析（import / 宣言、gotreesitter）
  imports/                           エコシステムごとの import 解決（esbuild、go.mod / go.work、Python の import システム、Ruby の require / Zeitwerk）
  lsp/                               言語サーバー（callHierarchy / references）による精密化
pkg/infrastructure/watch/            fsnotify + デバウンス
pkg/infrastructure/claudemd/         CLAUDE.md の管理区間更新
pkg/infrastructure/codexhooks/       Codex プラグインの hook を Codex の hooks.json に写す（depgraph codex install-hooks）
pkg/interfaces/cmd/                  CLI（cobra: parse / scan / check / watch / serve / mcp / hook / gate / projects / store / codex）
pkg/interfaces/handler/ui/           HTTP サーバー + SSE + 埋め込み UI（web/ のビルド成果物）
pkg/interfaces/handler/mcpapp/       MCP サーバー: UI を MCP App として提供（web/ の 1 ファイルビルド、アプリ専用ツール）
web/                                 ブラウザ UI のソース（Vite + TypeScript、Cytoscape + elkjs）
plugin/                              Claude Code と Codex のプラグイン（hook・スキル。.claude-plugin/ と .agents/plugins/marketplace.json に登録）
pkg/interfaces/handler/hook/         Claude Code hook・品質ゲート（要約・差分・セッションで増えた Findings）
```

解析バックエンドは `graph.Resolver` で差し替え可能。優先順は treesitter → lsp → scip。

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

## License

Apache-2.0（[LICENSE](LICENSE)・[NOTICE](NOTICE) 参照）。Neo4j 本体は同梱しません。ユーザー自身の Neo4j に接続するか、
ユーザーのコンテナランタイムで公式の Neo4j Community イメージ（GPLv3）を実行します。
