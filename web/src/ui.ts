import cytoscape, { type Core, type EdgeSingular, type EventObject, type NodeSingular, type StylesheetJson } from 'cytoscape';
import './style.css';
import type { Finding, GraphResponse, PackageDetail, View } from './lib/api';
import { synthView } from './lib/bench';
import { toCyElements } from './lib/cy';
import { fromElk, labelSize, toElkGraph } from './lib/elk';
import { fitTransform } from './lib/minimap';
import { pushApart, type Rect } from './lib/overlap';
import { elbowRoute, routeFor } from './lib/route';
import { highlightFinding, highlightPackage, type States } from './lib/highlight';
import { leavesUnder, manualKey, parseManual, type Positions } from './lib/manual';
import { crumbs, directorySummary, findingKey, groupFindings, parentOf } from './lib/panel';
import { collapseUnder, defaultState, parseState, toGraphQuery, type UIState } from './lib/state';
import type { Host } from './lib/host';
import { Superseded } from './lib/layout';

const $ = <T extends HTMLElement = HTMLElement>(s: string) => document.querySelector(s) as T;
const esc = (s: unknown) => String(s).replace(/[&<>"]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' })[c]!);
const css = (v: string) => getComputedStyle(document.documentElement).getPropertyValue(v).trim();

const C = {
  bg: css('--bg'), text: css('--text'), muted: css('--muted'), accent: css('--accent'), error: css('--error'),
  warn: css('--warn'), in: css('--in'), node: css('--node'), group: css('--group'), edge: '#3b4257', mono: css('--mono'),
};

// ---- app state -----------------------------------------------------------------
const params = new URLSearchParams(location.search);
const bench = Number(params.get('bench') || 0); // ?bench=N: synthetic graph with N nodes
let host: Host; // set by start()
let st: UIState = defaultState();
let rootPath = '';
let view: View = { depth: 1, focus: '', nodes: [], edges: [], nodeOf: {} };
let findings: Finding[] = [];
let activeKey: string | null = null; // selected finding
let selected: string | null = null; // selected package key
let lastShape = '';
const openRules = new Set(['layer-violation', 'import-cycle', 'module-cycle']);

const save = () => {
  try {
    localStorage.setItem('depgraph:ui:' + rootPath, JSON.stringify(st));
  } catch {}
};
const restore = () => {
  try {
    st = parseState(localStorage.getItem('depgraph:ui:' + rootPath));
  } catch {
    st = defaultState();
  }
  $<HTMLInputElement>('#ext').checked = st.showExt;
};

// ---- graph ---------------------------------------------------------------------
const frame = (e: NodeSingular, k: string) => (e.data('frame') as Record<string, string | number>)[k];
const route = (e: EdgeSingular, k: string) => (e.data('route') as Record<string, string>)[k];

const style: StylesheetJson = [
  {
    selector: 'node',
    style: {
      shape: 'round-rectangle',
      label: 'data(label)',
      'font-family': C.mono,
      'font-size': 11,
      color: C.text,
      'text-valign': 'center',
      'text-halign': 'center',
      'background-color': C.node,
      'border-width': 1.3,
      'border-color': C.accent,
    },
  },
  { selector: 'node[w]', style: { width: 'data(w)', height: 'data(h)' } },
  { selector: 'node.group', style: { 'background-color': C.group, 'border-style': 'dashed', 'font-weight': 600 } },
  {
    // opened directory: a frame with its name in the top-left corner
    selector: 'node.box',
    style: {
      'background-color': C.bg,
      'background-opacity': 0.45,
      'border-color': '#3b4257',
      'border-width': 1.2,
      'corner-radius': '8',
      padding: '0px',
      'compound-sizing-wrt-labels': 'exclude',
      'text-valign': 'top',
      'text-halign': 'left',
      'text-margin-x': (e: NodeSingular) => Number(e.data('lw')) + 12,
      'text-margin-y': 22,
      color: C.muted,
      'font-size': 12,
      'font-weight': 600,
    },
  },
  {
    // ELK's box for this frame (lib/cy.ts): min size, extra split like ELK's padding
    selector: 'node.box.fixed',
    style: {
      'min-width': (e: NodeSingular) => frame(e, 'min-width'),
      'min-height': (e: NodeSingular) => frame(e, 'min-height'),
      'min-width-bias-left': (e: NodeSingular) => frame(e, 'min-width-bias-left'),
      'min-width-bias-right': (e: NodeSingular) => frame(e, 'min-width-bias-right'),
      'min-height-bias-top': (e: NodeSingular) => frame(e, 'min-height-bias-top'),
      'min-height-bias-bottom': (e: NodeSingular) => frame(e, 'min-height-bias-bottom'),
    } as unknown as cytoscape.Css.Node,
  },
  { selector: 'node.box.loose', style: { padding: '28px' } },
  { selector: 'node.external, node.context', style: { 'border-color': C.muted, color: C.muted, 'border-style': 'dotted' } },
  { selector: 'node.context', style: { 'background-color': C.bg, opacity: 0.8 } },
  { selector: 'node.god', style: { 'border-color': C.warn, 'border-width': 2.2 } },
  { selector: 'node.bad', style: { 'border-color': C.error, 'border-width': 2.2 } },
  { selector: 'node.pkg.bad', style: { 'background-color': '#2a1a20' } },
  {
    selector: 'edge',
    style: {
      width: 'data(width)',
      'line-color': C.edge,
      'target-arrow-color': C.edge,
      'target-arrow-shape': 'triangle',
      'arrow-scale': 0.8,
      'curve-style': 'straight', // every edge gets a route (lib/cy.ts, lib/route.ts)
      'font-size': 9,
      color: C.muted,
      'text-background-color': C.bg,
      'text-background-opacity': 1,
      'text-background-padding': '1px',
    },
  },
  {
    // ELK's orthogonal route (lib/cy.ts): exact endpoints + bend points
    selector: 'edge.routed',
    style: {
      'curve-style': (e: EdgeSingular) => route(e, 'curve-style'),
      'edge-distances': (e: EdgeSingular) => route(e, 'edge-distances'),
      'source-endpoint': (e: EdgeSingular) => route(e, 'source-endpoint'),
      'target-endpoint': (e: EdgeSingular) => route(e, 'target-endpoint'),
    } as unknown as cytoscape.Css.Edge,
  },
  {
    selector: 'edge.routed.segments',
    style: {
      'segment-weights': (e: EdgeSingular) => route(e, 'segment-weights'),
      'segment-distances': (e: EdgeSingular) => route(e, 'segment-distances'),
    } as unknown as cytoscape.Css.Edge,
  },
  // an edge of a dragged node carries its own weight label (ELK placed the others)
  { selector: 'edge.manual', style: { label: 'data(wl)' } },
  {
    selector: 'node.elabel',
    style: {
      events: 'no',
      shape: 'round-rectangle',
      width: (e: NodeSingular) => labelSize(String(e.data('label'))).width,
      height: (e: NodeSingular) => labelSize(String(e.data('label'))).height,
      'background-color': C.bg,
      'border-width': 0,
      'font-size': 9,
      color: C.muted,
    } as unknown as cytoscape.Css.Node,
  },
  { selector: 'edge.bad', style: { 'line-color': C.error, 'target-arrow-color': C.error } },
  // highlight states (lib/highlight.ts)
  { selector: 'node.selected', style: { 'border-color': '#ffffff', 'border-width': 3 } },
  { selector: 'node.out', style: { 'border-color': C.accent, 'border-width': 2.6 } },
  { selector: 'node.in', style: { 'border-color': C.in, 'border-width': 2.6 } },
  { selector: 'node.focus', style: { 'border-color': '#ffffff', 'border-width': 3 } },
  { selector: 'node.dim', style: { opacity: 0.15 } },
  { selector: 'edge.out', style: { 'line-color': C.accent, 'target-arrow-color': C.accent, 'z-index': 9 } },
  { selector: 'edge.in', style: { 'line-color': C.in, 'target-arrow-color': C.in, 'z-index': 9 } },
  { selector: 'edge.focus', style: { 'line-color': '#ffffff', 'target-arrow-color': '#ffffff', 'z-index': 9 } },
  { selector: 'edge.dim', style: { opacity: 0.06 } },
];

const cy: Core = cytoscape({
  container: $('#graph'),
  style,
  layout: { name: 'preset' },
  minZoom: 0.05,
  maxZoom: 3,
  boxSelectionEnabled: false,
});

const timing = { layout: 0, draw: 0 };

async function render(fit: boolean) {
  const t0 = performance.now();
  const shape = view.nodes.map((n) => n.id + '@' + n.parent).sort().join('|');
  let res;
  try {
    res = fromElk(await host.layout(toElkGraph(view, { spacing: st.spacing })));
  } catch (e) {
    if (e instanceof Superseded) return; // a newer render is on its way
    throw e;
  }
  timing.layout = performance.now() - t0;
  const reset = fit || shape !== lastShape;
  lastShape = shape;
  manual = loadManual();
  const { elements } = toCyElements(view, res, manual);
  const t1 = performance.now();
  cy.batch(() => {
    cy.elements().remove();
    cy.add(elements as cytoscape.ElementDefinition[]);
  });
  if (reset) fitView();
  // force one frame so the draw time is measured
  await new Promise((r) => requestAnimationFrame(() => r(null)));
  timing.draw = performance.now() - t1;
  const ms = Math.round(performance.now() - t0);
  const n = elements.filter((e) => e.group === 'nodes');
  $('#perf').textContent = `${n.length} nodes · ${elements.length - n.length} edges · total ${ms}ms (elk ${Math.round(timing.layout)} · draw ${Math.round(timing.draw)})`;
  $('#perf').dataset.ms = String(ms);
  applyHighlight();
}

/** the existing elements with these ids */
const byIds = (ids: string[]) => ids.reduce((c, id) => c.union(cy.getElementById(id)), cy.collection());

function fitView() {
  cy.fit(undefined, 40);
  if (cy.zoom() > 1.3) {
    cy.zoom(1.3);
    cy.center();
  }
}

async function relayout() {
  // "layout" discards the dragged positions of this view and lets ELK place everything
  manual = {};
  saveManual();
  await render(true);
}

// ---- highlight -------------------------------------------------------------------
const STATES = 'selected out in focus dim';
function setStates(s: States) {
  cy.batch(() => {
    cy.elements().removeClass(STATES);
    for (const [id, cls] of Object.entries(s)) if (cls.length) cy.getElementById(id).addClass(cls.join(' '));
  });
}

function applyHighlight() {
  const f = findings.find((x) => findingKey(x) === activeKey);
  if (f) {
    const { ids, states } = highlightFinding(view, f);
    setStates(states);
    const eles = byIds(ids);
    if (eles.nonempty()) cy.animate({ fit: { eles, padding: 120 } }, { duration: 250 });
    return;
  }
  activeKey = null;
  if (selected && view.nodeOf[selected]) return setStates(highlightPackage(view, view.nodeOf[selected]));
  setStates({});
}

// ---- side panel ----------------------------------------------------------------
function renderFindings() {
  const el = $('#findings');
  $('#f-count').textContent = String(findings.length);
  if (!findings.length) {
    el.innerHTML = '<div class="empty">no violations</div>';
    return;
  }
  el.innerHTML = '';
  for (const g of groupFindings(findings)) {
    const det = document.createElement('details');
    det.className = 'rule-group';
    det.open = openRules.has(g.rule) || g.items.some((f) => findingKey(f) === activeKey);
    const sevColor = g.severity === 'error' ? 'var(--error)' : g.severity === 'warn' ? 'var(--warn)' : 'var(--muted)';
    det.innerHTML = `<summary><i class="sev" style="background:${sevColor}"></i>${esc(g.rule)} <span class="badge">${g.items.length}</span></summary>`;
    det.addEventListener('toggle', () => (det.open ? openRules.add(g.rule) : openRules.delete(g.rule)));
    for (const f of g.items) {
      const on = findingKey(f) === activeKey;
      const d = document.createElement('div');
      d.className = `finding ${f.severity}${on ? ' active' : ''}`;
      d.title = f.message;
      d.innerHTML = `<div class="msg">${esc(f.message)}</div>`;
      d.onclick = () => {
        activeKey = on ? null : findingKey(f);
        selected = null;
        applyHighlight();
        renderFindings();
      };
      det.appendChild(d);
    }
    el.appendChild(det);
  }
}

function renderCrumbs() {
  const el = $('#crumbs');
  const cs = crumbs(rootPath, st.focus);
  el.innerHTML = cs
    .map((c, i) => (i === cs.length - 1 && i > 0 ? `<span class="cur">${esc(c.label)}</span>` : `<a data-p="${esc(c.prefix)}">${esc(c.label)}</a>`))
    .join('<span class="sep">›</span>');
  if (st.focus) el.innerHTML += `<a class="up" data-p="${esc(parentOf(st.focus))}" title="Esc">↑ up</a>`;
  el.querySelectorAll<HTMLElement>('a').forEach((a) => (a.onclick = () => goTo(a.dataset.p ?? '')));
}

const chips = (items: [string, number | ''][]) =>
  `<div class="chips">${items.map(([k, c]) => `<span class="chip" data-k="${esc(k)}">${esc(k)}${c !== '' ? ` <span class="n">${c}</span>` : ''}</span>`).join('')}</div>`;

function bindChips(el: HTMLElement) {
  el.querySelectorAll<HTMLElement>('.chip').forEach(
    (c) =>
      (c.onclick = () => {
        const k = c.dataset.k!;
        const id = view.nodeOf[k] ?? 'g:' + k;
        const n = view.nodes.find((x) => x.id === id);
        if (c.dataset.g || n?.group) return goTo(n?.key ?? k);
        if (!n && !k.startsWith('ext:')) {
          goTo(parentOf(k)).then(() => selectPackage(k));
          return;
        }
        selectPackage(k);
      }),
  );
}

function showDirectory() {
  const el = $('#detail');
  if (!st.focus) {
    el.innerHTML = '<h2>Detail</h2><div class="empty">click a package or a directory</div>';
    return;
  }
  const s = directorySummary(view);
  let html = `<h2>Directory</h2><h3>${esc(st.focus)}/</h3><div class="meta">${s.packages} packages · uses ${s.uses.length} outside · used by ${s.usedBy.length} outside</div>`;
  if (s.usedBy.length) html += `<div class="meta" style="color:var(--in)">used by</div>${chips(s.usedBy)}`;
  if (s.uses.length) html += `<div class="meta" style="color:var(--accent)">uses</div>${chips(s.uses)}`;
  html += `<div class="meta">inside</div><div class="chips">${s.inside
    .filter((n) => !(n.group && n.expanded))
    .map((n) => `<span class="chip" data-k="${esc(n.key)}" data-g="${n.group ? 1 : ''}">${esc(n.label)}${n.group ? ` <span class="n">/ ·${n.members}</span>` : ''}</span>`)
    .join('')}</div>`;
  el.innerHTML = html;
  bindChips(el);
}

async function showPackage(key: string) {
  const el = $('#detail');
  const head = `<h2>Package</h2><h3>${esc(key)}</h3>`;
  if (key.startsWith('ext:')) {
    el.innerHTML = head + '<div class="meta">external dependency</div>';
    return;
  }
  el.innerHTML = head + '<div class="empty">loading…</div>';
  const d: PackageDetail = await host.pkg(key);
  if (selected !== key) return;
  let html = `${head}<div class="meta">${d.files.length} files · imported by ${d.importedBy.length} · imports ${d.imports.length}</div>`;
  if (d.importedBy.length) html += `<div class="meta" style="color:var(--in)">imported by</div>${chips(d.importedBy.map((k) => [k, '']))}`;
  if (d.imports.length) html += `<div class="meta" style="color:var(--accent)">imports</div>${chips(d.imports.map((k) => [k, '']))}`;
  for (const f of d.files) {
    html += `<div class="file"><div class="path">${esc(f.path)} <span style="color:var(--muted)">· ${f.lines} lines</span></div>`;
    for (const s of f.symbols ?? [])
      html += `<div class="sym ${s.exported ? '' : 'private'}"><span class="kind">${esc(s.kind)}</span><span>${esc(s.name)}</span><span class="line">:${s.line}</span></div>`;
    html += '</div>';
  }
  el.innerHTML = html;
  bindChips(el);
}

function selectPackage(key: string) {
  selected = key;
  activeKey = null;
  applyHighlight();
  renderFindings();
  const id = view.nodeOf[key];
  const el = id ? cy.getElementById(id) : null;
  if (el && el.nonempty()) cy.animate({ center: { eles: el } }, { duration: 200 });
  if (!bench) void showPackage(key);
}

// ---- navigation ----------------------------------------------------------------
async function goTo(prefix: string) {
  st = { ...st, focus: prefix, expanded: [] };
  selected = null;
  activeKey = null;
  save();
  await load(true);
  showDirectory();
}

const viewNode = (id: string) => view.nodes.find((n) => n.id === id);

cy.on('tap', 'node', (e: EventObject) => {
  const n = viewNode(e.target.id());
  if (!n) return;
  activeKey = null;
  if (n.group && n.expanded) {
    // click on an opened directory's frame folds it
    st = { ...st, expanded: collapseUnder(st.expanded, n.key) };
    save();
    void load(false);
    return;
  }
  const shift = (e.originalEvent as MouseEvent | undefined)?.shiftKey;
  if (n.group && shift && !n.context) {
    st = { ...st, expanded: [...st.expanded, n.key] };
    save();
    void load(false);
    return;
  }
  if (n.group) return void goTo(n.key); // drill down
  selectPackage(n.key);
});
cy.on('dbltap', 'node', (e: EventObject) => {
  const n = viewNode(e.target.id());
  if (n && !n.group && !n.external) void goTo(parentOf(n.key) === st.focus ? n.key : parentOf(n.key));
});
cy.on('tap', (e: EventObject) => {
  if (e.target !== cy) return;
  selected = null;
  activeKey = null;
  applyHighlight();
  renderFindings();
  showDirectory();
});
document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape' && st.focus && document.activeElement?.tagName !== 'INPUT') void goTo(parentOf(st.focus));
});

// tooltip
const tip = $('#tip');
cy.on('mouseover', 'node', (e: EventObject) => {
  const n = viewNode(e.target.id());
  if (!n) return;
  const what = n.group
    ? `${n.members} packages · ${n.expanded ? 'click the frame to fold' : 'click: drill down · shift+click: open here'}`
    : n.external
      ? 'external'
      : `${n.files} files`;
  tip.innerHTML = `${esc(n.key)}${n.group ? '/' : ''}<div class="m">${what} · ${n.symbols} symbols</div>`;
  tip.style.display = 'block';
});
cy.on('mousemove', (e: EventObject) => {
  if (tip.style.display !== 'block' || !e.renderedPosition) return;
  tip.style.left = e.renderedPosition.x + 14 + 'px';
  tip.style.top = e.renderedPosition.y + 14 + 'px';
});
cy.on('mouseout', 'node', () => (tip.style.display = 'none'));

// ---- dragging ------------------------------------------------------------------
// Nodes and opened directories can be moved. Edges of moved nodes leave the ELK
// route for a side-to-side orthogonal route (class "manual", lib/route.ts) that
// follows while dragging. On release, whatever the moved element (or the frame
// that grew around it) now covers is pushed out of the way (lib/overlap.ts).
// Positions are remembered per project/depth/focus and win over ELK until
// "layout" is pressed.
let manual: Positions = {};
const posKey = () => manualKey(rootPath, view.depth, st.focus);
function loadManual(): Positions {
  try {
    return parseManual(localStorage.getItem(posKey()));
  } catch {
    return {};
  }
}
function saveManual() {
  try {
    if (Object.keys(manual).length) localStorage.setItem(posKey(), JSON.stringify(manual));
    else localStorage.removeItem(posKey());
  } catch {}
}
const leavesOf = (id: string) => byIds(leavesUnder(view, id));

/** side-to-side route for these edges from the current positions */
function reroute(edges: cytoscape.EdgeCollection) {
  const box = (n: NodeSingular) => ({ ...n.position(), w: Number(n.data('w')), h: Number(n.data('h')) });
  cy.batch(() => {
    edges.forEach((e) => {
      const s = e.source();
      const t = e.target();
      const r = routeFor(elbowRoute(box(s), box(t)), s.position(), t.position());
      if (!r) return;
      cy.getElementById('lbl:' + e.id()).remove(); // the edge shows its own label now
      e.removeClass('segments straight').addClass(`routed manual ${r['curve-style']}`).data('route', r);
    });
  });
}

cy.on('grab', 'node', (e: EventObject) => {
  // frames around moved nodes size themselves from their children from now on
  leavesOf(e.target.id()).ancestors('.fixed').removeClass('fixed').addClass('loose');
});
let dragFrame = 0;
cy.on('drag', 'node', (e: EventObject) => {
  if (dragFrame) return; // at most once per animation frame
  const id = e.target.id();
  dragFrame = requestAnimationFrame(() => {
    dragFrame = 0;
    reroute(leavesOf(id).connectedEdges());
  });
});

const rectOf = (n: NodeSingular): Rect => {
  const b = n.boundingBox({ includeLabels: false, includeOverlays: false });
  return { id: n.id(), x1: b.x1, y1: b.y1, x2: b.x2, y2: b.y2 };
};

/** push away what the moved element, and each frame around it, now covers */
function resolveOverlaps(moved: NodeSingular) {
  const shifted = new Set<string>();
  for (let n: NodeSingular | null = moved; n && n.nonempty(); n = n.parent().nonempty() ? (n.parent()[0] as NodeSingular) : null) {
    const siblings = (n.parent().nonempty() ? n.siblings() : cy.nodes().orphans().not(n)).filter((x) => !x.hasClass('elabel'));
    const shifts = pushApart(rectOf(n), siblings.map((x) => rectOf(x as NodeSingular)), 14);
    for (const [id, d] of Object.entries(shifts)) {
      for (const l of leavesUnder(view, id)) {
        const leaf = cy.getElementById(l);
        if (leaf.empty()) continue;
        const p = leaf.position();
        leaf.position({ x: p.x + d.dx, y: p.y + d.dy });
        shifted.add(l);
      }
      cy.getElementById(id).ancestors('.fixed').removeClass('fixed').addClass('loose');
    }
  }
  return shifted;
}

cy.on('free', 'node', (e: EventObject) => {
  const target = e.target as NodeSingular;
  const shifted = resolveOverlaps(target);
  const ids = new Set([...leavesUnder(view, target.id()), ...shifted]);
  const nodes = byIds([...ids]);
  nodes.forEach((l) => {
    manual[l.id()] = { ...l.position() };
  });
  reroute(nodes.connectedEdges());
  saveManual();
});

// ---- minimap -------------------------------------------------------------------
// The whole graph shrunk into a canvas plus the visible area; click or drag on
// it to move the view. Drawn by hand (lib/minimap.ts) rather than a plugin.
const mini = $<HTMLCanvasElement>('#minimap');
const mctx = mini.getContext('2d')!;
let miniFrame = 0;
let miniT = fitTransform({ x1: 0, y1: 0, x2: 1, y2: 1 }, mini.width, mini.height, 8);
function drawMinimap() {
  miniFrame = 0;
  const dpr = window.devicePixelRatio || 1;
  const [w, h] = [mini.clientWidth, mini.clientHeight];
  if (mini.width !== w * dpr) [mini.width, mini.height] = [w * dpr, h * dpr];
  mctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  mctx.clearRect(0, 0, w, h);
  const nodes = cy.nodes().not('.elabel');
  if (nodes.empty()) return;
  miniT = fitTransform(nodes.boundingBox({ includeLabels: false }), w, h, 8);
  const rect = (b: { x1: number; y1: number; x2: number; y2: number }) => {
    const a = miniT.toMini({ x: b.x1, y: b.y1 });
    const c = miniT.toMini({ x: b.x2, y: b.y2 });
    return [a.x, a.y, Math.max(1, c.x - a.x), Math.max(1, c.y - a.y)] as const;
  };
  mctx.strokeStyle = '#3b4257';
  nodes.filter(':parent').forEach((n) => mctx.strokeRect(...rect(n.boundingBox({ includeLabels: false }))));
  nodes.filter(':childless').forEach((n) => {
    mctx.fillStyle = n.hasClass('bad') ? C.error : n.hasClass('god') ? C.warn : n.hasClass('dim') ? '#2a3040' : C.accent;
    mctx.fillRect(...rect(n.boundingBox({ includeLabels: false })));
  });
  mctx.strokeStyle = C.text;
  mctx.lineWidth = 1;
  mctx.strokeRect(...rect(cy.extent()));
}
const scheduleMinimap = () => {
  if (!miniFrame) miniFrame = requestAnimationFrame(drawMinimap);
};
cy.on('viewport add remove position style', scheduleMinimap);
window.addEventListener('resize', scheduleMinimap);
const panTo = (ev: PointerEvent) => {
  const r = mini.getBoundingClientRect();
  const g = miniT.fromMini({ x: ev.clientX - r.left, y: ev.clientY - r.top });
  cy.pan({ x: cy.width() / 2 - g.x * cy.zoom(), y: cy.height() / 2 - g.y * cy.zoom() });
};
mini.addEventListener('pointerdown', (ev) => {
  mini.setPointerCapture(ev.pointerId);
  panTo(ev);
});
mini.addEventListener('pointermove', (ev) => {
  if (mini.hasPointerCapture(ev.pointerId)) panTo(ev);
});

// ---- controls ------------------------------------------------------------------
$('#d-minus').onclick = () => {
  st = { ...st, depth: Math.max(1, (st.depth || view.depth) - 1), expanded: [] };
  save();
  void load(true);
};
$('#d-plus').onclick = () => {
  st = { ...st, depth: (st.depth || view.depth) + 1, expanded: [] };
  save();
  void load(true);
};
$('#collapse').onclick = () => {
  st = { ...st, expanded: [] };
  save();
  void load(true);
};
const setSpacing = (d: number) => {
  st = { ...st, spacing: Math.min(5, Math.max(1, st.spacing + d)) };
  save();
  $('#s-val').textContent = String(st.spacing);
  void render(true);
};
$('#s-minus').onclick = () => setSpacing(-1);
$('#s-plus').onclick = () => setSpacing(1);
$('#relayout').onclick = () => void relayout();
$('#legend-btn').onclick = () => ($('#legend').hidden = !$('#legend').hidden);
let qTimer: number | undefined;
$('#q').addEventListener('input', () => {
  clearTimeout(qTimer);
  qTimer = window.setTimeout(() => void load(false), 200);
});
$<HTMLInputElement>('#ext').addEventListener('change', (e) => {
  st = { ...st, showExt: (e.target as HTMLInputElement).checked };
  save();
  void load(false);
});

// ---- data ----------------------------------------------------------------------
async function load(fit: boolean): Promise<void> {
  if (bench) {
    rootPath = `bench ${bench}`;
    view = synthView(bench, 25);
    // ?weights=1: give edges weights so ELK has labels to place too
    if (params.get('weights')) view.edges = view.edges.map((e, i) => ({ ...e, weight: 2 + (i % 9) }));
    findings = [];
  } else {
    const r: GraphResponse = await host.graph(toGraphQuery(st, $<HTMLInputElement>('#q').value));
    if (r.root !== rootPath) {
      rootPath = r.root;
      restore();
      if (st.depth || st.expanded.length || st.showExt || st.focus) return load(true);
    }
    view = r.view;
    findings = r.findings;
    $('#s-pkgs').textContent = String(r.stats.Packages ?? 0);
    $('#s-files').textContent = String(r.stats.Files ?? 0);
    $('#s-syms').textContent = String(r.stats.Symbols ?? 0);
  }
  $('#root').textContent = rootPath;
  $('#d-val').textContent = st.depth ? String(st.depth) : `auto(${view.depth})`;
  $('#s-val').textContent = String(st.spacing);
  await render(fit);
  renderCrumbs();
  renderFindings();
}

function connect() {
  const live = $('#live');
  host.live({
    ready() {
      live.classList.add('on');
      $('#live-text').textContent = 'live';
    },
    async update() {
      live.classList.add('flash');
      $('#live-text').textContent = 'updating';
      await load(false);
      setTimeout(() => {
        live.classList.remove('flash');
        $('#live-text').textContent = 'live';
      }, 600);
    },
    lost() {
      live.classList.remove('on');
      $('#live-text').textContent = 'reconnecting';
    },
  });
}

// exposed for the screenshot / benchmark scripts
Object.assign(window, { cy, depgraph: { get view() { return view; }, load, goTo, selectPackage, get state() { return st; } } });

/** Starts the UI over host (src/main.ts: the browser, src/mcp-app.ts: an MCP App). */
export function start(h: Host): void {
  host = h;
  void load(true).then(() => {
    showDirectory();
    if (!bench) connect();
  });
}
