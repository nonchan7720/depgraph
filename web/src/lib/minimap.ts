// Minimap geometry: fit the graph's bounding box into the minimap canvas.
type P = { x: number; y: number };
export interface BBox {
  x1: number;
  y1: number;
  x2: number;
  y2: number;
}

export function fitTransform(b: BBox, w: number, h: number, pad: number) {
  const gw = Math.max(1, b.x2 - b.x1);
  const gh = Math.max(1, b.y2 - b.y1);
  const scale = Math.min((w - 2 * pad) / gw, (h - 2 * pad) / gh);
  const ox = (w - gw * scale) / 2 - b.x1 * scale;
  const oy = (h - gh * scale) / 2 - b.y1 * scale;
  return {
    scale,
    toMini: (p: P): P => ({ x: p.x * scale + ox, y: p.y * scale + oy }),
    fromMini: (p: P): P => ({ x: (p.x - ox) / scale, y: (p.y - oy) / scale }),
  };
}
