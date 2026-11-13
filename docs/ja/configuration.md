# 設定（`.depgraph.yml`）

[English](../configuration.md) | 日本語 · [README](../../README_ja.md)

スキャンする root に `.depgraph.yml` を置いてアーキテクチャを宣言します。キーはすべて省略可能です。
`scan`・`check`・`watch`・`serve`・`hook` が読み、`watch` は変更されると読み直します
（`project` の変更は再起動が必要）。

エディタは JSON Schema [`schema/depgraph.schema.json`](../../schema/depgraph.schema.json)（`depgraph schema` でも出力）で
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
  wide-function: {maxPackages: 8}                    # 1 つの関数が呼び出してよいパッケージ数の上限
  file-cycle: {deferred: true}                       # 関数内 import で隠れた循環（Python）も報告する
  # duplicate-symbol: {ignore: [main, New, "Test*", "*Handler"]}  # 対象外の名前（デフォルトを置き換える）
  unused-export: {ignore: ["pkg/api/**"]}            # 報告しないパス（ライブラリの公開 API など）
  unreachable-file: {entries: ["plugins/**"]}       # depgraph から見えないエントリポイント
  # entries / ignore は unused-export・unreachable-file・unreachable-package で共通
  unused-dependency: {ignore: [tslib, "ruby:pg", "web/package.json:lodash"]}  # 報告しない依存パッケージ（名前、言語別、マニフェスト別）

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
- **名前のパターン**（`duplicate-symbol` と `unused-dependency` の `ignore`）はシンボル名・パッケージ名の全体に一致します。`*` は任意の文字列、
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
このリポジトリ自身の [`.depgraph.yml`](../../.depgraph.yml) が例になります。

## フレームワーク：レイヤーに外部パッケージ

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
| Ruby | `Gemfile` が求める gem（バンドルの `Gemfile.lock` の `DEPENDENCIES`）の名前に一致する `require` や定数（トップレベルの名前で判定：`ActiveRecord::Base` の `ActiveRecord`） | それ以外は不明。`ext:` パターンに書く価値のある gem と定数は `external-unclassified` が一覧にする（[詳細](../languages/ja/ruby.md#サードパーティの判定レイヤーの-ext)） |

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

## レイヤーの内側：依存性逆転と型の漏洩

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

## モノレポ（`components`）

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
  `ignore` / `entries` は、そのディレクトリからの相対パスです。`unused-dependency` の `ignore` は、そのディレクトリとその配下の
  マニフェストに適用されます。`components:` のパターンにマッチするディレクトリに
  専用のファイルがあれば、そちらを優先します。同じディレクトリを `components:` と専用ファイルの両方で宣言するとエラーです。
  `project` と `store` は、そのディレクトリを root にしたときだけ効きます。ルールの有効 / 無効・severity・閾値も同じで、
  これらは適用せずに警告を表示します。スキャン対象外のディレクトリ（`node_modules`・`.git`・`.depgraphignore` など）の
  ファイルは読みません。
- コンポーネントのディレクトリは root からのパスか、`*` と `?` を使ったパターンです（`**` は使えません）。
