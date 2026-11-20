# アーキテクチャ

[English](../architecture.md) | 日本語 · [README](../../README_ja.md)

depgraph がどう作られているかの説明です。各機能がどう実現されているか、Neo4j に何を保存しているか、
ルールが Neo4j のどの機能を使っているか、そしてなぜストアが SQLite のような組み込みデータベースではなく
Neo4j なのかを書いています。

- [全体の流れ](#全体の流れ)
- [コードを読む](#コードを読む)
- [グラフを保存する](#グラフを保存する)
- [ルールの評価方法](#ルールの評価方法)
- [検出結果を届ける](#検出結果を届ける)
- [グラフスキーマ](#グラフスキーマ)
- [import の解決](#import-の解決)
- [Neo4j の役割と、使っている Neo4j の機能](#neo4j-の役割と使っている-neo4j-の機能)
- [なぜ Neo4j なのか](#なぜ-neo4j-なのか)
- [なぜ SQLite ではないのか](#なぜ-sqlite-ではないのか)
- [Neo4j のコスト](#neo4j-のコスト)
- [コードの構成](#コードの構成)
- [手でクエリを書く](#手でクエリを書く)

## 全体の流れ

どのコマンドも、1 本のパイプラインの一部です。

```
 ソースファイル       resolver             グラフストア        ルール             検出結果（Findings）
 Go, TS / JS,   ──►  tree-sitter     ──►  Neo4j         ──►  Cypher        ──►  CLAUDE.md の管理区間
 Python, Ruby        + import の解決       （プロジェクト                         hook / gate   （エージェントへ）
                                           ごとに 1 グラフ）                      ブラウザ UI / MCP App
                                                                                終了コード    （CI）
        ▲                                      ▲
        └── watch: fsnotify で検知し、──────────┘
            変更されたファイルだけ読み直す
```

| コマンド | コードを読む | グラフを書く | ルールを評価する | 届け先 |
|---|---|---|---|---|
| `scan` | 全ファイル | する | する | stderr、`CLAUDE.md`、終了コード |
| `watch` | 全ファイル、以後は変更分 | する | 変更のたびに | stderr、`CLAUDE.md`、ブラウザ UI |
| `check` | しない | しない | する | stderr、`CLAUDE.md`、終了コード |
| `serve` / `mcp` | しない | しない | する | ブラウザ UI / MCP App |
| `hook` | しない | セッション状態のみ | する | エージェントへの追加コンテキスト |
| `gate` | 変更されたファイル | する | する | エージェントに返す終了コード |
| `parse` | 指定したファイル | しない（Neo4j を使わない） | しない | JSON |

`scan`・`watch`・`gate` の中身はユースケース `pkg/services/analysis`（`FullScan`・`HandleChanges`・`Evaluate`）です。
このサービスはストアと resolver をインターフェース（`graph.Repository`・`rules.Repository`・`graph.Resolver`）
としてしか知りません。実装を組み立てるのは `pkg/interfaces/cmd` です。

## コードを読む

`pkg/infrastructure/resolver` が、ソースファイル 1 つを `graph.FileGraph` に変換します。ここまでは Neo4j を
使いません（`depgraph parse` はこの結果を JSON で出力します）。

1. **走査。** リポジトリを 1 回走査します。組み込みのディレクトリ名（`node_modules`・`vendor`・`.git` など）と
   `.depgraphignore` のパターンは読み飛ばします。
2. **構文解析。** 各ファイルを tree-sitter で解析します（pure Go の
   [gotreesitter](https://github.com/odvcencio/gotreesitter) を使うので cgo が不要で、`go build` だけで
   クロスビルドできます）。フルスキャンではワーカープールで並列に解析しますが、結果はパス順に渡すので、
   出力はワーカー数に依存しません。
3. **抽出。** そのファイルの構文木から、そのファイルだけで分かることを取り出します。

   | 取り出すもの | 使うルール |
   |---|---|
   | パッケージ（ディレクトリ）とトップレベルの宣言（`Symbol`） | すべて |
   | import | 循環、レイヤー、god-package |
   | 関数・メソッド・クラスの本体の「形」（識別子とリテラルを伏せたトークン列のハッシュと winnowing 指紋） | `duplicate-code` |
   | 他ファイルのどの名前を使うか、何を export するか、なぜエントリポイントなのか | `unused-export`、`unreachable-*`、`low-cohesion` |
   | import が読み込むファイル（ロード時か、関数の中か） | `file-cycle` |
   | 各関数が呼ぶ関数 | `function-cycle`、`pass-through`、`wide-function` |
   | フィールド・引数・戻り値の型と、型を生成している場所 | `concrete-dependency`、`type-leak` |

4. **import の解決。** import は、各エコシステム自身の解決方法で、プロジェクトの設定を読んで解決します
   （[import の解決](#import-の解決)）。結果は「リポジトリ内のパッケージ」「リポジトリ外のパッケージ
   （`ext:<名前>`）」「未解決」のいずれかです。未解決の import は捨てます。それはプロジェクトのコンパイラが
   報告するもので、depgraph の仕事ではないからです。
5. **精密化（watch のみ）。** 変更を保存して評価し終えたあと、バックグラウンドで言語サーバーに変更ファイルの
   呼び出しと参照（`callHierarchy`・`textDocument/references`）を問い合わせ、シンボル間の
   `CALLS` / `REFERENCES` エッジとして保存します。これは統計に数えられるだけで、どのルールも依存していません
   （フルスキャンでは得られないため）。ルールは構文から読んだコールグラフを使います。

depgraph はコードに書かれていることだけを読み、推測しません。実装が複数あるインターフェース経由の呼び出しは
辿りませんし、分類できない import は違反として報告せず「未分類」として一覧にします。事実でない検出結果は
バグとして扱います。

## グラフを保存する

`pkg/infrastructure/repository/graph` が `FileGraph` を Neo4j に書きます。**更新の単位は常にファイル 1 つ**です。

1. 1 トランザクションの中で、ファイルのノードを MERGE し、古いシンボルと import エッジを消し、新しいものを
   作ります（`UpsertFile`）。
2. パッケージ間の `IMPORTS` エッジは、変更のあったパッケージについてファイル単位の import から導出し直します
   （`RebuildPackageImports`）。
3. ファイルの無くなったパッケージと、誰も import しなくなった外部パッケージを削除します。

これが `watch` の差分更新の仕組みです。fsnotify のイベントをデバウンス（400 ms）し、変更されたファイルだけを
解析し直して置き換え、その結果に対してルールを評価します。フルスキャンに戻るのは 2 つの場合だけです。
import の解決が依存するファイル（`go.mod`・`tsconfig.json`・`package.json` など）が変わったときと、起動時の
最初のスキャン（depgraph が止まっている間に消されたファイルもここで取り除きます）です。

**ファイル単位の事実と、そこから作るリンクグラフ。** あるファイルが他のファイルの何を使っているか
（`uses`・`calls`・`loads`・`typeUses` など）は、まずそのファイルに書かれているとおりに、自分のノードの
リストプロパティとして保存します。もしこれを最初から「このファイルから他ファイルのシンボルへのエッジ」に
すると、**相手側の**ファイルが書き換わるたびにエッジが消え、戻すには import しているファイルすべてを
解析し直す必要があります。ファイル単位のプロパティなら、他のファイルに触れずに 1 ファイルを置き換えられます。

ルールを評価する前に、これらの名前をプロジェクト全体で解決し、ノードとリレーションとして保存します。
これが**リンクグラフ**です（[スキーマ](#グラフスキーマ)）。リンクは名前を解決するだけです（呼び出しがどの関数に
届くか、import がどのファイルを読み込むか、フィールドの型は何か）。ルールが報告する内容は何も決めません。
リンクはファイルか設定が変わったときにだけやり直し、保存済みのものとの差分だけを書き込みます。あとから
作られたファイルも、既にそれを import していたファイルを解析し直すことなく見つかります。

ソースファイルには書かれていない事実もあります。マニフェストが宣言する依存パッケージと、設定ファイル・scripts・
インストール済みパッケージがそれを使っているかどうかです。これらはグラフをリンクするときにプロジェクトの
ファイルから読み、`Dependency` ノードとして保存します。そのためルール自体はクエリのままです（`unused-dependency`）。

1 つのデータベースに複数のリポジトリが入ります。すべてのノードがプロジェクト key を持ち、すべてのクエリが
それで絞られます（[Projects](neo4j.md#projects) 参照）。

## ルールの評価方法

`rules.Repository.Evaluate`（`pkg/infrastructure/repository/rules`）は、リンクグラフを最新にしてから、有効な
ルールをすべて実行します。**どのルールも、グラフに対する Cypher クエリです。** Go がやるのは、リンク時の名前
解決と、クエリの結果行を検出結果の文面にすることだけです。

| ルール | クエリの内容 |
|---|---|
| `import-cycle` | パッケージ間の import ごとに、最短の戻り道を探す（`allShortestPaths`、8 パッケージまで） |
| `module-cycle` | 同じことを `(Module)-[:MODULE_IMPORTS]->(Module)` に対して行う。`import-cycle` が報告済みの循環は除く |
| `file-cycle` | 同じことを `(File)-[:LOADS]->(File)` に対して行う。まずロード時の読み込みだけで、次に関数内の import でしか閉じない循環を探す |
| `function-cycle` | 同じことを `(Function)-[:CALLS]->(Function)` に対して、Go パッケージ内の別ファイル間の呼び出しについて行う。1 つの型のメソッドだけで閉じる循環は除く |
| `pass-through` | 誰からも転送されない関数から始まり、何か別のことをする最初の関数で終わる、2 本以上の `FORWARDS` の経路 |
| `wide-function` | 関数が `CALLS` で呼ぶ関数（コンストラクタを除く）のパッケージと、メンバー経由で呼ぶ型のパッケージ（`CALLS_INTO`）を、重複なく数える |
| `layer-violation` | 禁止ペアすべてを一度に。パッケージ間の import をレイヤーの正規表現と照合する。外部 import は分類（class）で判定 |
| `external-unclassified` | サードパーティだと分かればワイルドカードのレイヤーに一致するのに、分類の付いていない外部 import |
| `concrete-dependency` | abstract ペアをまたいで実装を使っている箇所（`(Use)-[:OF]->(Type)`）と、その型が実装していて呼び出しをすべて覆う最小のインターフェース |
| `type-leak` | abstract ペアをまたいで struct / class を公開している箇所 |
| `duplicate-code` | `(Symbol)-[:SIMILAR]->(Symbol)` がつなぐグループ。SIMILAR 自体も Cypher が書く（伏せ字化したハッシュが同じ、または珍しい指紋を共有し、全指紋の Jaccard 類似度が閾値以上） |
| `duplicate-symbol` | 複数ファイルで宣言された exported な名前（集約） |
| `god-package` | fan-in と fan-out（集約） |
| `low-cohesion` | 単位の中で `(Part)-[:LINKS]-(Part)` がつなぐグループ。それぞれが外から使われ、2 つのグループを使う側が無いもの |
| `unreachable-file`・`unreachable-package` | エントリポイントからの 1 回の探索で届くものに印を付け、届かないものを「誰も import していないファイル / パッケージ」の下にまとめる |
| `unused-export` | `USES_NAMES` / `USES_GO` で名前を挙げられていない export。エントリポイントと到達不能ファイルは除く |
| `unused-dependency` | 使用の記録がなく、どのファイルからも import されていない `Dependency` ノード。scopes のいずれかに属するファイルを通る `(Dependency)-[:PROVIDES {scopes}]->(Package)<-[:IMPORTS]-(File)` がなく、gem の場合はその `constants` を `extConstants` に持つ Ruby ファイルもないもの |

レイヤーと `ignore` は `.depgraph.yml` の glob です。これを一度だけ正規表現に変換します。変換結果は Go（RE2）
でも Cypher（`=~`、Java の正規表現）でも同じ意味になるので、Go のユニットテストとクエリとで、同じパターンが
同じパッケージに一致します。

循環は常に同じ形で報告します。ある import を通る最短の循環が複数あるときは、key の並びが最小のものを
選びます。`shortestPath` だけではそのうちのどれかが返るため、同じグラフでも実行のたびに違う循環が報告
されていました。

## 検出結果を届ける

検出結果（Finding）は、ルール・severity・メッセージ・安定した識別子を持ちます。そのため 2 回の評価を比べて、
何が増え、何が解消されたかが分かります。

| 届け先 | 方法 |
|---|---|
| `CLAUDE.md` | `scan`・`watch`・`check` が `<!-- depgraph:start -->` 〜 `<!-- depgraph:end -->` の区間を書き換えます。手書きの部分は残します |
| hook（`depgraph hook`） | スキャンはせず、時間制限付き（3 秒）。保存済みのグラフに対してルールを評価し、セッション開始時は要約を、以後はそのセッションの前回プロンプトからの差分だけを出力します。セッションごとの前回の検出結果は Neo4j（`HookSession`）に置き、リポジトリには書きません |
| gate（`depgraph gate`） | 変更されたファイルを自分でグラフに反映し（`watch` のデバウンスと競合しないため）、評価して、セッションが基準（baseline）から増やした検出結果があれば失敗します。git worktree は別プロジェクトとして扱い、worktree の基点コミットからリース付きでグラフを作ります |
| ブラウザ UI（`watch`・`serve`） | UI をバイナリに埋め込んだ HTTP サーバー。`watch` は評価のたびに SSE で送ります |
| MCP App（`depgraph mcp`） | 同じ UI を 1 ファイルの HTML にしたもの。MCP のツール経由でグラフを読みます |
| CI | `scan` / `check --fail-on <severity>` が exit 1 を返します |

詳細: [コーディングエージェント連携](agents.md)、[ブラウザ UI と MCP App](ui.md)、[コマンド](commands.md)。

## グラフスキーマ

スキャンがファイル単位で書き込むもの:

```
(Project     {key, name, root, origin, base, worktree, lastScanAt, commit, dirty, baseCommit, syncUntil})
(Package     {project, key, name, external})
(File        {project, path, language, lines, updatedAt,
              scope, entry, internal, generated, uses, exports, keep, mentions,  # そのファイルが使うもの・export するもの
              loads, calls, ownMembers, typeUses, constructs,
              decls, idents, declRefs, extConstants})
(Symbol      {project, file, name, kind, exported, line, endLine, language,
              tokens, cloneHash, fingerprints,                                   # 本体の「形」
              receiver, methods, abstract, data, implements, embeds})            # 型とそのメンバー
(HookSession {project, id, findings, baseline, updatedAt})

(Package)-[:CONTAINS]->(File)-[:DECLARES]->(Symbol)
(File)-[:IMPORTS {spec, class, basis}]->(Package)
(Package)-[:IMPORTS]->(Package)          # ファイル単位の import から導出
(Symbol)-[:CALLS|REFERENCES]->(Symbol)   # watch 中に LSP が変更ファイルについて追加
```

ルールの評価前に、これらの事実から解決して作るリンクグラフ:

```
(Function {key, file, name, dir, lang, line, recv, ignore})        # 呼ぶ、または呼ばれる関数
(Function)-[:CALLS {via, construct}]->(Function)                   # via: メンバーの宣言された型を経由
(Function)-[:FORWARDS]->(Function)                                 # 引数をそのまま渡すだけ
(Function)-[:CALLS_INTO]->(Package)                                # メンバーの型のパッケージ
(File)-[:LOADS {eager}]->(File)                                    # 同一ディレクトリ内。eager: ロード時
(Module {key})-[:MODULE_IMPORTS {from, to}]->(Module)                   # モノレポのモジュール
(Type {key, pkg, name, kind, abstract, data, embeds, methods})
(Type)-[:IMPLEMENTS {exact}]->(Type)
(Use {file, pkg, owner, member, role, line, exported, collection, construct, calls})-[:OF]->(Type)
(Symbol {cloneGroup})-[:SIMILAR {score}]->(Symbol)                 # 複製
(Part {unit, what, id, data, used})-[:LINKS]->(Part)               # パッケージ / モジュールの部分
(ExtImport {pkg, file, ext, lang, python, class, basis})           # Python / Ruby ファイルの外部 import
(Dependency {manifest, dir, eco, name, section, line, languages,   # マニフェストが宣言する依存パッケージ
             constants, checked, use, doubt})                      # constants: gem の定数
(Dependency)-[:PROVIDES {scopes}]->(Package)                       # それが（scopes のファイルに）提供する外部パッケージ
(Root {key: 'unused'})-[:ENTRY {why}]->(File)                      # エントリポイント
(File)-[:USES_FILE]->(File), (File)-[:GO_PKG]->(File)              # ファイルから届く先
(File)-[:USES_NAMES {names}]->(File), (File)-[:USES_GO {names}]->(Package)
(Root)-[:USES_ALL]->(File), (Root)-[:USES_ALL_GO]->(Package), (Package)-[:USES_GO_PKG]->(Package)
File.reached, File.dead, File.deadRoot, Package.dead, Package.deadRoot   # 未使用コード系ルールの印
(LinkState {project, key})                                         # リンクグラフの元になった状態
```

- `project` はプロジェクト key です。`Package` は `(project, key)`、`File` は `(project, path)`、
  `HookSession` は `(project, id)`、`Project` は `key`、リンクグラフのノードは `(project, key)` で一意です。
- `Package.key` はリポジトリルートからのディレクトリパスです（ルートは `.`）。リポジトリ外への import は
  `ext:<パッケージ>` という key の `external=true` な `Package` になります（これもプロジェクトごと。
  `elkjs/lib/elk-api` はパッケージ `ext:elkjs`）。どこにも解決できない import は `Package` もエッジも作りません。
- ファイルの `IMPORTS` エッジの `class` と `basis` は、その import がサードパーティか標準ライブラリか、
  そう判断した根拠は何かを表します（[フレームワーク](configuration.md#フレームワークレイヤーに外部パッケージ)参照）。
- `File` の usage プロパティは、そのファイル自身から分かることだけです。どのファイル / パッケージのどの名前を
  使うか（`uses`）、別名で export している名前（`exports`）、意図して残す名前（`keep`: Python の `__all__`、
  デコレータ付き定義、STI のサブクラス）、生成コード / vendored か、なぜエントリポイントか（`entry`）。
  ファイルと一緒に置き換わります。
- `Project.syncUntil` は、worktree のプロジェクトをバックグラウンドで作るプロセスが持つリースです。

## import の解決

import は各エコシステム自身の解決方法で、プロジェクトの設定を読んで解決します。

| 言語 | 方法 | 詳細 |
|---|---|---|
| TypeScript / JavaScript | esbuild の resolver: `tsconfig.json` / `jsconfig.json`、`package.json`（`exports`・`imports`・workspaces。インストール前も）、`node_modules` | [TypeScript / JavaScript](../languages/ja/typescript.md#import-の解決) |
| Go | `go.mod` / `go.work`（入れ子モジュール・`use`・`replace`） | [Go](../languages/ja/go.md#import-の解決) |
| Python | リポジトリのパッケージルート（ルート・`src/`・`pyproject.toml` / `setup.cfg` のパッケージディレクトリ）上の import システム | [Python](../languages/ja/python.md#import-の解決) |
| Ruby | リポジトリから分かるロードパス上の `require` / `require_relative` と、Zeitwerk と同じ規則での定数（アプリケーション・engine・packwerk の pack） | [Ruby](../languages/ja/ruby.md#import-の解決) |

相対パス・エイリアス・プロジェクト内モジュールが何も指していない import は、外部ではなく未解決として扱い、
依存には現れません。`watch` はこれらの設定ファイルが変わると再スキャンします。

## Neo4j の役割と、使っている Neo4j の機能

ストアは Neo4j だけです。全プロジェクトのグラフ、プロジェクトの台帳、hook のセッション状態を持ち、
3 種類の問いに答えます。ルール、UI のビュー（`Overview`・`PackageDetail`・`Stats`）、そして人が手で書く
クエリです。

depgraph は Bolt ドライバー（`neo4j-go-driver`）越しに素の Cypher だけを使います。**APOC や Graph Data Science の
プロシージャは一切呼びません。** そのため Neo4j 5 であれば何でも動きます（depgraph が起動する Community
イメージ、自前のサーバー、Aura）。

| Neo4j の機能 | depgraph での使いどころ |
|---|---|
| ラベル付きプロパティグラフ（ノード、型付きリレーション、プロパティ、リストプロパティ） | 上の[スキーマ](#グラフスキーマ) |
| 複数プロパティの一意制約（`REQUIRE (p.project, p.key) IS UNIQUE`）と range インデックス | 1 つのデータベースに複数プロジェクトを入れる。2 つのプロセスが同時にスキャンしても同じパッケージが二重にできない |
| `MERGE ... ON CREATE SET` | パッケージ・ファイル・プロジェクト・セッションの冪等な upsert |
| `SET f += $map`（値が `null` のプロパティは削除される） | ファイルの usage プロパティ全部を 1 文で置き換える |
| `UNWIND $list` | ファイルのシンボル / import をまとめて書く。レイヤーの禁止ペアをまとめて評価する |
| `DETACH DELETE` | ファイルを、シンボルとすべてのエッジごと一度に消す |
| リトライ付きのマネージドトランザクション（`ExecuteWrite`） | ファイルの置き換えがアトミックになり、読む側が書きかけを見ない |
| 経路上のノードとリレーションに条件を付けた `allShortestPaths((b)-[:IMPORTS*1..7]->(a))` | 循環の 4 ルール |
| 終点を `DISTINCT` で受ける可変長パターン（プランナーが各ノードを 1 回だけ辿る） | エントリポイントから届く範囲、複製のグループ、単位の部分 |
| `CALL { ... UNION ... }`・`COLLECT { ... ORDER BY ... }`・`COUNT { ... }` のサブクエリ、`EXISTS { ... }` | `wide-function`、行の中での整列済みリスト、`unused-export` |
| パス関数・リスト関数（`nodes(p)`、リスト内包、スライス、`reduce`、`range`） | 循環を「最小の key から始まる形」に回転し、同じ循環を 1 回だけ報告する |
| リストに対する述語（`any` / `none` / `all`）と正規表現（`=~`） | レイヤー・`ignore`・名前パターンをクエリの中で照合する |
| 集約（`count(DISTINCT ...)`・`collect`）と `OPTIONAL MATCH` | fan-in / fan-out、同名の宣言、ファイルとそのシンボルの読み出し |
| パターン内包 | 統計と UI の全体図を 1 往復で取る |
| `UNION` | リポジトリ内と外部パッケージのレイヤー違反を 1 クエリで |
| 時刻型（`datetime()`） | `updatedAt`、古いセッションの削除、リース `syncUntil` |
| 条件付き書き込み（`MERGE ... WITH p WHERE p.syncUntil < $now SET ...`） | 1 プロセスしか取れないリース |

### 例: `import-cycle`

このルールは次のクエリがすべてです（`pkg/infrastructure/repository/rules/implement.go`）。

```cypher
// リポジトリ内の、ignore されていないパッケージ間の import a -> b すべてについて
MATCH (a:Package {project: $project})-[:IMPORTS]->(b:Package {project: $project})
WHERE a.external = false AND b.external = false AND a <> b
  AND none(rx IN $ignore WHERE a.key =~ rx OR b.key =~ rx)
// b から a への最短の戻り道が、その import を通る最短の循環を閉じる
MATCH p = allShortestPaths((b)-[:IMPORTS*1..7]->(a))
WHERE all(x IN nodes(p) WHERE x.external = false AND none(rx IN $ignore WHERE x.key =~ rx))
// 最短の戻り道が複数あるときは、常に同じもの（最小のもの）を選ぶ
WITH a, b, [n IN nodes(p) | n.key] AS back ORDER BY back
WITH a, b, head(collect(back)) AS back
// 循環を key のリストにし、最小の key から始まるよう回転して、重複を除く
WITH [a.key] + back AS cyc
WITH cyc[0..size(cyc)-1] AS ring
WITH ring, reduce(m = 0, i IN range(0, size(ring) - 1) | CASE WHEN ring[i] < ring[m] THEN i ELSE m END) AS s
WITH ring[s..] + ring[..s] AS r
WITH DISTINCT r + [r[0]] AS cyc
RETURN cyc ORDER BY size(cyc), cyc LIMIT 50
```

最初の実装はすべての循環を列挙していました（`(a)-[:IMPORTS*2..8]->(a)`）。これは指数時間で、700 ファイルの
リポジトリで 17 秒かかり、hook の持ち時間 3 秒を大きく超えていました。import ごとに最短経路を 1 本だけ
求める方法は多項式時間で（40 パッケージ・280 import の密なグラフで 0.17 秒）、8 パッケージ以下の循環に乗る
import はすべて報告できます。GQL の `SHORTEST` ではなく `allShortestPaths` を使っているのは、Neo4j 5.26 の
プランナーがこのクエリでは `SHORTEST` を約 500 倍遅く実行したためです。

### 例: `layer-violation`

禁止ペアすべてを 1 クエリで評価します。各ペアが、2 つのレイヤーの正規表現を持っています。

```cypher
UNWIND $pairs AS pr
MATCH (a:Package {project: $project})-[:IMPORTS]->(b:Package)
WHERE a.external = false AND b.external = false
  AND NOT any(rx IN $ignore WHERE a.key =~ rx OR b.key =~ rx)
  AND any(rx IN pr.fromSet.rx WHERE a.key =~ rx)       // a は import する側のレイヤーにいる
  AND any(rx IN pr.toSet.rx   WHERE b.key =~ rx)       // b は禁止されたレイヤーにいる
  AND NOT any(rx IN pr.toSet.rx   WHERE a.key =~ rx)   // かつ、その import がペアをまたいでいる
  AND NOT any(rx IN pr.fromSet.rx WHERE b.key =~ rx)
RETURN DISTINCT a.key AS from, b.key AS to, pr.from AS fromLayer, pr.to AS toLayer
```

（抜粋です。実際のクエリは除外パターン、コンポーネント、外部パッケージを含むレイヤーも扱います。）

### 例: `pass-through`

引数をそのまま次へ渡すだけの関数の連なりは、`FORWARDS` の経路です。

```cypher
MATCH (head:Function {project: $project})-[:FORWARDS]->(:Function)
WHERE NOT (:Function)-[:FORWARDS]->(head)          // 先頭には誰も転送していない
MATCH p = (head)-[:FORWARDS*2..]->(last:Function)  // 転送するだけの関数が 2 つ以上
WHERE NOT (last)-[:FORWARDS]->(:Function)          // 最後の関数は別のことをする
RETURN [n IN nodes(p) | [n.file, n.name]] AS chain
ORDER BY head.file, head.name LIMIT 50
```

## なぜ Neo4j なのか

ストアは最初のコミットから Neo4j です。この選択の根にある原則は、**ルールは Cypher クエリである**という
ことです。理由を、重要な順に挙げます。

1. **データがグラフで、ルールが経路の問いだから。** 「この import は循環に乗っているか、最短の
   循環はどれか」「このレイヤーのパッケージが、あのレイヤーのパッケージに届いているか」「このパッケージに
   誰が依存しているか」は、パターンと経路の問いです。Cypher ならクエリが問いと同じ形になり、最短経路探索は
   データベースの機能であって、自分で保守するコードではありません。
2. **グラフを「報告する」だけでなく「調べられる」から。** depgraph が提供するのは検出結果の一覧だけでは
   ありません。検出結果に納得がいかないとき、Neo4j Browser を開いてグラフに直接「なぜ」を聞けます
   （[手でクエリを書く](#手でクエリを書く)）。エージェントがグラフを直接問い合わせる MCP ツールは
   [ロードマップ](development.md#roadmap)にあります。
3. **複数のプロセスが共有するサーバーだから。** エージェントが作業している間、`watch` がグラフを書き、hook が
   プロンプトごとに読み、gate が編集のたびにファイルを反映して読み、バックグラウンドのプロセスが worktree の
   グラフを作り、`serve` / `mcp` が表示します。これらは別々のプロセスで、別のディレクトリから起動されることも
   あり、コンテナのラベルで同じストアを見つけます。制約とトランザクションが同時書き込みを安全にし、
   リポジトリの中には何も書かずに済みます。
4. **ファイル単位の置き換えを素直に書けるから。** `MERGE`・`DETACH DELETE`・`UNWIND` で、外部キーの管理を
   せずに、ファイルのサブグラフを 1 トランザクションで置き換えられます。

## なぜ SQLite ではないのか

SQLite を採らなかった理由は 1 つです。**ルールを Cypher で書くため**です。ルールとはグラフに対する問い
（経路、パターン、近傍）であり、depgraph はその問いをグラフ用のクエリ言語で書きたいと考えています。そうすれば
ルールを読み、変え、保存済みのグラフに対して手で試せます。SQLite にはそのような言語が無く、どのルールも
再帰 SQL かアプリケーションコードになります。**SQLite でもこのデータは保存できます。** できないのは、ルールを
本来書きたい形で実行することです。下の表は、それぞれの選択が実際に何を意味するかを示します（このリポジトリには
ベンチマークできる SQLite 実装が無いので、設計の比較です）。

| | Neo4j（現在） | SQLite |
|---|---|---|
| 各 import を通る最短の循環を、経路上のノードに条件を付けて求める | `shortestPath` 句 1 つ | 再帰 CTE。経路を列挙する書き方は、`import-cycle` の最初の実装と同じ指数時間の探索になる。多項式時間にするには、幅優先探索と経路の復元を SQL で手書きするか、探索を Go に移す必要がある |
| エッジのパターン（`(レイヤー a)-[:IMPORTS]->(レイヤー b)`、fan-in） | パターン 1 つ | エッジテーブルの JOIN。書けるし、速い |
| glob パターンをクエリの中で照合する | `=~` が組み込み | `REGEXP` は組み込まれていない。アプリケーションが関数を登録する |
| リストプロパティ（`fingerprints`・`uses`・`calls`） | ネイティブのリスト | JSON 列か子テーブル |
| 1 ファイルの置き換え | `DETACH DELETE` + `MERGE` | 外部キーの CASCADE 付き `DELETE`。書ける |
| 複数プロセスの同時利用（`watch`・hook・gate・`serve`） | サーバー。読み書きとも並行 | 書き込みは同時に 1 つ。ローカルディスク上の WAL モードとリトライで動く |
| データの置き場所 | ラベルで見つけるコンテナ。リポジトリには何も置かない | ファイル。全プロセスが同じパスを知る必要があり、リポジトリの外に置いて depgraph が後片付けする |
| グラフを見る | Neo4j Browser、Cypher | ノード表とエッジ表への SQL |
| ビルド | Bolt ドライバーは pure Go | cgo 不要のドライバーがあるので `CGO_ENABLED=0` は保てるが、大きな依存を 1 つ抱える |
| ユーザーに必要なもの | コンテナランタイム、約 1 GB のイメージ、起動に 10〜20 秒、メモリ 330〜480 MiB | なし |

つまりトレードオフはこうです。SQLite にすれば、コンテナランタイムと起動時間が不要になります。これは depgraph の
最大のコストです（[次節](#neo4j-のコスト)）。その代わり、いまは Cypher クエリ 1 本であるすべてのルールが、
再帰 SQL かアプリケーションコードになり、グラフは人やエージェントがグラフ用のクエリ言語で調べられるものでは
なくなります。

この判断はコードによって固定されてはいません。ストアはインターフェース
（`graph.Repository`・`rules.Repository`・`view.Reader`・`project.Repository`、hook の `SessionStore`）の
向こう側にあり、ドメインのロジックはドライバーを import しません（このリポジトリの `.depgraph.yml` が禁止して
います）。別のストアは、これらのインターフェースのもう 1 つの実装であり、現在の実装の出力をゴールデンとして
一致を確認することになります。

## Neo4j のコスト

この選択には代償があり、いくつかの Pull Request はその支払いでした。

| コスト | 対応 |
|---|---|
| 最初のスキャンの前に、Neo4j をインストールするか docker compose で起動する必要があった | depgraph 自身が Neo4j をコンテナ（docker・podman・nerdctl）で起動する。既定ではエフェメラルで、depgraph がどう終了しても watchdog プロセスがコンテナを消す（[Neo4j ストア](neo4j.md)） |
| それでもコンテナランタイムは必要。イメージは約 1 GB、起動に 10〜20 秒 | メモリに上限を設定（ヒープ 512 MiB、ページキャッシュ 64 MiB）。インストール済みの Neo4j を子プロセスで動かす `local` プロバイダーが[ロードマップ](development.md#roadmap)にある |
| 循環の列挙が指数時間だった | import 1 本ずつ最短経路を求める（[上記](#例-import-cycle)） |
| `shortestPath` は複数ある最短経路のどれかを返すので、実行のたびに循環が変わった | `allShortestPaths` のうち最小のものを選ぶ |
| 大きなプロジェクトを 1 トランザクションで削除すると、トランザクションのメモリ上限を超えた（14,000 ファイルのリポジトリ） | 10,000 件ずつのバッチで削除する。先にリレーション、次にノード（1 つのノードが持つリレーション数に上限はないので、ノードをリレーションごとまとめて消すバッチは大きさが決まらず、大きなリポジトリのリンク済みグラフではそれでも上限を超えた） |
| `IF NOT EXISTS` は、同じ制約を同時に作る 2 つのセッションを守らない | `EnsureSchema` が「既に存在する」を成功として扱う |
| `collect()` は順序を保証しないので、実行のたびにメッセージが変わった | 検出結果を作る前に Go でリストを整列する |

## コードの構成

コードは、自身の [`.depgraph.yml`](../../.depgraph.yml) が宣言するレイヤーに従っていて、depgraph がそれを
自分自身に対して検査しています。`domain` は他のレイヤーもサードパーティのパッケージも import せず、
`services` は `infrastructure` をインターフェース経由でしか使いません。

```
main.go                                  エントリポイント（cmd.Execute() を呼ぶだけ）

pkg/domain/                              I/O なし、サードパーティの import なし
  graph/                                 言語非依存のグラフモデル、Repository / Resolver interface
  rules/                                 ルールの登録、Config（レイヤー・glob）、Finding と差分、Repository interface
  project/                               Project、key の決定、移動 / 衝突の判定、Repository interface
  view/                                  UI が読むもの（全体図、パッケージ詳細、統計）

pkg/services/analysis/                   ユースケース: スキャン、差分適用、ルール評価、LSP による精密化

pkg/infrastructure/
  resolver/                              リポジトリ走査 + 除外パターン
    treesitter/                          ファイル単位の構造解析（gotreesitter）
    imports/                             エコシステムごとの import 解決（esbuild、go.mod / go.work、Python、Ruby）
    deps/                                マニフェスト（go.mod、package.json、pyproject.toml / requirements、Gemfile）が宣言する依存パッケージと、import 以外の使われ方
    lsp/                                 言語サーバー（callHierarchy / references）による精密化
  repository/graph/                      Neo4j: 書き込み、スキーマ、UI のビュー
  repository/rules/                      Neo4j: リンクとルール（Cypher）
  repository/project/                    Neo4j: Project ノード、リース
  repository/hooksession/                Neo4j: hook のセッション状態
  neo4jprovider/                         Neo4j の用意の仕方: Provider の契約、選択、external
    container/                           docker / podman / nerdctl のコンテナの Neo4j（ライフサイクル、ラベル、watchdog）
  config/                                .depgraph.yml（厳格なデコード、モノレポのサブディレクトリの設定、JSON Schema）
  workspace/                             root のプロジェクト識別（.depgraph.yml、git の origin・worktree）
  watch/                                 fsnotify + デバウンス
  claudemd/                              CLAUDE.md の管理区間
  codexhooks/                            Codex プラグインの hook を Codex の hooks.json に写す

pkg/interfaces/
  cmd/                                   CLI（cobra）と composition root
  handler/ui/                            HTTP サーバー + SSE + 埋め込み UI
  handler/mcpapp/                        MCP サーバー: UI を MCP App として提供
  handler/hook/                          hook と品質ゲート（要約、差分、セッションで増えた検出結果）

web/                                     ブラウザ UI のソース（Vite + TypeScript、Cytoscape + elkjs）。バイナリに埋め込む
plugin/                                  Claude Code と Codex のプラグイン（hook、スキル）
```

解析バックエンドは `graph.Resolver` で差し替えられます。現在は tree-sitter で、`watch` 中は LSP が精密化します。
SCIP インデックスの取り込みはロードマップにあります。

## 手でクエリを書く

[外部の Neo4j](neo4j.md#external-自分で用意した-neo4jdocker-composeサーバーaura)（このリポジトリの
`docker-compose.yml` は Neo4j Browser を http://localhost:7474 で提供します）なら、グラフを直接クエリできます。
どのクエリもプロジェクト key で絞ってください。

```cypher
// 循環依存
MATCH p=(a:Package {project: 'my-repo'})-[:IMPORTS*2..8]->(a) WHERE a.external=false
RETURN [n IN nodes(p) | n.key] AS cycle LIMIT 20;

// fan-in の上位
MATCH (p:Package {project: 'my-repo'})<-[:IMPORTS]-(q) WHERE p.external=false
RETURN p.key, count(q) AS fanIn ORDER BY fanIn DESC LIMIT 10;

// あるパッケージを誰が、どのファイルから import しているか
MATCH (a:Package {project: 'my-repo'})-[:CONTAINS]->(f:File)-[:IMPORTS]->(b:Package {key: 'pkg/domain/graph'})
RETURN a.key, collect(f.path) AS files ORDER BY a.key;
```
