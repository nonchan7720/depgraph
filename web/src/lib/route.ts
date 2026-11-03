// Edge routes as Cytoscape segment data. ELK's routes and the simple
// orthogonal routes of dragged nodes go through the same encoding, so every
// edge leaves its source on the right side and enters its target on the left.
type P = { x: number; y: number };
export interface Box {
  x: number; // center
  y: number;
  w: number;
  h: number;
}

const r2 = (x: number) => Number(x.toFixed(2));
const r6 = (x: number) => Number(x.toFixed(6));

/** Cytoscape: point = start + w·(end−start) + d·n, with n = (−vy, vx)/|v| */
export function segmentsFor(start: P, bends: P[], end: P): { weights: number[]; distances: number[] } | null {
  const vx = end.x - start.x;
  const vy = end.y - start.y;
  const l2 = vx * vx + vy * vy;
  if (l2 < 1e-9) return null;
  const l = Math.sqrt(l2);
  const [nx, ny] = [-vy / l, vx / l];
  const weights: number[] = [];
  const distances: number[] = [];
  for (const p of bends) {
    const [px, py] = [p.x - start.x, p.y - start.y];
    weights.push((px * vx + py * vy) / l2);
    distances.push(px * nx + py * ny);
  }
  return { weights, distances };
}

export function pointsFromSegments(start: P, end: P, weights: number[], distances: number[]): P[] {
  const vx = end.x - start.x;
  const vy = end.y - start.y;
  const l = Math.hypot(vx, vy);
  const [nx, ny] = [-vy / l, vx / l];
  return weights.map((w, i) => ({ x: start.x + w * vx + distances[i] * nx, y: start.y + w * vy + distances[i] * ny }));
}

/** data.route for an edge drawn through pts (start on the source, end on the target) */
export function routeFor(pts: [number, number][], s: P, t: P): Record<string, string> | null {
  if (pts.length < 2) return null;
  const a = pts[0];
  const b = pts[pts.length - 1];
  const seg = segmentsFor({ x: a[0], y: a[1] }, pts.slice(1, -1).map(([x, y]) => ({ x, y })), { x: b[0], y: b[1] });
  if (!seg) return null;
  const route: Record<string, string> = {
    'curve-style': seg.weights.length ? 'segments' : 'straight',
    'edge-distances': 'endpoints',
    'source-endpoint': `${r2(a[0] - s.x)}px ${r2(a[1] - s.y)}px`,
    'target-endpoint': `${r2(b[0] - t.x)}px ${r2(b[1] - t.y)}px`,
  };
  if (seg.weights.length) {
    route['segment-weights'] = seg.weights.map(r6).join(' ');
    route['segment-distances'] = seg.distances.map(r6).join(' ');
  }
  return route;
}

/**
 * Orthogonal route from the middle of s's right side to the middle of t's
 * left side; turns in the gap between them, or goes around both when t is
 * not to the right of s.
 */
export function elbowRoute(s: Box, t: Box, gap = 14): [number, number][] {
  const a: [number, number] = [s.x + s.w / 2, s.y];
  const b: [number, number] = [t.x - t.w / 2, t.y];
  if (b[0] - a[0] >= 2 * gap) {
    if (a[1] === b[1]) return [a, b];
    const mx = (a[0] + b[0]) / 2;
    return [a, [mx, a[1]], [mx, b[1]], b];
  }
  const [sTop, sBot, tTop, tBot] = [s.y - s.h / 2, s.y + s.h / 2, t.y - t.h / 2, t.y + t.h / 2];
  const my = tTop > sBot ? (sBot + tTop) / 2 : sTop > tBot ? (tBot + sTop) / 2 : Math.max(sBot, tBot) + gap;
  const x1 = a[0] + gap;
  const x2 = b[0] - gap;
  return [a, [x1, a[1]], [x1, my], [x2, my], [x2, b[1]], b];
}
