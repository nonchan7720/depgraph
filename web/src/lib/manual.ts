// Positions the user set by dragging, kept on top of the ELK layout.
import type { View } from './api';

export type Point = { x: number; y: number };
export type Positions = Record<string, Point>;

/** localStorage key: manual positions only make sense for the same folding */
export const manualKey = (root: string, depth: number, focus: string) => `depgraph:ui:pos:${root}|${depth}|${focus}`;

export function parseManual(raw: string | null): Positions {
  if (!raw) return {};
  let o: unknown;
  try {
    o = JSON.parse(raw);
  } catch {
    return {};
  }
  if (!o || typeof o !== 'object' || Array.isArray(o)) return {};
  const out: Positions = {};
  for (const [k, v] of Object.entries(o as Record<string, unknown>)) {
    const p = v as Partial<Point> | null;
    if (p && typeof p.x === 'number' && typeof p.y === 'number' && Number.isFinite(p.x) && Number.isFinite(p.y)) out[k] = { x: p.x, y: p.y };
  }
  return out;
}

/** layout positions with the manual ones applied (only for nodes that exist) */
export function mergeManual(layout: Positions, manual: Positions): { nodes: Positions; moved: Set<string> } {
  const nodes: Positions = { ...layout };
  const moved = new Set<string>();
  for (const [id, p] of Object.entries(manual)) {
    if (id in layout) {
      nodes[id] = { ...p };
      moved.add(id);
    }
  }
  return { nodes, moved };
}

export function affectedEdges(edges: { id: string; source: string; target: string }[], moved: Set<string>): string[] {
  return edges.filter((e) => moved.has(e.source) || moved.has(e.target)).map((e) => e.id);
}

/** the packages (leaves) inside a box, or the node itself */
export function leavesUnder(view: View, id: string): string[] {
  const kids = new Map<string, string[]>();
  for (const n of view.nodes) if (n.parent) kids.set(n.parent, [...(kids.get(n.parent) ?? []), n.id]);
  const out: string[] = [];
  const walk = (x: string) => {
    const k = kids.get(x);
    if (!k?.length) out.push(x);
    else k.forEach(walk);
  };
  walk(id);
  return out;
}

