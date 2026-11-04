// The view side of the MCP Apps protocol (io.modelcontextprotocol/ui, spec
// 2026-01-26): JSON-RPC 2.0 over postMessage with the host that renders this
// page in a sandboxed iframe. Only what depgraph uses is implemented, so no
// SDK (and its schema libraries) is bundled into the page.
import type { GraphQuery } from './api';

export const PROTOCOL_VERSION = '2026-01-26';

/** The message channel to the host (window.parent in the browser). */
export interface Port {
  post(msg: unknown): void;
  listen(handler: (msg: unknown) => void): void;
}

export interface HostContext {
  displayMode?: 'inline' | 'fullscreen' | 'pip';
  availableDisplayModes?: string[];
  [key: string]: unknown;
}

export interface InitializeResult {
  protocolVersion: string;
  hostContext?: HostContext;
  [key: string]: unknown;
}

interface CallToolResult {
  content?: { type: string; text?: string }[];
  structuredContent?: unknown;
  isError?: boolean;
}

type Handler = (params: any) => unknown;

interface Message {
  jsonrpc: '2.0';
  id?: number | string;
  method?: string;
  params?: unknown;
  result?: unknown;
  error?: { code: number; message: string };
}

const isMessage = (m: unknown): m is Message => typeof m === 'object' && m !== null && (m as Message).jsonrpc === '2.0';

export class Bridge {
  private next = 1;
  private pending = new Map<number | string, { resolve(v: unknown): void; reject(e: Error): void }>();
  private handlers = new Map<string, Handler>();

  constructor(private port: Port) {
    port.listen((m) => void this.receive(m));
  }

  request<T = unknown>(method: string, params: unknown = {}): Promise<T> {
    const id = this.next++;
    return new Promise<T>((resolve, reject) => {
      this.pending.set(id, { resolve: resolve as (v: unknown) => void, reject });
      this.port.post({ jsonrpc: '2.0', id, method, params });
    });
  }

  notify(method: string, params: unknown = {}): void {
    this.port.post({ jsonrpc: '2.0', method, params });
  }

  /** Handles a notification or request from the host; a request is answered with the return value. */
  on(method: string, handler: Handler): void {
    this.handlers.set(method, handler);
  }

  async initialize(appInfo: { name: string; version: string }): Promise<InitializeResult> {
    const r = await this.request<InitializeResult>('ui/initialize', {
      protocolVersion: PROTOCOL_VERSION,
      appInfo,
      appCapabilities: { availableDisplayModes: ['inline', 'fullscreen'] },
    });
    this.notify('ui/notifications/initialized');
    return r;
  }

  /** Calls a tool of the MCP server through the host and returns its structuredContent. */
  async callTool<T = unknown>(name: string, args: Record<string, unknown> = {}): Promise<T> {
    return toolResult(await this.request<CallToolResult>('tools/call', { name, arguments: args })) as T;
  }

  private async receive(m: unknown): Promise<void> {
    if (!isMessage(m)) return;
    if (m.method === undefined) {
      const p = m.id === undefined ? undefined : this.pending.get(m.id);
      if (!p) return;
      this.pending.delete(m.id!);
      if (m.error) p.reject(new Error(m.error.message));
      else p.resolve(m.result);
      return;
    }
    const h = this.handlers.get(m.method);
    if (m.id === undefined) {
      h?.(m.params);
      return;
    }
    if (!h) {
      this.port.post({ jsonrpc: '2.0', id: m.id, error: { code: -32601, message: `method not found: ${m.method}` } });
      return;
    }
    try {
      this.port.post({ jsonrpc: '2.0', id: m.id, result: (await h(m.params)) ?? {} });
    } catch (e) {
      this.port.post({ jsonrpc: '2.0', id: m.id, error: { code: -32603, message: String((e as Error).message ?? e) } });
    }
  }
}

/** The structuredContent of a tool result; an error result throws its text. */
export function toolResult(r: CallToolResult): unknown {
  const text = (r.content ?? []).map((c) => c.text ?? '').join('\n');
  if (r.isError) throw new Error(text || 'tool error');
  if (r.structuredContent === undefined) throw new Error('tool result has no structured content: ' + text);
  return r.structuredContent;
}

/** The graph_view arguments for a graph query (the same fields as GET /api/graph). */
export function graphArgs(o: GraphQuery): Record<string, unknown> {
  const a: Record<string, unknown> = {};
  if (o.depth) a.depth = o.depth;
  if (o.expand?.length) a.expand = o.expand;
  if (o.ext) a.ext = true;
  if (o.q) a.q = o.q;
  if (o.focus) a.focus = o.focus;
  return a;
}

/** A port to the parent window (the host). */
export function parentPort(): Port {
  return {
    post: (m) => window.parent.postMessage(m, '*'),
    listen: (h) =>
      window.addEventListener('message', (e) => {
        if (e.source === window.parent) h(e.data);
      }),
  };
}
