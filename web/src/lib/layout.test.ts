import { describe, expect, it } from 'vitest';
import type { ElkNode } from 'elkjs/lib/elk-api';
import { sequenced, Superseded, type Engine } from './layout';

function engines() {
  const made: { resolve: (n: ElkNode) => void; terminated: boolean }[] = [];
  const make = (): Engine => {
    const e = { resolve: (_: ElkNode) => {}, terminated: false };
    made.push(e);
    return {
      layout: (g) => new Promise<ElkNode>((r) => (e.resolve = () => r(g))),
      terminateWorker: () => (e.terminated = true),
    };
  };
  return { made, make };
}

describe('sequenced', () => {
  it('lays out with one engine while idle', async () => {
    const { made, make } = engines();
    const layout = sequenced(make);
    const p = layout({ id: 'a' });
    made[0].resolve({ id: 'a' });
    await expect(p).resolves.toEqual({ id: 'a' });
    const q = layout({ id: 'b' });
    made[0].resolve({ id: 'b' });
    await expect(q).resolves.toEqual({ id: 'b' });
    expect(made).toHaveLength(1);
  });

  it('restarts the engine for a newer request and rejects the stale one', async () => {
    const { made, make } = engines();
    const layout = sequenced(make);
    const old = layout({ id: 'old' });
    const cur = layout({ id: 'new' });
    expect(made[0].terminated).toBe(true);
    made[0].resolve({ id: 'old' });
    made[1].resolve({ id: 'new' });
    await expect(old).rejects.toBeInstanceOf(Superseded);
    await expect(cur).resolves.toEqual({ id: 'new' });
  });
});
