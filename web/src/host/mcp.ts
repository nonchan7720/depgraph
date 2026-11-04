// The MCP App host: the view runs in the host's sandboxed iframe (no network,
// no worker scripts), reads the graph through the depgraph MCP server's
// app-only tools and lays it out in-thread. There is no event stream: the
// "refresh" control re-runs the rules (show_dependency_graph), and so does
// the model calling that tool again (the host then sends its result here).
import ELK from 'elkjs/lib/elk.bundled.js';
import type { GraphResponse, PackageDetail } from '../lib/api';
import type { Host, Live } from '../lib/host';
import { sequenced } from '../lib/layout';
import { Bridge, graphArgs, parentPort, type HostContext } from '../lib/mcp';

// the height asked for inline; fullscreen fills the host's frame
const INLINE_HEIGHT = 680;

function applyContext(c: HostContext | undefined) {
  if (!c) return;
  const full = c.displayMode === 'fullscreen';
  const btn = document.querySelector<HTMLButtonElement>('#mcp-full');
  if (btn) {
    btn.hidden = !(c.availableDisplayModes ?? []).includes('fullscreen');
    btn.textContent = full ? 'exit fullscreen' : 'fullscreen';
    btn.dataset.mode = full ? 'inline' : 'fullscreen';
  }
}

export function mcpHost(): Host {
  const bridge = new Bridge(parentPort());
  document.documentElement.classList.add('mcp-app');
  const ready = bridge.initialize({ name: 'depgraph', version: '1' }).then((r) => {
    bridge.notify('ui/notifications/size-changed', { height: INLINE_HEIGHT });
    return r;
  });
  bridge.on('ui/notifications/host-context-changed', (c: HostContext) => applyContext(c));
  bridge.on('ui/resource-teardown', () => ({}));

  function live(l: Live) {
    const refresh = Object.assign(document.createElement('button'), {
      className: 'btn',
      textContent: 'refresh',
      title: 're-run the rules over what is in Neo4j',
    });
    refresh.onclick = async () => {
      refresh.disabled = true;
      try {
        await bridge.request('tools/call', { name: 'show_dependency_graph', arguments: {} });
        await l.update();
      } catch {
        l.lost();
      } finally {
        refresh.disabled = false;
      }
    };
    const full = Object.assign(document.createElement('button'), { className: 'btn', id: 'mcp-full', hidden: true });
    full.onclick = async () => {
      const r = await bridge.request<{ mode: HostContext['displayMode'] }>('ui/request-display-mode', { mode: full.dataset.mode ?? 'fullscreen' });
      applyContext({ displayMode: r.mode, availableDisplayModes: ['inline', 'fullscreen'] });
    };
    document.querySelector('header')!.append(refresh, full);
    bridge.on('ui/notifications/tool-result', () => void l.update());
    void ready.then((r) => {
      applyContext(r.hostContext);
      l.ready();
    });
  }

  const call = async <T>(name: string, args: Record<string, unknown>) => {
    await ready;
    return bridge.callTool<T>(name, args);
  };
  return {
    graph: (q) => call<GraphResponse>('graph_view', graphArgs(q)),
    pkg: (key) => call<PackageDetail>('package_detail', { key }),
    live,
    layout: sequenced(() => new ELK()),
  };
}
