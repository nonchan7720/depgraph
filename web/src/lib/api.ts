// Types of the depgraph UI JSON API (pkg/interfaces/handler/ui).

export interface ViewNode {
  id: string;
  key: string;
  label: string;
  parent: string;
  group: boolean;
  expanded: boolean;
  external: boolean;
  context: boolean;
  members: number;
  files: number;
  symbols: number;
  bad: boolean;
  god: boolean;
}

export interface ViewEdge {
  id: string;
  from: string;
  to: string;
  weight: number;
  bad: boolean;
}

export interface View {
  depth: number;
  focus: string;
  nodes: ViewNode[];
  edges: ViewEdge[];
  nodeOf: Record<string, string>;
}

export interface Finding {
  rule: string;
  severity: string;
  message: string;
  id: string;
  data?: Record<string, unknown>;
}

export interface GraphResponse {
  root: string;
  updated: string;
  stats: Record<string, number>;
  view: View;
  findings: Finding[];
}

export interface GraphQuery {
  depth?: number;
  expand?: string[];
  ext?: boolean;
  q?: string;
  focus?: string;
}

/** Builds the query string for GET /api/graph. */
export function graphQuery(o: GraphQuery): string {
  const p = new URLSearchParams();
  if (o.depth) p.set('depth', String(o.depth));
  if (o.expand?.length) p.set('expand', o.expand.join(','));
  if (o.ext) p.set('ext', '1');
  if (o.q) p.set('q', o.q);
  if (o.focus) p.set('focus', o.focus);
  return p.toString();
}

export interface SymbolInfo {
  name: string;
  kind: string;
  exported: boolean;
  line: number;
}

export interface FileInfo {
  path: string;
  language: string;
  lines: number;
  symbols: SymbolInfo[] | null;
  imports: string[] | null;
}

export interface PackageDetail {
  key: string;
  files: FileInfo[];
  importedBy: string[];
  imports: string[];
}
