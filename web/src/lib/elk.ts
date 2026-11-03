// API view <-> ELK graph. Pure: elkjs itself runs in a Web Worker (see ../layout.ts).
//
// Expanded groups become ELK hierarchical nodes; ELK (layered, RIGHT,
// ORTHOGONAL, INCLUDE_CHILDREN) places everything and routes every edge, and
// the result is mapped back as node centers, edge routes, and for each group
// its box size and the padding around its children (Cytoscape sizes a compound
// node from its children, see ./cy.ts).
import type { ElkExtendedEdge, ElkNode } from 'elkjs/lib/elk-api';
import type { View } from './api';
import { labelOf, nodeSize } from './transform';

export interface ElkOptions {
  spacing: number; // 1..5
}

/** space reserved inside an opened group: room for its label on top */
export const COMBO_PADDING = { top: 30, left: 12, bottom: 12, right: 12 };

export function toElkGraph(view: View, opts: ElkOptions): ElkNode {
  const s = opts.spacing;
  const combos = new Set(view.nodes.filter((n) => n.group && n.expanded).map((n) => n.id));
  const elk = new Map<string, ElkNode>();
  for (const n of view.nodes) {
    if (combos.has(n.id)) {
      const p = COMBO_PADDING;
      elk.set(n.id, { id: n.id, children: [], layoutOptions: { 'elk.padding': `[top=${p.top},left=${p.left},bottom=${p.bottom},right=${p.right}]` } });
    } else {
      const [width, height] = nodeSize(labelOf(n));
      elk.set(n.id, { id: n.id, width, height });
    }
  }
  const root: ElkNode = {
    id: 'root',
    layoutOptions: {
      'elk.algorithm': 'layered',
      'elk.direction': 'RIGHT',
      'elk.edgeRouting': 'ORTHOGONAL',
      'elk.hierarchyHandling': 'INCLUDE_CHILDREN',
      'elk.json.shapeCoords': 'ROOT',
      'elk.json.edgeCoords': 'ROOT',
      'elk.spacing.nodeNode': String(8 + s * 6),
      'elk.layered.spacing.nodeNodeBetweenLayers': String(20 + s * 12),
      'elk.spacing.edgeNode': String(6 + s * 3),
      'elk.spacing.edgeEdge': String(3 + s * 2),
      'elk.layered.spacing.edgeNodeBetweenLayers': String(6 + s * 3),
      'elk.layered.spacing.edgeEdgeBetweenLayers': String(3 + s * 2),
      'elk.spacing.componentComponent': String(20 + s * 10),
      'elk.padding': '[top=10,left=10,bottom=10,right=10]',
    },
    children: [],
    edges: [],
  };
  for (const n of view.nodes) {
    const parent = n.parent && combos.has(n.parent) ? elk.get(n.parent)! : root;
    parent.children!.push(elk.get(n.id)!);
  }
  const leaves = new Set(view.nodes.filter((n) => !combos.has(n.id)).map((n) => n.id));
  root.edges = view.edges
    .filter((e) => leaves.has(e.from) && leaves.has(e.to))
    .map((e): ElkExtendedEdge => {
      const edge: ElkExtendedEdge = { id: e.id, sources: [e.from], targets: [e.to] };
      if (e.weight > 1) {
        const text = String(e.weight);
        edge.labels = [{ id: 'lbl:' + e.id, text, ...labelSize(text), layoutOptions: { 'elk.edgeLabels.placement': 'CENTER' } }];
      }
      return edge;
    });
  return root;
}

/** box of an edge weight label (monospace 9px + padding) */
export const labelSize = (text: string) => ({ width: Math.ceil(text.length * 5.6) + 6, height: 12 });

export interface ElkResult {
  nodes: Record<string, { x: number; y: number }>; // centers of leaves
  edges: Record<string, [number, number][]>; // start, bends..., end
  comboPadding: Record<string, [number, number, number, number]>; // top, right, bottom, left
  comboSize: Record<string, { w: number; h: number }>;
  labels: Record<string, { x: number; y: number; w: number; h: number }>; // edge id -> label center
  width: number;
  height: number;
}

/** maps an ELK result laid out with shapeCoords/edgeCoords = ROOT */
export function fromElk(g: ElkNode): ElkResult {
  const out: ElkResult = { nodes: {}, edges: {}, comboPadding: {}, comboSize: {}, labels: {}, width: 0, height: 0 };
  const rect = (n: ElkNode) => ({ l: n.x ?? 0, t: n.y ?? 0, r: (n.x ?? 0) + (n.width ?? 0), b: (n.y ?? 0) + (n.height ?? 0) });
  const visit = (n: ElkNode) => {
    const kids = n.children ?? [];
    if (!kids.length) {
      const r = rect(n);
      out.nodes[n.id] = { x: (r.l + r.r) / 2, y: (r.t + r.b) / 2 };
      return;
    }
    kids.forEach(visit);
    const box = rect(n);
    const rs = kids.map(rect);
    const [l, t, r, b] = [Math.min(...rs.map((x) => x.l)), Math.min(...rs.map((x) => x.t)), Math.max(...rs.map((x) => x.r)), Math.max(...rs.map((x) => x.b))];
    out.comboSize[n.id] = { w: box.r - box.l, h: box.b - box.t };
    out.comboPadding[n.id] = [t - box.t, box.r - r, box.b - b, l - box.l].map((x) => Math.max(0, x)) as [number, number, number, number];
  };
  (g.children ?? []).forEach(visit);

  const allEdges: ElkExtendedEdge[] = [];
  const collect = (n: ElkNode) => {
    allEdges.push(...((n.edges ?? []) as ElkExtendedEdge[]));
    (n.children ?? []).forEach(collect);
  };
  collect(g);
  for (const e of allEdges) {
    const pts: [number, number][] = [];
    for (const s of e.sections ?? []) {
      for (const p of [s.startPoint, ...(s.bendPoints ?? []), s.endPoint]) {
        const last = pts[pts.length - 1];
        if (!last || last[0] !== p.x || last[1] !== p.y) pts.push([p.x, p.y]);
      }
    }
    out.edges[e.id] = pts;
    const l = e.labels?.[0];
    if (l) out.labels[e.id] = { x: (l.x ?? 0) + (l.width ?? 0) / 2, y: (l.y ?? 0) + (l.height ?? 0) / 2, w: l.width ?? 0, h: l.height ?? 0 };
  }

  const rs = (g.children ?? []).map(rect);
  out.width = g.width ?? Math.max(0, ...rs.map((x) => x.r));
  out.height = g.height ?? Math.max(0, ...rs.map((x) => x.b));
  return out;
}

