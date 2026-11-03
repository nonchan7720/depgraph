# depgraph

[English](README.md) | 日本語

AIが書いたコードの依存関係を Neo4j に入れて可視化・ルール検査し、結果を `CLAUDE.md` に自動反映するツール。
バイブコーディング中にファイルが書き換わるたびにグラフを更新し、「循環依存が増えた」「レイヤー違反が入った」「同名関数が複製された」を即座に検出して次のプロンプトに戻します。

対応言語（tree-sitter）: Go / TypeScript / JavaScript / Python

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
| `scan --root DIR [--reset]` | フルスキャン → ルール評価 → CLAUDE.md 更新 |
| `watch --root DIR [--no-lsp] [--no-ui]` | scan 後、fsnotify で監視して差分更新（tree-sitter → LSP で CALLS/REFERENCES を追加） |
| `check --root DIR` | 投入済みグラフに対してルール評価だけ実行（CLAUDE.md 更新） |
| `serve --root DIR [--addr 127.0.0.1:7777]` | 投入済みグラフをブラウザ UI で表示（監視なし、CLAUDE.md は更新しない） |
| `parse --root DIR [files...]` | Neo4j なしで tree-sitter の解析結果を JSON（1 行 1 ファイル）で出力（デバッグ用） |
| `hook <session-start\|prompt>` | Claude Code の hook。Findings を追加文脈として出力（読むだけ、常に exit 0） |
| `projects list` / `projects rm <key>` / `projects prune [--dry-run]` | DB 内のプロジェクトの一覧・削除 |
| `store ls` / `store down` / `store rm [<key>...] [--all]` | depgraph が管理する Neo4j コンテナとデータボリュームの一覧・停止・削除（container プロバイダー） |
| `version` / `--version` | バージョンを表示（`version` はコミット・ビルド日時・Go ツールチェーンも表示） |

フラグ（`depgraph <command> --help` で一覧）:

| flag | 対象 | 既定値 |
|---|---|---|
| `--root` | 全コマンド | `.` |
| `--project` | 全コマンド | `<root>/.depgraph.yml` の `project`、無ければ `--root` のディレクトリ名（linked worktree では `@<worktree 名>` を付加） |
| `--exclude` | 全コマンド | なし（カンマ区切りの除外パターン） |
| `--ignore-file` | 全コマンド | `.depgraphignore`（`--root` 基準、空で無効） |
| `--neo4j` / `--user` / `--pass` / `--db` | scan / check / watch / serve / hook / projects | 外部 Neo4j: `$NEO4J_URI` / `$NEO4J_USER` / `$NEO4J_PASSWORD` / `$NEO4J_DATABASE`（URI 未設定時は depgraph が Neo4j を用意。ユーザー / パスワード / DB の未設定時 `neo4j` / `depgraph123` / `neo4j`） |
| `--neo4j-provider` | scan / check / watch / serve / hook / projects | `$DEPGRAPH_NEO4J_PROVIDER`、なければ `store.provider`、なければ URI があれば external、なければ container |
| `--container-cli` | scan / check / watch / serve / hook / projects / store | `$DEPGRAPH_CONTAINER_CLI`、なければ `store.containerCli`、なければ docker / podman / nerdctl で最初に応答したもの |
| `--persist` | scan / check / watch / serve | `store.persist`、なければ false（エフェメラル） |
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

hook は他のコマンドと同じ Neo4j を使います（[Neo4j](#neo4j) 参照）。`--neo4j` / `$NEO4J_URI` があれば外部の Neo4j、
なければ root の稼働中の depgraph コンテナをラベルで探して使います。コンテナを起動することはなく、
稼働中のものがなければ何も出力しません。

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
リポジトリ外への import は `ext:<パッケージ>` という key の `external=true` な Package になります（これもプロジェクトごと。`elkjs/lib/elk-api` はパッケージ `ext:elkjs`）。
どこにも解決できない import は Package も辺も作りません（プロジェクトのコンパイラ / 型チェックが報告します）。

## Import resolution

import は各エコシステム自身の解決方法で、プロジェクトの設定を読んで解決します。

| 言語 | 方法 |
|---|---|
| TypeScript / JavaScript | esbuild の resolver: `tsconfig.json` / `jsconfig.json`（`paths`・`baseUrl`・`extends`）、`package.json`（`exports`・`imports`・workspaces）、`node_modules`、`x.ts` を指す ESM の `./x.js` |
| Go | `go.mod` / `go.work`（入れ子モジュール・`use`・`replace`） |
| Python | リポジトリのパッケージルート（ルート・`src/`・`pyproject.toml` / `setup.cfg` のパッケージディレクトリ）上の import システム。`__init__.py` の無いディレクトリは名前空間パッケージで、標準ライブラリを隠しません |

相対パス・エイリアス・プロジェクト内モジュールが何も指していない import は、外部ではなく未解決として扱い、依存には現れません。`watch` はこれらの設定ファイルが変わると再スキャンします。

## Rules

| rule | severity | 内容 |
|---|---|---|
| `import-cycle` | error | パッケージ間の循環 import |
| `layer-violation` | error | import してはいけないレイヤーを import している（`handler → repository` など、または独自レイヤー） |
| `god-package` | warn | 10 パッケージ以上から import されている |
| `duplicate-symbol` | warn | 同名の exported function/class が複数ファイルに存在 |

レイヤー・禁止 import・severity・閾値は `.depgraph.yml` で設定します（次節）。

### 設定（`.depgraph.yml`）

スキャンする root に `.depgraph.yml` を置いてアーキテクチャを宣言します。キーはすべて省略可能です。
`scan`・`check`・`watch`・`serve`・`hook` が読み、`watch` は変更されると読み直します
（`project` の変更は再起動が必要）。

```yaml
project: my-repo            # プロジェクト key（Projects 参照）

layers:                     # レイヤー名 -> パッケージパスの glob
  domain: ["pkg/domain/**"]
  services: ["pkg/services/**"]
  infrastructure: ["pkg/infrastructure/**"]
  interfaces: ["pkg/interfaces/**", "cmd/**"]

forbidden:                  # レイヤー -> import してはいけないレイヤー
  domain: [infrastructure, services, interfaces]
  services: [interfaces]
  infrastructure: [interfaces, services]

rules:                      # ルールごと: enabled、severity（error | warn | info）、オプション
  import-cycle: {severity: error}
  layer-violation: {severity: error}
  god-package: {severity: warn, fanIn: 10}
  duplicate-symbol: {enabled: false}
  # duplicate-symbol: {ignore: [main, init, New]}   # 対象外の名前（デフォルトを置き換える）

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
- 1 つのパッケージが複数のレイヤーに属してもかまいません。違反になるのはペアをまたぐ import だけで、
  両方が禁止される側のレイヤー、または両方が import する側のレイヤーに属するなら報告しません。
- `layers` を書くとデフォルトのレイヤーを置き換え、デフォルトの禁止ペアもなくなります。
  `forbidden` だけを書いた場合はデフォルトのレイヤーを参照します。
- `ignore` のパッケージは検出結果から外れます（それを通る循環、それからの扇入れなど）。
  `.depgraphignore` と違い、スキャンはされ UI にも表示されます。
- デコードは厳密です。未知のキー・ルール・オプション・severity や、未定義のレイヤーを指す
  `forbidden` は、黙って無視せずファイル名つきのエラーにします。

`.depgraph.yml` が無いときのデフォルトは、レイヤー `controller`・`db`・`domain`・`handler`・`infra`・
`repository`・`usecase`（それぞれ同名のパスセグメント。加えて `controllers`・`handlers`・
`infrastructure`・`repositories`・`usecases`）と、禁止ペア `handler → repository / infra / db`、
`controller → repository / db`、`domain → infra / handler / repository`、`usecase → handler` です。
このリポジトリ自身の [`.depgraph.yml`](.depgraph.yml) が例になります。

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
pkg/infrastructure/config/           .depgraph.yml（プロジェクト key・ルール設定・ストア設定）
pkg/infrastructure/neo4jprovider/    Neo4j の用意の仕方: Provider の契約、選択、external
  container/                         docker / podman / nerdctl のコンテナの Neo4j（ライフサイクル・ラベル・後片付け）
pkg/infrastructure/workspace/        root のプロジェクト識別（.depgraph.yml、git の origin・worktree）
pkg/infrastructure/resolver/         リポジトリ走査 + 除外パターン
  treesitter/                        デフォルト: 構造解析（import / 宣言、gotreesitter）
  imports/                           エコシステムごとの import 解決（esbuild、go.mod / go.work、Python の import システム）
  lsp/                               言語サーバー（callHierarchy / references）による精密化
pkg/infrastructure/watch/            fsnotify + デバウンス
pkg/infrastructure/claudemd/         CLAUDE.md の管理区間更新
pkg/interfaces/cmd/                  CLI（cobra: parse / scan / check / watch / serve / hook / projects / store）
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
- [x] `.depgraph.yml` でのルール設定（レイヤー・禁止 import・severity・ignore）
- [ ] MCP サーバーモード（Claude Code / Cursor から直接問い合わせ）
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

Apache-2.0（予定）。Neo4j 本体は同梱しません。ユーザー自身の Neo4j に接続するか、
ユーザーのコンテナランタイムで公式の Neo4j Community イメージ（GPLv3）を実行します。
