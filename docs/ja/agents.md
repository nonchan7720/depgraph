# コーディングエージェント連携：プラグイン・hook・品質ゲート

[English](../agents.md) | 日本語 · [README](../../README_ja.md)

depgraph は検出結果を、レビューのときだけでなくエージェントの作業中に戻します。Claude Code と Codex のプラグイン、その中身である hook、そして「自分が増やした負債が残っている間はエージェントが作業を終えられない」ようにする品質ゲートがあります。

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
動かしておいてください（`PATH` 上の `depgraph` が必要です。[インストール](install.md) を参照）。下記の claude-hook-gate の
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

hook は他のコマンドと同じ Neo4j を使います（[Neo4j](neo4j.md) 参照）。`--neo4j` / `$NEO4J_URI` があれば外部の Neo4j、
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
  相対パス）。gate はこのときメインツリーではなく worktree を、別プロジェクト（`<base>@<name>`。[Projects](neo4j.md#projects) 参照）
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
