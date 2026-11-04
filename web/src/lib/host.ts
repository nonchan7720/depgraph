// What the UI needs from where it runs: the graph API, live updates and an
// ELK engine. src/host/http.ts is `depgraph serve` / `watch` in a browser
// (entry src/main.ts), src/host/mcp.ts an MCP App (entry src/mcp-app.ts).
import type { GraphQuery, GraphResponse, PackageDetail } from './api';
import type { Layout } from './layout';

export interface Live {
  /** updates will arrive */
  ready(): void;
  /** the data changed: reload */
  update(): Promise<void>;
  /** the connection was lost (it is retried) */
  lost(): void;
}

export interface Host {
  graph(q: GraphQuery): Promise<GraphResponse>;
  pkg(key: string): Promise<PackageDetail>;
  live(l: Live): void;
  layout: Layout;
}
