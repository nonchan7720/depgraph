import { describe, expect, it } from 'vitest';
import { Bridge, PROTOCOL_VERSION, graphArgs, toolResult, type Port } from './mcp';

// a host on the other side of postMessage, driven by the test
function fakePort() {
  const sent: any[] = [];
  let deliver: (m: unknown) => void = () => {};
  const port: Port = { post: (m) => sent.push(m), listen: (h) => (deliver = h) };
  return { port, sent, deliver: (m: unknown) => deliver(m) };
}

const tick = () => new Promise((r) => setTimeout(r, 0));

describe('Bridge', () => {
  it('correlates responses with requests', async () => {
    const h = fakePort();
    const b = new Bridge(h.port);
    const p1 = b.request('a', { x: 1 });
    const p2 = b.request('b');
    expect(h.sent.map((m) => [m.jsonrpc, m.method])).toEqual([['2.0', 'a'], ['2.0', 'b']]);
    expect(h.sent[0].id).not.toBe(h.sent[1].id);
    h.deliver({ jsonrpc: '2.0', id: h.sent[1].id, result: 'second' });
    h.deliver({ jsonrpc: '2.0', id: h.sent[0].id, result: 'first' });
    await expect(p1).resolves.toBe('first');
    await expect(p2).resolves.toBe('second');
  });

  it('rejects on an error response', async () => {
    const h = fakePort();
    const p = new Bridge(h.port).request('a');
    h.deliver({ jsonrpc: '2.0', id: h.sent[0].id, error: { code: -32000, message: 'nope' } });
    await expect(p).rejects.toThrow('nope');
  });

  it('dispatches notifications and ignores foreign messages', () => {
    const h = fakePort();
    const b = new Bridge(h.port);
    const got: unknown[] = [];
    b.on('ui/notifications/tool-result', (p) => got.push(p));
    h.deliver('not json-rpc');
    h.deliver({ jsonrpc: '2.0', method: 'ui/notifications/other', params: {} });
    h.deliver({ jsonrpc: '2.0', method: 'ui/notifications/tool-result', params: { content: [] } });
    expect(got).toEqual([{ content: [] }]);
  });

  it('answers requests from the host, unknown ones with method-not-found', async () => {
    const h = fakePort();
    const b = new Bridge(h.port);
    b.on('ui/resource-teardown', () => ({}));
    h.deliver({ jsonrpc: '2.0', id: 7, method: 'ui/resource-teardown', params: {} });
    h.deliver({ jsonrpc: '2.0', id: 8, method: 'nope', params: {} });
    await tick();
    expect([...h.sent].sort((a, b) => a.id - b.id)).toEqual([
      { jsonrpc: '2.0', id: 7, result: {} },
      { jsonrpc: '2.0', id: 8, error: { code: -32601, message: 'method not found: nope' } },
    ]);
  });

  it('initializes, then tells the host it is ready', async () => {
    const h = fakePort();
    const b = new Bridge(h.port);
    const p = b.initialize({ name: 'depgraph', version: '1' });
    expect(h.sent[0]).toMatchObject({ method: 'ui/initialize', params: { protocolVersion: PROTOCOL_VERSION, appInfo: { name: 'depgraph' } } });
    h.deliver({ jsonrpc: '2.0', id: h.sent[0].id, result: { protocolVersion: PROTOCOL_VERSION, hostContext: { displayMode: 'inline' } } });
    await expect(p).resolves.toMatchObject({ hostContext: { displayMode: 'inline' } });
    expect(h.sent[1]).toEqual({ jsonrpc: '2.0', method: 'ui/notifications/initialized', params: {} });
  });

  it('calls a server tool through the host and returns its structured content', async () => {
    const h = fakePort();
    const p = new Bridge(h.port).callTool('graph_view', { depth: 2 });
    expect(h.sent[0]).toMatchObject({ method: 'tools/call', params: { name: 'graph_view', arguments: { depth: 2 } } });
    h.deliver({ jsonrpc: '2.0', id: h.sent[0].id, result: { content: [], structuredContent: { root: '/r' } } });
    await expect(p).resolves.toEqual({ root: '/r' });
  });
});

describe('toolResult', () => {
  it('throws the text of an error result', () => {
    expect(() => toolResult({ isError: true, content: [{ type: 'text', text: 'key required' }] })).toThrow('key required');
  });
  it('throws when there is no structured content', () => {
    expect(() => toolResult({ content: [{ type: 'text', text: 'x' }] })).toThrow(/structured/);
  });
});

describe('graphArgs', () => {
  it('drops empty fields', () => {
    expect(graphArgs({ depth: 0, expand: [], ext: false, q: '', focus: '' })).toEqual({});
    expect(graphArgs({ depth: 2, expand: ['a'], ext: true, q: 'x', focus: 'f' })).toEqual({ depth: 2, expand: ['a'], ext: true, q: 'x', focus: 'f' });
  });
});
