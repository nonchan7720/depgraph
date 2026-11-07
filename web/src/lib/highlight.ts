// Element states for highlighting. Result maps every element id to its states
// (main.ts turns them into Cytoscape classes).
import type { Finding, View } from './api';

export type States = Record<string, string[]>;

/** Ids of the expanded boxes enclosing id (they must never be dimmed). */
function ancestors(view: View, id: string): string[] {
  const parent = new Map(view.nodes.map((n) => [n.id, n.parent]));
  const out: string[] = [];
  for (let p = parent.get(id); p; p = parent.get(p)) out.push(p);
  return out;
}

function base(view: View): States {
  const s: States = {};
  for (const n of view.nodes) s[n.id] = n.group && n.expanded ? [] : ['dim'];
  for (const e of view.edges) s[e.id] = ['dim'];
  return s;
}

export function highlightPackage(view: View, id: string): States {
  if (!view.nodes.some((n) => n.id === id)) return {};
  const s = base(view);
  const mark = (nid: string, st: string) => {
    if (s[nid]?.[0] === 'dim') s[nid] = [st];
    for (const a of ancestors(view, nid)) s[a] = [];
  };
  for (const e of view.edges) {
    if (e.from === id) {
      s[e.id] = ['out'];
      mark(e.to, 'out');
    } else if (e.to === id) {
      s[e.id] = ['in'];
      mark(e.from, 'in');
    }
  }
  s[id] = ['selected'];
  for (const a of ancestors(view, id)) s[a] = [];
  return s;
}

export function findingPackageKeys(f: Finding): string[] {
  const d = f.data ?? {};
  const strs = (v: unknown) => (Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : []);
  switch (f.rule) {
    case 'import-cycle':
      return strs(d.packages);
    case 'module-cycle':
      // one package import ("a -> b") per hop between two modules
      return strs(d.imports).flatMap((imp) => imp.split(' -> '));
    case 'layer-violation':
    case 'concrete-dependency':
    case 'type-leak':
      return [d.from, d.to].filter((x): x is string => typeof x === 'string');
    case 'god-package':
      return typeof d.pkg === 'string' ? [d.pkg] : [];
    case 'duplicate-symbol':
      return strs(d.files).map((p) => (p.includes('/') ? p.slice(0, p.lastIndexOf('/')) : '.'));
  }
  return [];
}

export function highlightFinding(view: View, f: Finding): { ids: string[]; states: States } {
  const ids = [...new Set(findingPackageKeys(f).map((k) => view.nodeOf[k]).filter(Boolean))];
  const set = new Set(ids);
  const s = base(view);
  for (const id of ids) {
    s[id] = ['focus'];
    for (const a of ancestors(view, id)) s[a] = [];
  }
  for (const e of view.edges) if (set.has(e.from) && set.has(e.to)) s[e.id] = ['focus'];
  return { ids, states: s };
}
