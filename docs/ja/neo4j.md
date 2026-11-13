# Neo4j ストアとプロジェクト

[English](../neo4j.md) | 日本語 · [README](../../README_ja.md)

グラフをどこに保存するか、depgraph が Neo4j をどう用意するか、1 つのデータベースに複数のリポジトリをどう入れるかの説明です。なぜ Neo4j なのか、クエリが何をしているかは[アーキテクチャ](architecture.md)を見てください。

グラフは Neo4j に入り、ルールは Cypher クエリです。depgraph が Neo4j をどう用意するかは **プロバイダー** で、
次の順に決まります。

1. `--neo4j-provider <name>`、なければ `$DEPGRAPH_NEO4J_PROVIDER`、なければ `.depgraph.yml` の `store.provider`
2. URI が設定されていれば（`--neo4j` か `$NEO4J_URI`）`external`
3. コンテナランタイムが応答すれば `container`
4. `local`（予定: マシンにインストールされた Neo4j を子プロセスとして起動。未実装）

どれも使えないときは、プロバイダーごとに何が足りないかを 1 行で示して止まります。

## `container`（既定）: depgraph が Neo4j を起動・後片付けする

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

## `external`: 自分で用意した Neo4j（docker compose、サーバー、Aura）

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
