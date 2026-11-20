# TypeScript / JavaScript

[English](../typescript.md) | 日本語 · [README](../../../README_ja.md) · 他の言語: [Go](go.md)、[Python](python.md)、[Ruby](ruby.md)

depgraph が TypeScript / JavaScript のコードから何を読むか、言語ごとに意味の変わるルールがここでは何を指すかをまとめます。
ルールそのもの、オプション、`.depgraph.yml` は[ルール](../../ja/rules.md)と[設定](../../ja/configuration.md)を見てください。

## ファイル

`.ts`・`.tsx`・`.mts`・`.cts`・`.js`・`.jsx`・`.mjs`・`.cjs`。専用のコンパイラが必要なコンポーネントファイル（`.vue`・`.svelte`・`.astro`・`.marko`）は解析しません。

## import の解決

esbuild の resolver で解決します:

- `tsconfig.json` / `jsconfig.json`（`paths`・`baseUrl`・`extends`）
- `package.json`（`exports`・`imports`・workspaces）
- `node_modules`
- `x.ts` を指す ESM の `./x.js`
- バンドラのクエリはファイル名に含めない（`./worker?worker&inline` は `./worker.ts`）

ワークスペースのパッケージ（`package.json` の `workspaces`、`pnpm-workspace.yaml`）は、インストール前（clone 直後・CI・gate の worktree）でもそのディレクトリに解決します。同名のパッケージがインストールされていればそちらを優先します。

何も指していない相対パスやエイリアスは、外部ではなく未解決として扱い、依存にはなりません。プロジェクトが依存として宣言していないパッケージの import（`@site/...` のようなバンドラ / サイトジェネレータのエイリアス）は、捨てずにプロジェクト内のパスと突き合わせます。

`package.json` のある各ディレクトリはモジュールで、`module-cycle` の単位の 1 つです。

## サードパーティの判定（レイヤーの `ext:`）

`node_modules` に解決されたパッケージはサードパーティです。未インストールなら、そのファイルか上位の `package.json` が宣言しているパッケージもサードパーティです。esbuild が組み込みに解決した Node の組み込みモジュールはサードパーティではありません（`punycode` は組み込み、`punycode/` はインストール済みパッケージ）。[フレームワーク](../../ja/configuration.md#フレームワークレイヤーに外部パッケージ)を参照してください。

## 未使用コード

- **エントリポイント**:
  - `package.json`: `main` / `module` / `browser` / `types` / `bin` / `exports` と、`scripts` が実行するファイル（`dist/` のパスは `tsconfig.json` を見てソースに戻す）。
  - HTML ページ: `<script src>` と、ページテンプレートが名前で指すスクリプト。
  - 設定ファイル（`*.config.*`・`.*rc.*`）、テスト・stories・型テスト・`testdata/`・`.d.ts`。
  - `scripts/` `bin/` `tools/` `docs/` `public/` `static/`、`examples/` とベンチマーク。
  - パッケージ直下または `src/` 直下の `index` / `main` / `server` / `app` / `cli`。
  - ファイルベースルーティング: Next.js・Remix・Gatsby・Expo Router などの `app/`・`pages/`・`routes/`。
  - Docusaurus の theme コンポーネント、React Email のテンプレート、MDX が import するファイル。
  - Rails アプリケーションでは、アセットツールが起点にする JavaScript（[Ruby](ruby.md#rails-アプリケーションの-javascript) を参照）。
- **使用とみなすもの**:
  - `import` / `export ... from` / `require`。re-export・barrel を辿ります。
  - 動的 import、`require.context`、`import.meta.glob`。パスを計算する場合（`` import(`./locales/${l}`) ``、エイリアス配下の `` `@/widgets/${w}.tsx` `` も）は、そのディレクトリ配下のファイルすべてを使います。
  - `new URL('./worker.ts', import.meta.url)` と、文字列中のソースファイル名。
- **報告するもの**:
  - `unused-export`: `export` された宣言。
  - `unreachable-file`: どのエントリポイントからも辿れないファイル。
  - `unused-dependency`: `dependencies`・`devDependencies`・`optionalDependencies` のパッケージのうち、どのファイルも import せず、
    scripts・設定ファイルなどほかのファイルも名前を挙げていないもの。確実なら `error`、import 以外の使われ方を否定できない
    とき（アドオン、型定義、インストールされていないパッケージ）は `warn` です。
    [未使用の依存パッケージ](../../ja/rules.md#未使用の依存パッケージ)を参照してください。
- **報告しないもの**:
  - Vue・Svelte・Astro・Qwik・Marko・Ember・auto-import を使うパッケージ（コンポーネントファイルを解析しないので、その import が見えないため）。
  - shadcn/ui がコピーするコンポーネント群（vendored コード）。
  - 自ファイル内でだけ使われている export。

### private なワークスペースパッケージ

モノレポで、リポジトリ内の他のパッケージが依存している `private` なワークスペースパッケージは公開されないので、使うのはリポジトリだけです:

- `main` / `exports` / `types` と `index` のファイルは到達可能性の起点のままですが、export は他のパッケージが使っていなければ報告します。
- `bin` と `scripts` のファイルは外から実行されるので、従来どおり起点です。
- 公開されるパッケージ、何も依存していない private なアプリ、フレームワークがファイルを名前で読み込むパッケージ（Ember のアプリ）は、エントリの export を従来どおり使用済みとみなします。

## その他のルール

- `concrete-dependency` と `type-leak` は、クラスのフィールド、`constructor` の引数、引数と戻り値、`new T()` を読みます。クラスが implements / extends と宣言していれば、その interface を満たすとみなします。[レイヤーの内側](../../ja/configuration.md#レイヤーの内側依存性逆転と型の漏洩)を参照してください。
- `low-cohesion` はモジュールをトップレベルの宣言単位で分けて見ます。
- `file-cycle` は、ファイルがどのモジュールを読み込むかを見ます。対象は `import` 文、`export ... from`、`import x = require()`、`require()` です。ファイルの読み込み時に何も読み込まれないもの、つまり `import type` / `export type`、`type` 付きの指定、型としてしか使われていない import（TypeScript が消します）、`import()`、文字列中のパスは対象外です。報告するのは 1 つのディレクトリ内の循環だけで、ディレクトリをまたぐ循環は `import-cycle` が報告します。

## 言語サーバ

`watch` 中は、`typescript-language-server` が変更ファイルについてシンボル間の呼び出しと参照を追加します（任意）。
