// Runs elkjs in a Web Worker so layouts never block the UI thread. A newer
// request supersedes an older one: the worker is restarted and the stale
// promise rejects with Superseded.
import ELK, { type ElkNode } from 'elkjs/lib/elk-api';
import workerUrl from 'elkjs/lib/elk-worker.min.js?url';

export class Superseded extends Error {}

let elk = new ELK({ workerUrl });
let seq = 0;
let busy = false;

export async function elkLayout(graph: ElkNode): Promise<ElkNode> {
  const my = ++seq;
  if (busy) {
    elk.terminateWorker();
    elk = new ELK({ workerUrl });
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
}
