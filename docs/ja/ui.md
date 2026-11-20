# ブラウザ UI と MCP App

[English](../ui.md) | 日本語 · [README](../../README_ja.md)

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
