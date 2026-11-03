// Label / size / edge width helpers shared by the ELK graph and the Cytoscape elements.
import type { ViewNode } from './api';

export function labelOf(n: ViewNode): string {
  if (n.group && !n.expanded) return `${n.label}/ ·${n.members}`;
  if (n.group) return `${n.label}/`;
  return n.label;
}

// monospace 11px ≈ 6.8px per character
const CHAR_W = 6.8;
export const NODE_H = 26;

export function nodeSize(label: string): [number, number] {
  return [Math.max(40, Math.ceil(label.length * CHAR_W) + 20), NODE_H];
}

export function edgeWidth(weight: number): number {
  return Math.min(6, 1 + Math.log2(Math.max(1, weight)) * 1.1);
}
