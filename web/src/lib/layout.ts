// Sequences ELK layouts: a newer request supersedes an older one (the engine
// is restarted and the stale promise rejects with Superseded). The engine is
// chosen by the host (../host): a Web Worker in the browser, in-thread in an
// MCP App, whose sandbox allows no worker script.
import type { ElkNode } from 'elkjs/lib/elk-api';

export class Superseded extends Error {}

export type Layout = (graph: ElkNode) => Promise<ElkNode>;

export interface Engine {
  layout(graph: ElkNode): Promise<ElkNode>;
  terminateWorker(): void;
}

export function sequenced(make: () => Engine): Layout {
  let elk = make();
  let seq = 0;
  let busy = false;
  return async (graph) => {
    const my = ++seq;
    if (busy) {
      elk.terminateWorker();
      elk = make();
    }
    busy = true;
    try {
      const r = await elk.layout(graph);
      if (my !== seq) throw new Superseded();
      return r;
    } catch (e) {
      if (my !== seq) throw new Superseded();
      throw e;
    } finally {
      if (my === seq) busy = false;
    }
  };
}
