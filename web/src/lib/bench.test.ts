import { describe, expect, it } from 'vitest';
import { synthView } from './bench';

describe('synthView', () => {
  it('builds n leaf nodes in expanded groups with acyclic edges between existing nodes', () => {
    const v = synthView(200, 20);
    const leaves = v.nodes.filter((n) => !n.group);
    expect(leaves).toHaveLength(200);
    const ids = new Set(v.nodes.map((n) => n.id));
    for (const n of leaves) expect(ids.has(n.parent)).toBe(true);
    for (const e of v.edges) {
      expect(ids.has(e.from) && ids.has(e.to)).toBe(true);
      expect(e.from < e.to).toBe(true); // index order -> acyclic
    }
    expect(v.edges.length).toBeGreaterThan(200);
  });
  it('is deterministic', () => {
    expect(synthView(50, 10)).toEqual(synthView(50, 10));
  });
});
