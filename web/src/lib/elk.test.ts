import { describe, expect, it } from 'vitest';
import ELK from 'elkjs/lib/elk.bundled.js';
import type { ElkNode } from 'elkjs/lib/elk-api';
import { fromElk, toElkGraph } from './elk';
import { labelOf, nodeSize } from './transform';
import { node, sampleView } from './fixture';
import { synthView } from './bench';

const find = (g: ElkNode, id: string): ElkNode | undefined => {
  for (const c of g.children ?? []) {
    if (c.id === id) return c;
    const d = find(c, id);
    if (d) return d;
  }
  return undefined;
};

describe('toElkGraph', () => {
  const g = toElkGraph(sampleView(), { spacing: 3 });

  it('asks for a layered, left-to-right, orthogonal, hierarchical layout in root coordinates', () => {
    expect(g.layoutOptions).toMatchObject({
      'elk.algorithm': 'layered',
      'elk.direction': 'RIGHT',
      'elk.edgeRouting': 'ORTHOGONAL',
      'elk.hierarchyHandling': 'INCLUDE_CHILDREN',
      'elk.json.shapeCoords': 'ROOT',
      'elk.json.edgeCoords': 'ROOT',
    });
  });

  it('nests packages inside their expanded group', () => {
    expect(g.children!.map((c) => c.id).sort()).toEqual(['g:pkg/a', 'g:pkg/b', 'lib']);
    const box = find(g, 'g:pkg/a')!;
    expect(box.children!.map((c) => c.id).sort()).toEqual(['pkg/a/x', 'pkg/a/y']);
    expect(box.layoutOptions?.['elk.padding']).toMatch(/top=\d+/);
  });

  it('sizes leaves like the drawn nodes', () => {
    const b = find(g, 'g:pkg/b')!;
    const [w, h] = nodeSize(labelOf(sampleView().nodes[1]));
    expect([b.width, b.height]).toEqual([w, h]);
  });

  it('nests combos in combos', () => {
    const v = sampleView();
    v.nodes.push(node({ id: 'g:pkg/a/z', parent: 'g:pkg/a', group: true, expanded: true }), node({ id: 'pkg/a/z/1', parent: 'g:pkg/a/z' }));
    const box = find(toElkGraph(v, { spacing: 3 }), 'g:pkg/a/z')!;
    expect(box.children!.map((c) => c.id)).toEqual(['pkg/a/z/1']);
  });

  it('keeps only edges between visible leaves, at the root', () => {
    const v = sampleView();
    v.edges.push({ id: 'nope->lib', from: 'nope', to: 'lib', weight: 1, bad: false });
    const e = toElkGraph(v, { spacing: 3 }).edges!;
    expect(e.map((x) => x.id).sort()).toEqual(['lib->pkg/a/x', 'pkg/a/x->g:pkg/b', 'pkg/a/x->pkg/a/y']);
    expect(e.find((x) => x.id === 'lib->pkg/a/x')).toMatchObject({ sources: ['lib'], targets: ['pkg/a/x'] });
  });

  it('spreads things further apart with more spacing', () => {
    const a = Number(toElkGraph(sampleView(), { spacing: 1 }).layoutOptions!['elk.spacing.nodeNode']);
    const b = Number(toElkGraph(sampleView(), { spacing: 5 }).layoutOptions!['elk.spacing.nodeNode']);
    expect(b).toBeGreaterThan(a);
  });
});

describe('fromElk', () => {
  // hand-made result in root coordinates
  const res: ElkNode = {
    id: 'root',
    children: [
      { id: 'lib', x: 0, y: 40, width: 40, height: 26 },
      {
        id: 'g:pkg/a', x: 100, y: 0, width: 200, height: 120,
        children: [{ id: 'pkg/a/x', x: 112, y: 30, width: 50, height: 26 }, { id: 'pkg/a/y', x: 230, y: 80, width: 50, height: 26 }],
      },
    ],
    edges: [
      { id: 'lib->pkg/a/x', sources: ['lib'], targets: ['pkg/a/x'], sections: [{ id: 's', startPoint: { x: 40, y: 53 }, bendPoints: [{ x: 70, y: 53 }, { x: 70, y: 43 }], endPoint: { x: 112, y: 43 } }] },
      { id: 'pkg/a/x->pkg/a/y', sources: ['pkg/a/x'], targets: ['pkg/a/y'], sections: [{ id: 's2', startPoint: { x: 162, y: 43 }, endPoint: { x: 230, y: 93 } }] },
    ],
  };
  const r = fromElk(res);

  it('returns node centers', () => {
    expect(r.nodes['lib']).toEqual({ x: 20, y: 53 });
    expect(r.nodes['pkg/a/x']).toEqual({ x: 137, y: 43 });
  });

  it('returns the full orthogonal route of each edge', () => {
    expect(r.edges['lib->pkg/a/x']).toEqual([[40, 53], [70, 53], [70, 43], [112, 43]]);
    expect(r.edges['pkg/a/x->pkg/a/y']).toEqual([[162, 43], [230, 93]]);
  });

  it('derives the box size of each group and the padding around its children', () => {
    // children bbox: x 112..280, y 30..106 ; box: x 100..300, y 0..120
    expect(r.comboPadding['g:pkg/a']).toEqual([30, 20, 14, 12]);
    expect(r.comboSize['g:pkg/a']).toEqual({ w: 200, h: 120 });
  });

  it('reports the overall size', () => {
    expect(r.width).toBe(300);
    expect(r.height).toBe(120);
  });
});

describe('elkjs end to end', () => {
  it('lays out a nested view with orthogonal routes and combos enclosing children', async () => {
    const v = synthView(60, 10);
    const out = fromElk(await new ELK().layout(toElkGraph(v, { spacing: 3 })));
    for (const n of v.nodes.filter((x) => !x.group)) expect(out.nodes[n.id]).toBeDefined();
    for (const e of v.edges) {
      const pts = out.edges[e.id];
      expect(pts.length).toBeGreaterThanOrEqual(2);
      for (let i = 1; i < pts.length; i++) {
        const [a, b] = [pts[i - 1], pts[i]];
        expect(Math.abs(a[0] - b[0]) < 0.01 || Math.abs(a[1] - b[1]) < 0.01).toBe(true);
      }
    }
    for (const p of Object.values(out.comboPadding)) for (const x of p) expect(x).toBeGreaterThanOrEqual(0);
  });
});


describe('edge labels', () => {
  it('asks ELK to place a label for edges folding several imports', () => {
    const g = toElkGraph(sampleView(), { spacing: 3 });
    const heavy = g.edges!.find((e) => e.id === 'pkg/a/x->g:pkg/b')!;
    expect(heavy.labels).toHaveLength(1);
    expect(heavy.labels![0]).toMatchObject({ id: 'lbl:pkg/a/x->g:pkg/b', text: '4' });
    expect(heavy.labels![0].width).toBeGreaterThan(0);
    expect(g.edges!.find((e) => e.id === 'lib->pkg/a/x')!.labels ?? []).toHaveLength(0);
  });

  it('reads the placed labels back as centers', () => {
    const r = fromElk({
      id: 'root',
      children: [],
      edges: [{ id: 'e', sources: ['a'], targets: ['b'], labels: [{ id: 'lbl:e', text: '3', x: 10, y: 20, width: 12, height: 10 }], sections: [] }],
    });
    expect(r.labels['e']).toEqual({ x: 16, y: 25, w: 12, h: 10 });
  });

  it('never lets two labels (or a label and a node) overlap', async () => {
    const v = synthView(120, 10);
    v.edges = v.edges.map((e, i) => ({ ...e, weight: 2 + (i % 17) }));
    const g = toElkGraph(v, { spacing: 3 });
    const out = fromElk(await new ELK().layout(g));
    const labels = Object.entries(out.labels).map(([id, l]) => ({ id, x1: l.x - l.w / 2, y1: l.y - l.h / 2, x2: l.x + l.w / 2, y2: l.y + l.h / 2 }));
    expect(labels.length).toBe(v.edges.length);
    const hit = (a: { x1: number; y1: number; x2: number; y2: number }, b: typeof a) => a.x1 < b.x2 && b.x1 < a.x2 && a.y1 < b.y2 && b.y1 < a.y2;
    for (let i = 0; i < labels.length; i++) for (let j = i + 1; j < labels.length; j++) expect(hit(labels[i], labels[j]), `${labels[i].id} / ${labels[j].id}`).toBe(false);
    const sizes = new Map(v.nodes.filter((n) => !n.group).map((n) => [n.id, nodeSize(labelOf(n))]));
    for (const [id, c] of Object.entries(out.nodes)) {
      const [w, h] = sizes.get(id)!;
      const nb = { x1: c.x - w / 2, y1: c.y - h / 2, x2: c.x + w / 2, y2: c.y + h / 2 };
      for (const l of labels) expect(hit(nb, l), `${id} / ${l.id}`).toBe(false);
    }
  });
});
