import { describe, expect, it } from 'vitest';
import { pointsFromSegments } from './cy';
import { elbowRoute, routeFor } from './route';

const box = (x: number, y: number, w = 40, h = 20) => ({ x, y, w, h });
const orth = (p: [number, number][]) => p.every((q, i) => i === 0 || q[0] === p[i - 1][0] || q[1] === p[i - 1][1]);

describe('elbowRoute', () => {
  it('leaves the source on its right side and enters the target on its left side', () => {
    const p = elbowRoute(box(0, 0), box(200, 100));
    expect(p[0]).toEqual([20, 0]);
    expect(p[p.length - 1]).toEqual([180, 100]);
    expect(orth(p)).toBe(true);
  });

  it('is a straight line when both are on one row', () => {
    expect(elbowRoute(box(0, 0), box(200, 0))).toEqual([[20, 0], [180, 0]]);
  });

  it('goes around when the target is to the left, still using the sides', () => {
    const p = elbowRoute(box(200, 0), box(0, 100));
    expect(p[0]).toEqual([220, 0]);
    expect(p[1][0]).toBeGreaterThan(220); // first leg goes right
    expect(p[p.length - 1]).toEqual([-20, 100]);
    expect(p[p.length - 2][0]).toBeLessThan(-20); // last leg comes from the left
    expect(orth(p)).toBe(true);
  });
});

describe('routeFor', () => {
  it('turns a route into Cytoscape segment data relative to the node centers', () => {
    const pts: [number, number][] = [[20, 0], [100, 0], [100, 100], [180, 100]];
    const r = routeFor(pts, { x: 0, y: 0 }, { x: 200, y: 100 })!;
    expect(r['curve-style']).toBe('segments');
    expect(r['source-endpoint']).toBe('20px 0px');
    expect(r['target-endpoint']).toBe('-20px 0px');
    const back = pointsFromSegments({ x: 20, y: 0 }, { x: 180, y: 100 }, r['segment-weights'].split(' ').map(Number), r['segment-distances'].split(' ').map(Number));
    expect(back[0].x).toBeCloseTo(100);
    expect(back[0].y).toBeCloseTo(0);
    expect(back[1].y).toBeCloseTo(100);
  });

  it('uses a straight edge when there are no bends', () => {
    expect(routeFor([[20, 0], [180, 0]], { x: 0, y: 0 }, { x: 200, y: 0 })!['curve-style']).toBe('straight');
  });
});
