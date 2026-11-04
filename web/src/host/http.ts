// The browser host: the HTTP API and event stream of the Go server, ELK in a Web Worker.
import ELK from 'elkjs/lib/elk-api';
import workerUrl from 'elkjs/lib/elk-worker.min.js?url';
import { graphQuery } from '../lib/api';
import type { Host } from '../lib/host';
import { sequenced } from '../lib/layout';

const json = async <T>(url: string): Promise<T> => (await (await fetch(url)).json()) as T;

function connect(l: Parameters<Host['live']>[0]) {
  const es = new EventSource('/events');
  es.addEventListener('hello', () => l.ready());
  es.addEventListener('update', () => void l.update());
  es.onerror = () => {
    l.lost();
    es.close();
    setTimeout(() => connect(l), 2000);
  };
}

export function httpHost(): Host {
  return {
    graph: (q) => json('/api/graph?' + graphQuery(q)),
    pkg: (key) => json('/api/package?key=' + encodeURIComponent(key)),
    live: connect,
    layout: sequenced(() => new ELK({ workerUrl })),
  };
}
