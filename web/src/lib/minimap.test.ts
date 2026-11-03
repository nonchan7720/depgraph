import { describe, expect, it } from 'vitest';
import { fitTransform } from './minimap';

describe('fitTransform', () => {
  const t = fitTransform({ x1: 0, y1: 0, x2: 1000, y2: 500 }, 200, 150, 10);

  it('fits the whole graph inside the canvas keeping the aspect ratio', () => {
    expect(t.scale).toBeCloseTo(0.18); // (200 - 20) / 1000
    const a = t.toMini({ x: 0, y: 0 });
    const b = t.toMini({ x: 1000, y: 500 });
    expect(a.x).toBeCloseTo(10);
    expect(b.x).toBeCloseTo(190);
    // centered vertically
    expect((a.y + b.y) / 2).toBeCloseTo(75);
  });

  it('maps back from the minimap to graph coordinates', () => {
    const p = t.fromMini(t.toMini({ x: 321, y: 123 }));
    expect(p.x).toBeCloseTo(321);
    expect(p.y).toBeCloseTo(123);
  });

  it('survives an empty graph', () => {
    const e = fitTransform({ x1: 0, y1: 0, x2: 0, y2: 0 }, 200, 150, 10);
    expect(Number.isFinite(e.scale)).toBe(true);
  });
});
