// Overlap removal after a drag: a moved node or a frame that grew must not
// end up covering nodes it does not contain. Pure.

export interface Rect {
  id: string;
  x1: number;
  y1: number;
  x2: number;
  y2: number;
}

export type Shift = { dx: number; dy: number };

/** a and b overlap (or come closer than gap) */
export function intersects(a: Rect, b: Rect, gap = 0): boolean {
  return a.x1 < b.x2 + gap && b.x1 < a.x2 + gap && a.y1 < b.y2 + gap && b.y1 < a.y2 + gap;
}

const cx = (r: Rect) => (r.x1 + r.x2) / 2;
const cy = (r: Rect) => (r.y1 + r.y2) / 2;

/**
 * Moves `others` so that none overlaps `anchor` (which stays put) or a box
 * that was pushed before it. Every box moves away from the anchor's center,
 * along the axis needing the smaller move, so pushes cascade outward and the
 * loop settles. Returns the total shift per moved box.
 */
export function pushApart(anchor: Rect, others: Rect[], gap = 12): Record<string, Shift> {
  const boxes = others.map((o) => ({ ...o }));
  const shifts: Record<string, Shift> = {};
  const pushers: Rect[] = [anchor];
  const isPusher = new Set<string>([anchor.id]);
  const [ax, ay] = [cx(anchor), cy(anchor)];
  for (let round = 0; round < 100; round++) {
    let changed = false;
    for (const p of pushers) {
      for (const o of boxes) {
        if (o.id === p.id || !intersects(p, o, gap)) continue;
        const dx = cx(o) >= ax ? p.x2 + gap - o.x1 : p.x1 - gap - o.x2;
        const dy = cy(o) >= ay ? p.y2 + gap - o.y1 : p.y1 - gap - o.y2;
        const d: Shift = Math.abs(dx) <= Math.abs(dy) ? { dx, dy: 0 } : { dx: 0, dy };
        o.x1 += d.dx;
        o.x2 += d.dx;
        o.y1 += d.dy;
        o.y2 += d.dy;
        const s = (shifts[o.id] ??= { dx: 0, dy: 0 });
        s.dx += d.dx;
        s.dy += d.dy;
        if (!isPusher.has(o.id)) {
          isPusher.add(o.id);
          pushers.push(o);
        }
        changed = true;
      }
    }
    if (!changed) break;
  }
  return shifts;
}
