# Browser UI and MCP App

English | [日本語](ja/ui.md) · [README](../README.md)

## Browser UI

- Packages are nodes, imports are arrows, laid out left to right by ELK (layered, orthogonal edges) in a Web Worker. Nodes and edges involved in cycles or layer violations are red, god packages orange
- Directories are folded into one node: click to drill down (breadcrumbs / Esc to go back), Shift+click to open it in place as a frame
- Nodes and frames can be dragged (whatever they land on is pushed aside); moved positions are remembered per view until `layout` is pressed. A minimap in the corner shows the whole graph
- Clicking a finding on the right highlights only the related nodes
- Clicking a node shows its files, symbols and importers / imports
- The `external` checkbox toggles external dependencies (`ext:*`); the search box filters
- During `watch` the graph redraws on every save. The layout is kept as long as the node set does not change
- Built from `web/` (Vite + TypeScript, Cytoscape + elkjs) into `pkg/interfaces/handler/ui/dist/` and embedded in the binary (`mise run build` runs `web:build` first). No CDN or web fonts: every dependency is pinned in `web/pnpm-lock.yaml`, and `mise run web:check-no-cdn` verifies the build loads nothing from a URL

API: `GET /api/graph` (packages, edges, findings, stats), `GET /api/package?key=...`, `GET /events` (SSE)

## MCP App

`depgraph mcp` is an MCP server (stdio) that shows the same browser UI inside the conversation of a host
that supports [MCP Apps](https://modelcontextprotocol.io/extensions/apps) (Claude, Claude Desktop, ...). Like
`serve` it reads the graph already in Neo4j: run `scan` or `watch` for the repository first.

```json
{
  "mcpServers": {
    "depgraph": { "command": "depgraph", "args": ["mcp", "--root", "/path/to/repo"] }
  }
}
```

(Claude Code: `claude mcp add depgraph -- depgraph mcp --root /path/to/repo`)

| tool | visible to | what it does |
|---|---|---|
| `show_dependency_graph` | model, app | re-runs the rules, answers a summary of the findings and opens the graph view (`ui://depgraph/graph.html`) |
| `graph_view` | app | the folded graph, same JSON as `GET /api/graph` |
| `package_detail` | app | one package, same JSON as `GET /api/package` |

- The view runs in the host's sandbox with no network access, so it is built as one self-contained HTML
  (`vite build --mode mcp-app` into `pkg/interfaces/handler/mcpapp/dist/`, embedded like the browser UI) and
  reads the graph through the app-only tools, which the host proxies to the server. ELK runs in the page
  instead of a Web Worker (the sandbox allows no worker script)
- There is no event stream: `refresh` in the header re-runs the rules and reloads, and so does the model
  calling `show_dependency_graph` again. `fullscreen` appears when the host offers it
- Logs go to stderr; stdout carries the protocol
