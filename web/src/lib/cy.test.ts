import { describe, expect, it } from 'vitest';
import ELK from 'elkjs/lib/elk.bundled.js';
import { pointsFromSegments, segmentsFor, toCyElements, type CyElement } from './cy';
import { fromElk, toElkGraph, type ElkResult } from './elk';
import { node, sampleView } from './fixture';
import { synthView } from './bench';

type P = { x: number; y: number };
const close = (a: P, b: P, eps = 1e-6) => Math.abs(a.x - b.x) < eps && Math.abs(a.y - b.y) < eps;

describe('segmentsFor', () => {
  it('encodes bend points relative to the start→end line and decodes back exactly', () => {
    const start = { x: 40, y: 53 };
    const end = { x: 312, y: 143 };
    const bends = [{ x: 70, y: 53 }, { x: 70, y: 143 }, { x: 300, y: -20 }];
    const s = segmentsFor(start, bends, end)!;
    expect(s.weights).toHaveLength(3);
    const back = pointsFromSegments(start, end, s.weights, s.distances);
    back.forEach((p, i) => expect(close(p, bends[i])).toBe(true));
  });

  it('handles points behind the start and beyond the end (weights outside 0..1)', () => {
    const start = { x: 0, y: 0 };
    const end = { x: 100, y: 0 };
    const s = segmentsFor(start, [{ x: -20, y: 30 }, { x: 140, y: -30 }], end)!;
    expect(s.weights[0]).toBeCloseTo(-0.2);
    expect(s.weights[1]).toBeCloseTo(1.4);
    // Cytoscape's normal of (1,0) is (0,1): positive distance = +y
    expect(s.distances).toEqual([30, -30]);
  });

  it('gives up on a degenerate start = end', () => {
    expect(segmentsFor({ x: 1, y: 1 }, [{ x: 2, y: 2 }], { x: 1, y: 1 })).toBeNull();
  });
});

const elkLike = (): ElkResult => ({
  nodes: { lib: { x: 20, y: 53 }, 'pkg/a/x': { x: 137, y: 43 }, 'pkg/a/y': { x: 255, y: 93 }, 'g:pkg/b': { x: 400, y: 43 } },
  edges: {
    'lib->pkg/a/x': [[40, 53], [70, 53], [70, 43], [112, 43]],
    'pkg/a/x->pkg/a/y': [[162, 43], [230, 93]],
    'pkg/a/x->g:pkg/b': [[162, 40], [360, 40]],
  },
  comboPadding: { 'g:pkg/a': [30, 20, 14, 12] },
  comboSize: { 'g:pkg/a': { w: 200, h: 120 } },
  labels: { 'pkg/a/x->g:pkg/b': { x: 260, y: 32, w: 12, h: 12 } },
  width: 500,
  height: 200,
});

const byId = (els: CyElement[], id: string) => els.find((e) => e.data.id === id)!;

describe('toCyElements', () => {
  const { elements } = toCyElements(sampleView(), elkLike(), {});

  it('places leaves at the ELK centers and nests them in their group', () => {
    const x = byId(elements, 'pkg/a/x');
    expect(x.group).toBe('nodes');
    expect(x.position).toEqual({ x: 137, y: 43 });
    expect(x.data.parent).toBe('g:pkg/a');
    expect(byId(elements, 'lib').data.parent).toBeUndefined();
  });

  it('classes nodes by kind and findings', () => {
    expect(byId(elements, 'g:pkg/a').classes).toContain('box');
    expect(byId(elements, 'g:pkg/b').classes).toContain('group');
    expect(byId(elements, 'g:pkg/b').classes).toContain('god');
    expect(byId(elements, 'lib').classes).toContain('bad');
    expect(byId(elements, 'lib').classes).toContain('pkg');
  });

  it('never sets style bypasses (everything goes through data + stylesheet)', () => {
    for (const e of elements) expect(e).not.toHaveProperty('style');
  });

  it('sizes a group box like ELK: min size plus the extra split by the ELK padding', () => {
    const box = byId(elements, 'g:pkg/a');
    expect(box.position).toBeUndefined(); // compound position follows its children
    expect(box.classes).toContain('fixed');
    expect(box.data.frame).toMatchObject({
      'min-width': 200,
      'min-height': 120,
      'min-width-bias-left': '37.5%', // 12 / (12 + 20)
      'min-width-bias-right': '62.5%',
      'min-height-bias-top': '68.18%', // 30 / 44
      'min-height-bias-bottom': '31.82%',
    });
  });

  it('draws routed edges as segments between the exact ELK endpoints', () => {
    const e = byId(elements, 'lib->pkg/a/x');
    expect(e.group).toBe('edges');
    expect(e.data).toMatchObject({ source: 'lib', target: 'pkg/a/x' });
    expect(e.classes).toContain('bad');
    expect(e.classes).toContain('routed');
    expect(e.data.route).toMatchObject({
      'curve-style': 'segments',
      'edge-distances': 'endpoints',
      'source-endpoint': '20px 0px', // (40,53) - center (20,53)
      'target-endpoint': '-25px 0px', // (112,43) - (137,43)
    });
    const route = e.data.route as Record<string, string>;
    const ws = route['segment-weights'].split(' ').map(Number);
    const ds = route['segment-distances'].split(' ').map(Number);
    const back = pointsFromSegments({ x: 40, y: 53 }, { x: 112, y: 43 }, ws, ds);
    expect(close(back[0], { x: 70, y: 53 }, 1e-3)).toBe(true);
    expect(close(back[1], { x: 70, y: 43 }, 1e-3)).toBe(true);
  });

  it('draws an edge without bends straight between the ELK endpoints', () => {
    const e = byId(elements, 'pkg/a/x->g:pkg/b');
    expect(e.data.route).toMatchObject({ 'curve-style': 'straight', 'source-endpoint': '25px -3px' });
  });

  it('adds a label node where ELK placed the weight label', () => {
    const l = byId(elements, 'lbl:pkg/a/x->g:pkg/b');
    expect(l.group).toBe('nodes');
    expect(l.classes).toContain('elabel');
    expect(l.position).toEqual({ x: 260, y: 32 });
    expect(l.data).toMatchObject({ label: '4', edge: 'pkg/a/x->g:pkg/b' });
    expect(l.data.parent).toBeUndefined();
  });

  it('carries weight and width for styling', () => {
    const e = byId(elements, 'pkg/a/x->g:pkg/b');
    expect(e.data.weight).toBe(4);
    expect(e.data.wl).toBe('4');
    expect(typeof e.data.width).toBe('number');
  });
});

describe('toCyElements with dragged positions', () => {
  const r = toCyElements(sampleView(), elkLike(), { 'pkg/a/y': { x: 300, y: 300 }, 'g:pkg/b': { x: 400, y: 200 }, gone: { x: 0, y: 0 } });

  it('drops the ELK label of an edge whose end was moved (the edge shows its own label)', () => {
    expect(r.elements.find((e) => e.data.id === 'lbl:pkg/a/x->g:pkg/b')).toBeUndefined();
    expect(byId(r.elements, 'pkg/a/x->g:pkg/b').classes).toContain('manual');
  });

  it('applies manual positions over ELK', () => {
    expect(byId(r.elements, 'pkg/a/y').position).toEqual({ x: 300, y: 300 });
    expect([...r.moved].sort()).toEqual(['g:pkg/b', 'pkg/a/y']);
  });

  it('re-routes the edges of moved nodes side to side (right of source → left of target) and keeps the others', () => {
    const moved = byId(r.elements, 'pkg/a/x->pkg/a/y');
    expect(moved.classes).toContain('manual');
    expect(moved.classes).toContain('routed');
    const route = moved.data.route as Record<string, string>;
    // pkg/a/x is 'x' (w 40) at (137,43); pkg/a/y is 'y' (w 40) at (300,300)
    expect(route['source-endpoint']).toBe('20px 0px');
    expect(route['target-endpoint']).toBe('-20px 0px');
    expect((byId(r.elements, 'lib->pkg/a/x').data.route as Record<string, string>)['curve-style']).toBe('segments');
  });

  it('lets a group that holds a moved node size itself from its children', () => {
    const box = byId(r.elements, 'g:pkg/a');
    expect(box.classes).toContain('loose');
    expect(box.classes).not.toContain('fixed');
  });
});

describe('ELK → Cytoscape end to end', () => {
  it('reproduces every ELK route within 0.01px', async () => {
    const v = synthView(80, 10);
    v.nodes.push(node({ id: 'solo' }));
    const res = fromElk(await new ELK().layout(toElkGraph(v, { spacing: 3 })));
    const { elements } = toCyElements(v, res, {});
    let checked = 0;
    for (const e of elements.filter((x) => x.group === 'edges' && (x.data.route as Record<string, string> | undefined)?.['curve-style'] === 'segments')) {
      const pts = res.edges[e.data.id];
      const route = e.data.route as Record<string, string>;
      const ws = route['segment-weights'].split(' ').map(Number);
      const ds = route['segment-distances'].split(' ').map(Number);
      const s = { x: pts[0][0], y: pts[0][1] };
      const t = { x: pts[pts.length - 1][0], y: pts[pts.length - 1][1] };
      pointsFromSegments(s, t, ws, ds).forEach((p, i) => expect(close(p, { x: pts[i + 1][0], y: pts[i + 1][1] }, 0.01)).toBe(true));
      checked++;
    }
    expect(checked).toBeGreaterThan(10);
  });
});
