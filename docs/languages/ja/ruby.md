# Ruby（Rails・engine・packwerk の pack）

[English](../ruby.md) | 日本語 · [README](../../../README_ja.md) · 他の言語: [Go](go.md)、[TypeScript / JavaScript](typescript.md)、[Python](python.md)

depgraph が Ruby のコードから何を読むか、言語ごとに意味の変わるルールが Ruby では何を指すかをまとめます。
ルールそのもの、オプション、`.depgraph.yml` は[ルール](../../ja/rules.md)と[設定](../../ja/configuration.md)を見てください。

Rails のコードは、他のファイルを `require` ではなく定数で参照します（Zeitwerk が `Admin::User` を `admin/user.rb` から読み込む）。そのため depgraph は定数を依存と使用として扱い、Rails やそのツールがコードを読み込む規約を知っています。

## ファイル

- Ruby のソース: `.rb`・`.rake`・`config.ru`・`Rakefile`。`views/` 配下の `.jbuilder`・`.builder`・`.ruby`（Ruby のテンプレートハンドラ）。
- ビュー: ERB・Haml・Slim は Ruby としては解析しません。コードと属性が書く定数を読みます（`<%= render CardComponent.new %>`、`%a{href: Admin::Routes.url(user)}`、`a href=X.url`）。
- 解析はしないが、書かれた定数を読むもの: 拡張子のない Ruby スクリプト（`#!/usr/bin/env ruby`）と、`config/` 配下の YAML。

## import の解決

- `require_relative` はファイルからの相対パスです。
- `require` は、リポジトリから分かるロードパスを使います。まずそのファイルのプロジェクト（`Gemfile` / `*.gemspec` のあるディレクトリ）の `lib/`・`spec/`・`test/`、次にリポジトリ内の全 gem の `lib/` です。リポジトリの外に出る `require` は辿りません。
- **定数**は、Zeitwerk と同じ規則で解決します。autoload の root は、`app/` 直下の各ディレクトリ（`assets`・`javascript`・`views` を除く）とその `concerns/`、および `lib/` です。次の場所から取ります:
  - アプリケーション
  - engine
  - packwerk の pack
  - `app/` が Rails の構成になっているディレクトリ（Discourse のプラグインなど）

  `Admin::UsersController` は、いずれかの root の下の `admin/users_controller.rb` です。書かれたレキシカルスコープの順（`Admin::UsersController::User`・`Admin::User`・`User`）に探します。Ruby や gem が定義する定数は依存になりません。

アプリケーション、各 gem / engine（`*.gemspec`）、各 packwerk の pack（`package.yml`）はモジュールで、`module-cycle` の単位の 1 つです。pack から別の pack への参照は両者の間の import になるので、`module-cycle` は pack 間の循環を検出します。packwerk 自身が検査するのは、宣言された依存だけです。

## サードパーティの判定（レイヤーの `ext:`）

`Gemfile` が求める gem（バンドルの `Gemfile.lock` の `DEPENDENCIES`：`rails`・`sidekiq`・`sidekiq/api`）の名前に一致する `require` や定数は、サードパーティです。定数はトップレベルの名前で判定します（`ActiveRecord::Base` の `ActiveRecord`）。それ以外は不明です:

- 他の gem の依存としてだけロックされた gem（`rails` 配下の `activerecord`、`json`・`net-http` のような default gem も）は、`external-unclassified` に一覧を出します。検査するなら、`ext:` パターンに名前を書いてください。
- 名前では確かめられないが gem のものらしい定数（`actionpack` の `ActionController`、`aws-sdk-s3` の `Aws`）も一覧に出します。
- Ruby 自身のものらしい定数（`Hash`・`IO`）は出しません。見分けるには、Ruby のバージョンごとの定数の一覧が要るためです。

[フレームワーク](../../ja/configuration.md#フレームワークレイヤーに外部パッケージ)を参照してください。

## 未使用コード

### 報告するもの

- `unused-dependency`: `Gemfile` の gem のうち、どのファイルも require せず、その定数をどのファイルも参照せず、誰も名前を挙げて
  いないもの。gem が何という名前で require され、どんな定数を宣言するかは、インストール済みの gem（bundle のパス、
  `GEM_HOME` / `GEM_PATH`、rbenv / asdf / mise / rvm）のうちロックされたバージョンから読みます。インストールされていない gem は
  スキップし、railtie や engine を持つ gem は使用中とみなします。`require: false` の gem は `error`、それ以外は `warn` です
  （起動時に Bundler が require するため）。[未使用の依存パッケージ](../../ja/rules.md#未使用の依存パッケージ)を参照してください。
- `unused-export`: クラス、中身のあるモジュール、クラスである定数（`Struct.new`・`Class.new`・`Data.define`）。メソッドは報告しません（コールバック・ルーティング・ビュー・`send` が名前で呼ぶため）。
- `unreachable-file`: どのエントリポイントからも辿れない Ruby ファイル。
- 他のクラスを入れるだけのモジュールは名前空間で、使う中身がないので報告しません。

### 使用とみなすもの

- **定数の名前**（どこに書かれていても）:
  - `User`・`Admin::Role`・スーパークラス・Concern の `include` / `extend`
  - 文字列とシンボル: `"Admin::User"`、`class_name: "User"` / `class_name: :Note`、`const_get(:Thing)`
  - 単語の配列: `%w[CsvImporter JsonImporter]`、`delegated_type types: %w[Message Comment]`
- **関連**: `has_many :comments` は `Comment` を使います（単数化は ActiveSupport の規則）。シンボルから作った名前は、`APIKey` のような acronym のクラスにも一致します。
- **バリデータ**: `validates :email, email: true`（`EmailValidator`）、`'gitlab/emoji_name': true`（`Gitlab::EmojiNameValidator`）。
- **実行時に組み立てる名前**:
  - `"Importers::#{kind}".constantize` は `Importers` 配下の定数すべてを使います。
  - `"#{module_parent_name}::#{kind}".constantize` は外側の名前空間の定数すべてを使います。
  - `const_get("#{name.camelize}Pipeline")` は、自分の名前空間にある `Pipeline` で終わる定数すべてを使います。
  - `has_one :"#{name}_search_data"` は `SearchData` で終わる定数すべてを使います。
  - `const_get` 以外では、末尾だけでは使用とみなしません（`"#{kind}Service".constantize` は `*Service` すべてを使うわけではない）。
- **ファイル**:
  - `require` はファイルを到達可能にします。
  - Ruby ファイルのグロブ（`Dir["lib/patches/*.rb"]`）は、そのディレクトリ配下のファイルすべてを使います。起点は、ファイル・そのプロジェクト・（再帰しないグロブなら）リポジトリのルートです。`require_rel` はファイルからの相対だけです。テストの中のグロブはファイルを走査するだけで、何も読み込みません。
  - 名前空間のファイル（`admin.rb`、`module Vacuum; end` だけのファイルも）は、配下の定数のために Zeitwerk が読み込みます。自分のクラスを入れるために名前空間を開き直しているだけのファイルは、そのためには読み込まれません。
- **ビューと設定**: ビュー、`config/` の YAML（`validator: "ColorListValidator"`）、拡張子のないスクリプトが書く定数。

### エントリポイント

Rails やツールが規約で読み込むもの:

- `app/`: `controllers`・`jobs`・`mailers`・`channels`・`helpers`・`mailboxes`・`views`・`workers`、モデル名から推論されるクラス（`policies`・`serializers`・`decorators`・`dashboards`・`inputs`・`admin`）。これらの下の `concerns/` はエントリポイントではありません。
- `config/`・`db/`・`bin/`・`script/`・`lib/tasks`・`lib/generators`・`Rakefile`・`config.ru`・`*.rake`・`sorbet/`・`locale/`（gettext）。
- テスト: `spec/`・`test/`・`*_spec.rb`・`*_test.rb`。
- スクリプト: `#!`、`if __FILE__ == $0`、`bundler/inline`。
- ツール:
  - ドットディレクトリ（`.skills/`）とドットファイル（`.active_record_doctor.rb`）
  - RuboCop / Standard / haml-lint の設定が `require` するファイル
- Discourse のプラグイン: `plugin.rb`。
- gem の `lib/` はその公開 API です。
- packwerk の pack の public path（`package.yml` の `public_path`、デフォルト `app/public/`）は到達可能です。pack を使うのはアプリケーションだけなので、public path の中で誰も使わないクラスは報告します。

### 報告しないもの

- ライブラリの名前空間を再オープンするクラス / モジュール。ライブラリは Ruby・Rails・`Gemfile` / `Gemfile.lock` の gem で、大文字小文字を問わず照合します（`omniauth` の `OmniAuth`）。たとえば `Paperclip`・`Chewy`・`ActiveRecord`・`String` です。ライブラリがこうしたクラスを名前で読み込みます。ただし、アプリケーション自身の名前空間と同じ名前の gem は数えません。`gitlab` gem があっても、`lib/gitlab.rb` があれば `Gitlab::*` はアプリケーションのものです。
- `models/` の STI サブクラス（`type` カラムから読み込まれる）。
- アプリケーションの抽象 `ApplicationRecord`。
- `Rails::Railtie`・`Rails::Engine`・generator のサブクラスと、クラス本体の DSL で登録されるクラス（`register_for 'x'`）。
- `self` を登録 / prepend するクラス・モジュール（`Object.prepend(self)`）。
- Concern の `ClassMethods`。

Concern はどこにあってもエントリポイントではないので、どこからも include されない Concern は報告します。

### Rails アプリケーションの JavaScript

Rails アプリケーションの JavaScript は、アセットツールが示す所から始まります:

- Sprockets: `app/assets/`
- jsbundling・importmap: `app/javascript/` 直下のファイル
- importmap: `config/importmap.rb` の pin と `pin_all_from` のディレクトリ
- Vite Ruby: `config/vite.json`、無ければ `app/javascript/entrypoints`・`app/frontend/entrypoints`
- Shakapacker / Webpacker: `source_path` / `source_entry_path`、無ければ `app/javascript/packs`
- Discourse のプラグイン: `assets/javascripts`（Ember のリゾルバが名前で読み込む）

## その他のルール

- `duplicate-symbol` は Ruby を対象外にします（クラスの再オープンは Ruby の拡張方法そのもののため）。
- `duplicate-code` はクラス全体ではなく、メソッド（`Class#name`・`Class.name`）を比べます。
- `concrete-dependency`・`type-leak`・`low-cohesion` は Ruby には適用しません。読める型がなく、interface はダックタイピングだけで、クラスの部分は動的ディスパッチで呼ばれるメソッドになるため、depgraph は推測しません。

## 既知の制約

- 規約のディレクトリ（controllers・helpers・serializers・policies など）のファイルはエントリポイントなので、そこにある未使用コードは報告しません。誤検出を出さないことを優先した、意図的なトレードオフです。
- それ以外の方法で組み立てた名前で読み込むコード（末尾だけの名前、データベースから取った名前）には、`# depgraph:entry` コメントか、`.depgraph.yml` の `entries:` が必要です。
- `config/initializers/inflections.rb` の acronym の設定は読みません。代わりに、シンボルから作った名前を大文字小文字を無視して照合します（関連とバリデータはこれで足ります）。
- Ruby の言語サーバは使わないので、メソッド間の呼び出しと参照は追加しません。
