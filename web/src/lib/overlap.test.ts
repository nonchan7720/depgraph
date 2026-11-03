import { describe, expect, it } from 'vitest';
import { intersects, pushApart, type Rect } from './overlap';

const r = (id: string, x1: number, y1: number, x2: number, y2: number): Rect => ({ id, x1, y1, x2, y2 });
const shift = (b: Rect, d?: { dx: number; dy: number }): Rect => (d ? { ...b, x1: b.x1 + d.dx, x2: b.x2 + d.dx, y1: b.y1 + d.dy, y2: b.y2 + d.dy } : b);

describe('intersects', () => {
  it('detects overlap with a clearance gap', () => {
    expect(intersects(r('a', 0, 0, 10, 10), r('b', 5, 5, 20, 20))).toBe(true);
    expect(intersects(r('a', 0, 0, 10, 10), r('b', 15, 0, 20, 10))).toBe(false);
    expect(intersects(r('a', 0, 0, 10, 10), r('b', 15, 0, 20, 10), 8)).toBe(true);
  });
});

describe('pushApart', () => {
  it('pushes an overlapping box out along the shortest axis, away from the anchor', () => {
    const anchor = r('frame', 0, 0, 200, 100);
    const d = pushApart(anchor, [r('n', 180, 40, 260, 60)], 10);
    // penetration: x = 200 - 180 + 10 = 30 (to the right), y would be 60 + 10 - 0 = 70 → x wins
    expect(d.n).toEqual({ dx: 30, dy: 0 });
  });

  it('pushes upward when the box sits mostly above', () => {
    const d = pushApart(r('frame', 0, 0, 200, 100), [r('n', 50, -20, 150, 5)], 10);
    expect(d.n).toEqual({ dx: 0, dy: -15 });
  });

  it('leaves boxes that do not overlap alone', () => {
    expect(pushApart(r('frame', 0, 0, 100, 100), [r('far', 300, 300, 320, 320)], 10)).toEqual({});
  });

  it('cascades: a pushed box pushes the next one, and nothing overlaps afterwards', () => {
    const anchor = r('frame', 0, 0, 200, 100);
    const others = [r('a', 190, 40, 250, 60), r('b', 255, 40, 300, 60), r('c', 500, 500, 520, 520)];
    const d = pushApart(anchor, others, 10);
    const moved = others.map((o) => shift(o, d[o.id]));
    const all = [anchor, ...moved];
    for (let i = 0; i < all.length; i++) for (let j = i + 1; j < all.length; j++) expect(intersects(all[i], all[j]), `${all[i].id}/${all[j].id}`).toBe(false);
    expect(d.c).toBeUndefined();
  });

  it('terminates on crowded input and resolves every overlap with the anchor', () => {
    const anchor = r('frame', 0, 0, 300, 300);
    const others = Array.from({ length: 30 }, (_, i) => r('n' + i, 10 * i, 10 * i, 10 * i + 40, 10 * i + 20));
    const d = pushApart(anchor, others, 6);
    for (const o of others) expect(intersects(anchor, shift(o, d[o.id]), 6)).toBe(false);
  });
});
