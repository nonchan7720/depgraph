# ルール

[English](../rules.md) | 日本語 · [README](../../README_ja.md)

depgraph が報告する内容です。どのルールも [`.depgraph.yml`](configuration.md) で無効化や severity の変更ができます。各言語でルールが何を意味するかは言語別ページ（[Go](../languages/ja/go.md)、[TypeScript / JavaScript](../languages/ja/typescript.md)、[Python](../languages/ja/python.md)、[Ruby](../languages/ja/ruby.md)）にあります。ルールがどう評価されるかは[アーキテクチャ](architecture.md#ルールの評価方法)を見てください。

## ルール一覧

| rule | severity | 報告するもの |
|---|---|---|
| [`import-cycle`](#import-cycle) | error | パッケージ同士の循環 import |
| [`module-cycle`](#module-cycle) | error | モノレポのモジュール同士の循環 import |
| [`file-cycle`](#file-cycle) | warn | 1 つのディレクトリ内のファイル同士の循環 import |
| [`function-cycle`](#function-cycle) | info | Go の 1 パッケージ内で、別ファイルの関数同士が循環して呼び合っている |
| [`layer-violation`](#layer-violation) | error | import してはいけないレイヤーを import している |
| [`concrete-dependency`](#concrete-dependency) | warn | インターフェース経由でしか使えない相手の実装を、型が保持・生成している |
| [`type-leak`](#type-leak) | warn | レイヤーの API が、インターフェース経由でしか使えないレイヤーの型を露出している |
| [`external-unclassified`](#external-unclassified) | info | `ext:` のワイルドカードがサードパーティかどうか分類できなかった import |
| [`duplicate-code`](#duplicate-code) | warn | 名前に関係なく、同じロジックが複数箇所にある |
| [`duplicate-symbol`](#duplicate-symbol) | warn | 同名の exported な関数 / クラスが複数ファイルにある |
| [`unused-export`](#unused-export) | warn | プロジェクト内の誰も使っていない export |
| [`unreachable-file`](#unreachable-file) | warn | どのエントリポイントからも import で辿れないファイル |
| [`unreachable-package`](#unreachable-package) | warn | どのエントリポイントからも import されない Go パッケージ |
| [`unused-dependency`](#unused-dependency) | error / warn | マニフェストで宣言されているのに、誰も import せず名前も挙げていない依存パッケージ |
| [`god-package`](#god-package) | warn | ハブ：多くのパッケージから import され、自分も多くを import している |
| [`low-cohesion`](#low-cohesion) | info | 無関係な部分の寄せ集めで、部分ごとに使う側が別のパッケージ / モジュール |
| [`wide-function`](#wide-function) | info | 多すぎるパッケージの関数を呼んでいる関数 |
| [`pass-through`](#pass-through) | warn | 引数をそのまま次へ渡すだけの関数の連なり |

## 循環

### `import-cycle`

デフォルトの severity: `error`

パッケージ間の循環 import。8 パッケージ以下の循環に乗る import ごとに、それを通る最短の循環（最大 50 件）

### `module-cycle`

デフォルトの severity: `error`

モノレポのモジュール間の import 循環（Go モジュール・npm のワークスペースパッケージ・Python プロジェクト・Ruby のアプリケーション / gem / engine / packwerk の pack。`go.mod`・`package.json`・`pyproject.toml`・`setup.py`・`setup.cfg`・`Gemfile`・`*.gemspec`・`package.yml` のあるディレクトリと[コンポーネント](configuration.md#モノレポcomponents)）。ディレクトリ同士では循環にならないもの（`packages/web/src/a -> packages/kit/src -> packages/web/src/b`）も検出します。1 ホップごとに import を 1 つ示し、`import-cycle` が報告済みの循環は重ねて報告しません

### `file-cycle`

デフォルトの severity: `warn`

1 つのディレクトリ内のファイル同士の循環 import（TypeScript / JavaScript と Python のモジュール）。`import-cycle` はディレクトリ同士を比べるので、これは見えません。8 ファイル以下の循環に乗る import ごとに、それを通る最短の循環（最大 50 件）を報告します。数えるのはモジュールを読み込む import だけで、型のためだけの import（`import type`、型としてしか使われない名前、`if TYPE_CHECKING:`）と `import()` は数えません。Python で、関数の中の import によってだけ閉じる循環は、そのファイルが隠している循環として報告します（遅延 import は循環を隠すだけで、なくしません）。`deferred: false` でこれを報告しないようにできます。Go にはこの循環がなく、Ruby の定数は互いに参照し合うのが普通なので、どちらも対象外です

### `function-cycle`

デフォルトの severity: `info`

Go のパッケージ内で、別々のファイルに宣言された関数同士が循環して呼び合っている（`parse.go Parse -> expr.go expr -> parse.go Parse`）。1 つのロジックが複数のファイルに散らばっている状態です。1 ファイル内の再帰や、1 つの型のメソッドだけで閉じる循環（複数ファイルに分けて宣言された型は 1 つの単位）は報告しません。呼び出しは構文から読みます。対象は `f()`、`pkg.F()`、メソッド内の `recv.m()` です。レシーバのフィールドや引数に対するメソッド呼び出し（`s.repo.Find()`、`r.Find()`）は、そのメンバーの宣言された型でたどります。struct / クラスならそのメソッドへ、interface なら、プロジェクト内でそれを実装する型がちょうど 1 つのときだけ、その実装へたどります。実装が複数あるときは、どれが動くか分からないのでたどりません。TypeScript / JavaScript と Python では、この循環には import が必要なので、`file-cycle` か `import-cycle` として既に報告されます

## レイヤー

レイヤーは [`.depgraph.yml`](configuration.md) で宣言します。

### `layer-violation`

デフォルトの severity: `error`

Import してはいけないレイヤーを import している（`handler → repository` など、または独自レイヤー）。レイヤーには外部パッケージも書けるので、フレームワークを import するドメインも違反になる（[フレームワーク](configuration.md#フレームワークレイヤーに外部パッケージ)参照）

### `concrete-dependency`

デフォルトの severity: `warn`

インターフェース経由でしか使ってはいけないレイヤー（`abstract:`）の実装を、struct / class が保持（フィールド・コンストラクタ引数）または生成している。しかもその実装は、保持側のパッケージ・実装自身のパッケージ・内側のレイヤーで宣言されたインターフェースを満たしている（[レイヤーの内側](configuration.md#レイヤーの内側依存性逆転と型の漏洩)参照）

### `type-leak`

デフォルトの severity: `warn`

レイヤーの API（公開関数・メソッドの引数と戻り値、公開フィールド）が、インターフェース経由でしか使ってはいけないレイヤー（`abstract:`）の struct / class を露出している

### `external-unclassified`

デフォルトの severity: `info`

外部パッケージのワイルドカードのレイヤー（`ext:*`、`ext:github.com/aws/*`）が、サードパーティだと分かれば一致するのに分類できなかった import。違反として報告も、黙って捨てもせず、言語ごとに理由付きで一覧にします（[フレームワーク](configuration.md#フレームワークレイヤーに外部パッケージ)参照）

## 重複

複製の見つけ方は[重複コード](#重複コード)を見てください。

### `duplicate-code`

デフォルトの severity: `warn`

名前に関係なく、同じロジックが複数箇所にある（完全に同じ、または少し手を加えた複製。[重複コード](#重複コード)参照）

### `duplicate-symbol`

デフォルトの severity: `warn`

同名の exported function/class が複数ファイルに存在（1 つの Go パッケージのファイルは 1 か所と数える。同名が両立するのは build constraint で分けたときだけのため。Ruby は対象外: クラスの再オープンは Ruby の拡張方法そのもの）

## 未使用コードのルール

何をエントリポイント・使用とみなすかは[未使用コード](#未使用コード)を見てください。

### `unused-export`

デフォルトの severity: `warn`

プロジェクト内の誰も（自ファイルでさえ）使っていない export（Go: main 以外のパッケージの exported な関数と型。メソッドは対象外 / TS・JS: `export` された宣言。re-export・barrel を辿る / Python: モジュール直下の public な関数とクラス / Ruby: クラス、中身のあるモジュール、`Struct.new` / `Class.new` の定数。メソッドは対象外）

### `unreachable-file`

デフォルトの severity: `warn`

どのエントリポイントからも import で辿れない TS/JS/Python/Ruby ファイル。誰も import していないファイルごとに 1 件で、そのファイル経由でしか辿れないファイルを併記

### `unreachable-package`

デフォルトの severity: `warn`

`main` パッケージ・テスト・ライブラリモジュールの公開パッケージのどれからも import されない Go パッケージ

### `unused-dependency`

デフォルトの severity: 確実な指摘は `error`、確実でない指摘は `warn`

`go.mod`（直接の `require`）、`package.json`（`dependencies`・`devDependencies`・`optionalDependencies`）、`pyproject.toml` / requirements ファイル、`Gemfile` のいずれかで宣言されているのに、どのコードも import せず、ほかのファイルも名前を挙げていない依存パッケージ。何を使用とみなすか、どんなときに確実とするかは[未使用の依存パッケージ](#未使用の依存パッケージ)を見てください。

## 劣化していく構造

### `god-package`

デフォルトの severity: `warn`

ハブ：10 パッケージ以上から import され、かつリポジトリ内の 3 パッケージ以上を import している。多くから依存されてもリポジトリ内の何にも依存しないパッケージ（ドメインモデルなど）は安定しており、god package ではない（安定依存の原則）

### `low-cohesion`

デフォルトの severity: `info`

Go のパッケージ（ファイル単位）や TypeScript / Python のモジュール（トップレベルの宣言単位）が、内部で何もつながっていない部分に分かれ、しかも部分ごとに使う側が別（2 つの部分を使う側が無い）：1 か所に複数の責務がある。サイズの上限は linter の仕事で、誰がどの部分を使うかはプロジェクト全体を見て初めて分かる。テスト・生成ファイル・エントリポイント・まるごと使われる単位（dot import、`export *`、値として使われる名前空間）・データだけの単位（利用側ごとに 1 つずつ使われるエンティティ / DTO：振る舞いの無い型、データクラス、定数、コンストラクタ）は対象外

### `wide-function`

デフォルトの severity: `info`

リポジトリ内の `maxPackages`（デフォルト 8）を超える数のパッケージの関数を呼んでいる関数・メソッド（自パッケージは数えません）。プロジェクトのことを知りすぎていて、単独で変更もテストもできない関数です。フィールドや引数に対するメソッド呼び出しは、そのメンバーの型（interface）を宣言しているパッケージを数え、実装側のパッケージは数えません。コンストラクターの呼び出し（Go の `NewX`、`new C()`、Python のクラス生成）は数えません。多くのパッケージから部品を組み立てるのは配線であって、その振る舞いを知っていることにはならないためです。呼び出しは `function-cycle` と同じ方法で Go・TypeScript / JavaScript・Python について読みます。意図して広い関数（コンポジションルート）はコメントで対象外にできます（[関数を対象外にする](#関数を対象外にする-depgraphignore)）。テストコードは対象外です

### `pass-through`

デフォルトの severity: `warn`

受け取った引数をそのまま次の関数へ渡すだけの関数が 2 つ以上連なっている（`api.Get -> service.Get -> load`）。本体が、自分の引数を同じ順番でそのまま渡す 1 つの呼び出しだけで、その結果を返している関数です。名前を足すだけで何もしない層です。ラッパーが 1 つだけなら API の設計判断なので報告しません。メソッド（interface を満たすために存在することがあるため）とデコレータ付きの Python 関数は数えません。呼び出しは `function-cycle` と同じ方法で Go・TypeScript / JavaScript・Python について追います。テストコードは対象外です

## 未使用コード

`unused-export` と `unreachable-*` はプロジェクト全体を見て初めて分かるものだけを報告します。コンパイラ・`go vet` / staticcheck・`tsc --noUnusedLocals`・eslint・ruff が見るのは未使用のローカル変数や非公開の名前で、誰も import しない export は見えません。
誤検知はバグなので、import グラフの外から読み込まれる兆候があるファイルはエントリポイント（到達可能で、export は外から使われる扱い。報告しない）とします。

- 言語ごとの、エントリポイント・使用とみなすもの・報告するもの・意図して残すもの:
  - [Go](../languages/ja/go.md#未使用コード): `main` パッケージ、テスト、ライブラリモジュールの公開パッケージ。
  - [TypeScript / JavaScript](../languages/ja/typescript.md#未使用コード): `package.json` のエントリと scripts、HTML ページ、設定ファイル、テスト、ファイルベースルーティング、private なワークスペースパッケージ。
  - [Python](../languages/ja/python.md#未使用コード): `__main__`、テスト、`pyproject.toml` / `setup.cfg` の scripts・entry points、Django の自動検出されるモジュール、`__all__`。
  - [Ruby](../languages/ja/ruby.md#未使用コード): 使用としての定数（関連・ビュー・実行時に組み立てる名前も）、Rails の規約、gem、packwerk の public path、Concern、ライブラリの名前空間。
- 共通: Dockerfile・Procfile・Makefile・compose・CI ワークフロー・`mise.toml`・シェルスクリプトが実行するファイルやモジュール（`node dist/server.js`・`python -m pkg.tool`・`uvicorn app.main:app`）、文字列中のパスやドット区切りモジュール名（`new URL('./worker.ts', ...)`・`"app.tasks.send"`・`importlib.import_module(f"app.plugins.{name}")`・`` import(`./locales/${l}`) ``）、あらゆる言語の文字列中のソースファイル名（JS をバンドルする Go プログラムの `"render.js"`）。
- ヘッダで生成コードと宣言しているファイルは報告しません。

depgraph から見えない方法で読み込まれるファイル（名前で読み込むプラグイン、外部ツールが実行するファイル）には `depgraph:entry` コメント（`// depgraph:entry`・`# depgraph:entry`）を書いてください。自リポジトリでは使わないライブラリの公開 API は `.depgraph.yml` の `ignore` で除外でき、エントリポイントは `entries` で追加できます（ファイルと Go パッケージディレクトリに対するパスの glob。3 つのルールで共通。[設定](configuration.md)参照）。自ファイル内でだけ使われている export は報告しません（export は不要ですが、同じファイルで登録する Go のハンドラなど、スタイルの範囲のため）。

レイヤー・禁止 import・severity・閾値は `.depgraph.yml` で設定します（[設定](configuration.md)）。

## 未使用の依存パッケージ

`unused-dependency` は、コードを消したり書き直したりしたのにマニフェストだけが残った依存を報告します。使われないままインストールされ、監査され、更新され続けるパッケージです。コンパイラが見るのは 1 ファイルの import で、マニフェストは見ません。

`go.mod` と `package.json` では import がパッケージを正確に指します（Go の import パスはモジュールのパスで始まり、JavaScript の import はパッケージ名で始まります）。Python のディストリビューションと Ruby の gem は、マニフェストに書かれていない名前で import されます（`PyYAML` は `yaml`、`rack-cors` は `Rack::Cors`）。そこで、その名前はインストール先から読みます。`pyproject.toml`（`[project]`・`[dependency-groups]`・Poetry）と `requirements*.txt` / `requirements/*.txt` についてはリポジトリ内の仮想環境（`.venv`・`venv`）の `dist-info`、`Gemfile` についてはインストール済みの gem のファイルです。gem は Bundler と RubyGems が置く場所から、`Gemfile.lock` が固定しているバージョンのものを探します。bundle のパス（`BUNDLE_PATH`、`.bundle/config`、なければ `vendor/bundle`）、`GEM_HOME` と `GEM_PATH`、ホームディレクトリ配下の rbenv・asdf・mise・rvm の Ruby です（何も実行しません。それ以外の場所の Ruby は `GEM_PATH` で指定してください）。名前が分からない依存はスキップし、報告しません。そこにインストールされていないもの、唯一のモジュールをほかのディストリビューションと共有しているもの（`google.*`）が該当します。何もインストールされていなければ、そのマニフェストについては何も報告しません。

次のいずれかに当てはまる依存は**使用中**で、報告しません。

- ファイルが import している。import は、そのパッケージを挙げている最も近いマニフェストの宣言を使います。Go ならファイル自身の `go.mod`、JavaScript ならファイルの位置かそれより上にある最も近い `package.json` です（ワークスペースパッケージは、自分で宣言していなければ上の `package.json` がインストールしたものを使います）。import したファイルのどのマニフェストもそのパッケージを挙げていない場合は、別の何かが提供しているので（ホイストするワークスペース、`go.work`）、それを宣言しているすべてのマニフェストを使用中とみなします。Go の import は、パスが最も長く一致する require 済みモジュールのものです。
- **Go**: `go.mod` の `tool` がそのモジュールを指している。または、depgraph がスキャンしていない（除外ディレクトリの）Go ファイルが import している。`go mod tidy` と同じ判断基準です。
- **JavaScript / TypeScript**: リポジトリ内の、ドキュメントとロックファイル以外のファイルが名前を挙げている。`package.json` の scripts がバイナリを実行している（`typescript` の `tsc`）、設定ファイルが列挙している（アドオンの省略名でも可: `babel-plugin-x` に対する `"plugins": ["x"]`）、depgraph がパースしないファイルが import している（`.vue`・`.svelte`・`.scss`）、コードが文字列で指定している（`client: 'pg'`、`node_modules` 内へのパス）、CI ワークフローやフックが実行している、など。名前を挙げたファイルがどのマニフェストの依存を使ったことになるかは、import の場合と同じ規則で決まります。ほかの宣言済みパッケージが peer として必要としている場合と、`x` が使われているときの `@types/x` も使用中です。
- **Python**: ファイルがそのモジュールを import している（リポジトリのどこでも可。ファイルは 1 つの環境を共有するため）。インストール済みのほかのディストリビューションが import している（設定で選んだ DB ドライバを Django が import する場合や、バージョン固定のために書いた間接依存）。または、ファイルが名前を挙げている。文字列中のモジュール名（`INSTALLED_APPS = ['storages']`、ドット区切りのパス）や、実行されるファイル中のディストリビューション名・コマンド名（`Procfile` の `gunicorn`）です。
- **Ruby**: ファイルが require している。gem のファイルが宣言する定数を参照している（Rails のコードは gem を定数経由で使います）。または、gem 名やそのコマンド名をファイルが挙げている（`Procfile`、スクリプト、CI ワークフロー）。railtie や engine を持つ gem も使用中です（Rails が読み込むため）。

そもそも依存として数えないもの: indirect な require、`peerDependencies`、リポジトリ自身が提供するもの（リポジトリ内のモジュール・ワークスペースパッケージ、ローカルへの `replace`、`workspace:` / `file:` / `link:`）。

**severity。** 残ったものを報告し、どれだけ確かかを severity で示します。

| | 条件 | severity |
|---|---|---|
| 確実 | 誰も import せず、誰も名前を挙げず、何かが勝手に読み込む可能性もない | ルールの severity（`error`） |
| 確実でない | import 以外の使われ方を否定できない。理由を指摘に書きます | 1 段階下（`warn`） |

確実でないのは次の場合です。名前や peer からアドオンと分かるパッケージ（`eslint-plugin-x`・`@vitest/coverage-v8`・`sass-loader`: ホストが名前で読み込むことがある）、型定義（`x` が見当たらない `@types/x`）、コンパイラが出力に import を挿入するヘルパー（`tslib`・`core-js`）、そしてインストールされておらず、`node_modules` からバイナリやほかのパッケージの peer を読めなかった場合。Python のディストリビューションは、ほかのパッケージが読み込むエントリポイントを登録している場合（`pytest-cov`）と型スタブ（`types-*`）の場合に確実でなくなります。gem が確実になるのは `require: false` のときだけです。それ以外の gem はアプリケーションの起動時に Bundler が require するので、参照されなくても動作することがあります（ほかのクラスを拡張するなど）。`go.mod` の指摘はすべて確実です。`severity: warn` を設定すると、確実な指摘が `warn`、それ以外が `info` になります。

リポジトリからは見えない形で使っている依存は、`.depgraph.yml` で名前を指定して対象外にします: `unused-dependency: {ignore: [tslib, "@types/*"]}`（[設定](configuration.md)）。

コード以外のファイル（設定ファイル、`node_modules`）の内容は、グラフをリンクするとき、つまりソースファイル・マニフェスト・インストール内容のいずれかが変わったあとに読みます。

## 関数を対象外にする (`depgraph:ignore`)

意図どおりの関数単位の指摘は、その関数のある場所で、理由と一緒に対象外にします。関数の直前（空行を挟まない）か関数の中のコメントに `depgraph:ignore <rule> [<rule>...]` と書きます。ルール名の後ろの文は読む人向けです。

```go
// Wire はアプリケーションを組み立てる。
//
// depgraph:ignore wide-function (コンポジションルート: 全パッケージを意図して配線している)
func Wire(cfg Config) (*App, error) { ... }
```

```python
# depgraph:ignore wide-function, pass-through
def create_app(settings): ...
```

- 対象のルールは `wide-function` と `pass-through`（連鎖の先頭の関数）です。その他のルールはパッケージ・ファイル・使われていない宣言を報告するもので、`.depgraph.yml` で設定します。
- ルール名のないコメントは何も対象外にしません。何を無視したかは必ず書き残します。
- Go・TypeScript / JavaScript・Python で、ファイルと一緒に読み込まれます。

## 重複コード

depgraph は関数・メソッド・クラスごとに本体の「形」を持ちます。構文トークンからコメントを除き、識別子と
リテラルをすべて伏せたもので、名前を変えた複製は同じ形になります。形が同じもの、またはトークン 4-gram の
winnowing 指紋（Schleimer ら *Winnowing*）が 80% 以上一致する（Jaccard）ものを複製としてまとめるので、文を
足したり変えたりした複製も見つかります。50 トークン未満の本体、10 を超える関数に共通する指紋（どの関数にも
あるエラー処理やループ）、テストコード（`_test.go`・`*.test.*`・`*.spec.*`・`test_*.py`・`tests/` など。テーブル
テストは意図的に似ている）は対象外です。コンパイラや linter が報告しない「同じことをする、動いている 2 つの関数」を見つけるためのルールです。
