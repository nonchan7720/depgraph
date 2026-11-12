# Python

[English](../python.md) | 日本語 · [README](../../../README_ja.md) · 他の言語: [Go](go.md)、[TypeScript / JavaScript](typescript.md)、[Ruby](ruby.md)

depgraph が Python のコードから何を読むか、言語ごとに意味の変わるルールが Python では何を指すかをまとめます。
ルールそのもの、オプション、`.depgraph.yml` は [README](../../../README_ja.md#rules) を見てください。

## ファイル

`.py` ファイル。

## import の解決

リポジトリから分かるパッケージルートの上で、import システムと同じ方法で解決します:

- リポジトリのルートと `src/`
- `pyproject.toml` / `setup.cfg` のパッケージディレクトリ
- import するファイルのトップレベルパッケージの親
- すべてのトップレベルパッケージの親

`__init__.py` の無いディレクトリは名前空間パッケージで、標準ライブラリを隠しません。何も指していない相対 import は、外部ではなく未解決として扱い、依存にはなりません。

`pyproject.toml`・`setup.py`・`setup.cfg` のある各ディレクトリはプロジェクトで、`module-cycle` の単位の 1 つです。

## サードパーティの判定（レイヤーの `ext:`）

`languages.python` が指すバージョンの標準ライブラリに無い import はサードパーティです。標準ライブラリは、プロジェクトの型チェッカーが持つ typeshed から取ります。外部の import は標準ライブラリかインストール済みのパッケージのどちらかで、import では `ext:sqlalchemy` がサブモジュール（`sqlalchemy.orm`）にもマッチします。どの Python のバージョンを使うかはディレクトリごとに設定します。README の [Python：`languages`](../../../README_ja.md#フレームワークレイヤーに外部パッケージ) を参照してください。

## 未使用コード

- **エントリポイント**:
  - `__main__.py`、`__main__` ガード、`conftest.py`、`test_*.py`、`tests/`。
  - マイグレーション、`setup.py` / `manage.py` / `wsgi.py` / `asgi.py`。
  - `pyproject.toml` / `setup.cfg` の scripts・entry points の対象。
  - Django プロジェクトでは、自動検出されるモジュール（`models`・`admin`・`apps`・`urls`・`views`・`wagtail_hooks` など）。
- **使用とみなすもの**:
  - `import` / `from ... import`（関数の中のものも）。
  - import を通した `module.attr` と、`from pkg import sub; sub.f`。
  - 文字列中のドット区切りのモジュール名（`"app.tasks.send"`・`"pkg.mod:attr"`）と、`importlib.import_module(f"app.plugins.{name}")`（`app/plugins` 配下のモジュールすべて）。
  - コマンドが実行するモジュール（`python -m pkg.tool`・`uvicorn app.main:app`・`celery -A app.worker`）。
- **報告するもの**:
  - `unused-export`: モジュール直下の public な（`_` で始まらない）関数とクラス。
  - `unreachable-file`: どのエントリポイントからも辿れないファイル。
- **報告しないもの**:
  - `__init__.py`（到達不能として報告しない）。
  - `__all__` の名前と、デコレータ付きの定義（フレームワークが登録するルート・fixture・タスク）。
  - dunder 名。
  - 自ファイル内でだけ使われている export。

## その他のルール

- `concrete-dependency` と `type-leak` は、クラスの属性、`__init__` の引数、引数と戻り値（アノテーション）、`T()` を読みます。クラスが継承していれば、その interface を満たすとみなします。Protocol・ABC・dataclass・pydantic のモデル・Enum は実装とみなしません。[レイヤーの内側](../../../README_ja.md#レイヤーの内側依存性逆転と型の漏洩)を参照してください。
- `low-cohesion` はモジュールをトップレベルの宣言単位で分けて見ます。
- `file-cycle` は、モジュールの `import` 文と `from ... import` 文を見ます。関数の中の import も数えます（遅延 import は循環を隠すだけで、なくしません）。`if TYPE_CHECKING:` の下の import は数えません。`from pkg import name` は、サブモジュール `pkg/name.py` があればそれを、なければ `pkg/__init__.py` を読み込むものとして扱います。報告するのは 1 つのディレクトリ内の循環だけで、ディレクトリをまたぐ循環は `import-cycle` が報告します。

## 言語サーバ

`watch` 中は、`pyright-langserver` が変更ファイルについてシンボル間の呼び出しと参照を追加します（任意）。
