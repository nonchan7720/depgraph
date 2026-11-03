// ELK result -> Cytoscape elements. Pure (no Cytoscape import), unit-tested.
//
// Leaves get ELK's center as a preset position. Opened groups are compound
// nodes; Cytoscape sizes them from their children, so each gets ELK's box as
// min-width/min-height with the extra space split by ELK's padding (bias).
// Edges reproduce ELK's orthogonal route: the exact start/end points become
// manual endpoints and every bend point is encoded as (weight, distance)
// against the start→end line, which is what Cytoscape's
// `curve-style: segments` + `edge-distances: endpoints` decodes.
// Edges of nodes the user dragged get a simple side-to-side orthogonal route
// instead (class `manual`, see ./route.ts), as their ELK route no longer fits.
// Weight labels are placed by ELK too and drawn as small label nodes (class
// `elabel`); a manual edge shows its own label instead.
// Nothing is set as a style bypass: the route (`data.route`, class `routed`) and
// the frame size (`data.frame`, class `fixed`) are read by the stylesheet, so
// dropping a class is enough to switch an element back to the default style.
import type { View, ViewNode } from './api';
import type { ElkResult } from './elk';
import { mergeManual, type Positions } from './manual';
import { elbowRoute, routeFor } from './route';
import { edgeWidth, labelOf, nodeSize } from './transform';

export { pointsFromSegments, segmentsFor } from './route';

type P = { x: number; y: number };

export interface CyElement {
  group: 'nodes' | 'edges';
  data: { id: string; [k: string]: unknown };
  position?: P;
  classes?: string;
}

const r2 = (x: number) => Number(x.toFixed(2));

function nodeClasses(n: ViewNode): string {
  const c = [n.group ? (n.expanded ? 'box' : 'group') : 'pkg'];
  if (n.external) c.push('external');
  if (n.context) c.push('context');
  if (n.bad) c.push('bad');
  if (n.god) c.push('god');
  return c.join(' ');
}

const pct = (a: number, b: number) => (a + b > 0 ? r2((a / (a + b)) * 100) : 50) + '%';

export function toCyElements(view: View, r: ElkResult, manual: Positions): { elements: CyElement[]; moved: Set<string> } {
  const boxes = new Set(view.nodes.filter((n) => n.group && n.expanded).map((n) => n.id));
  const { nodes: pos, moved } = mergeManual(r.nodes, manual);
  const parentOf = new Map(view.nodes.map((n) => [n.id, n.parent && boxes.has(n.parent) ? n.parent : undefined]));
  // boxes holding a moved node can no longer use ELK's box
  const loose = new Set<string>();
  for (const id of moved) for (let p = parentOf.get(id); p; p = parentOf.get(p)) loose.add(p);

  const elements: CyElement[] = [];
  for (const n of view.nodes) {
    const label = labelOf(n);
    const parent = parentOf.get(n.id);
    const data = { id: n.id, ...(parent ? { parent } : {}), label, view: n };
    if (boxes.has(n.id)) {
      const size = r.comboSize[n.id];
      const pad = r.comboPadding[n.id];
      const el: CyElement = { group: 'nodes', data: { ...data, lw: Math.ceil(label.length * 7.2) }, classes: nodeClasses(n) + (loose.has(n.id) ? ' loose' : '') };
      if (size && pad && !loose.has(n.id)) {
        const [t, rt, b, l] = pad;
        el.classes += ' fixed';
        el.data.frame = {
          'min-width': r2(size.w),
          'min-height': r2(size.h),
          'min-width-bias-left': pct(l, rt),
          'min-width-bias-right': pct(rt, l),
          'min-height-bias-top': pct(t, b),
          'min-height-bias-bottom': pct(b, t),
        };
      }
      elements.push(el);
    } else {
      const [w, h] = nodeSize(label);
      elements.push({ group: 'nodes', data: { ...data, w, h }, position: pos[n.id] ? { ...pos[n.id] } : undefined, classes: nodeClasses(n) });
    }
  }

  const leaves = new Set(view.nodes.filter((n) => !boxes.has(n.id)).map((n) => n.id));
  const sizes = new Map(view.nodes.map((n) => [n.id, nodeSize(labelOf(n))]));
  const boxOf = (id: string) => {
    const [w, h] = sizes.get(id)!;
    return { ...pos[id], w, h };
  };
  for (const e of view.edges) {
    if (!leaves.has(e.from) || !leaves.has(e.to)) continue;
    const s = pos[e.from];
    const t = pos[e.to];
    if (!s || !t) continue;
    const wl = e.weight > 1 ? String(e.weight) : '';
    const el: CyElement = {
      group: 'edges',
      data: { id: e.id, source: e.from, target: e.to, weight: e.weight, wl, width: r2(edgeWidth(e.weight)) },
      classes: e.bad ? 'bad' : '',
    };
    const manualEdge = moved.has(e.from) || moved.has(e.to) || !r.edges[e.id] || r.edges[e.id].length < 2;
    // ELK's route, or a side-to-side route for edges of dragged nodes
    const route = manualEdge ? routeFor(elbowRoute(boxOf(e.from), boxOf(e.to)), s, t) : routeFor(r.edges[e.id], s, t);
    if (route) {
      el.data.route = route;
      el.classes = `${el.classes} routed ${route['curve-style']}`;
    }
    if (manualEdge) el.classes += ' manual';
    el.classes = el.classes!.trim();
    elements.push(el);
    // ELK placed the weight label clear of nodes and other labels
    const l = r.labels[e.id];
    if (wl && l && !manualEdge) elements.push({ group: 'nodes', data: { id: 'lbl:' + e.id, label: wl, edge: e.id }, position: { x: l.x, y: l.y }, classes: 'elabel' });
  }
  return { elements, moved };
}
