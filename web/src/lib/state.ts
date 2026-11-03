// UI state remembered per repository root (localStorage).
import { graphQuery } from './api';

export interface UIState {
  depth: number; // 0 = auto
  expanded: string[];
  showExt: boolean;
  focus: string;
  spacing: number; // 1..5
}

export const defaultState = (): UIState => ({ depth: 0, expanded: [], showExt: false, focus: '', spacing: 3 });

export function parseState(raw: string | null): UIState {
  const d = defaultState();
  if (!raw) return d;
  let s: unknown;
  try {
    s = JSON.parse(raw);
  } catch {
    return d;
  }
  if (!s || typeof s !== 'object') return d;
  const o = s as Partial<Record<keyof UIState, unknown>>;
  return {
    depth: typeof o.depth === 'number' && o.depth > 0 ? Math.floor(o.depth) : 0,
    expanded: Array.isArray(o.expanded) ? o.expanded.filter((x): x is string => typeof x === 'string') : [],
    showExt: o.showExt === true,
    focus: typeof o.focus === 'string' ? o.focus : '',
    spacing: typeof o.spacing === 'number' ? Math.min(5, Math.max(1, Math.round(o.spacing))) : d.spacing,
  };
}

export function toQuery(s: UIState, q: string): string {
  return graphQuery({ depth: s.depth, expand: s.expanded, ext: s.showExt, q: q.trim(), focus: s.focus });
}

export function collapseUnder(expanded: string[], prefix: string): string[] {
  return expanded.filter((k) => k !== prefix && !k.startsWith(prefix + '/'));
}
