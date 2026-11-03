// Side panel helpers: findings grouping, breadcrumbs, directory summary.
import type { Finding, View, ViewNode } from './api';

const sevRank: Record<string, number> = { error: 0, warn: 1, info: 2 };
const rank = (s: string) => sevRank[s] ?? 3;

export interface RuleGroup {
  rule: string;
  severity: string;
  items: Finding[];
}

export function groupFindings(findings: Finding[]): RuleGroup[] {
  const by = new Map<string, Finding[]>();
  for (const f of findings) by.set(f.rule, [...(by.get(f.rule) ?? []), f]);
  return [...by.entries()]
    .map(([rule, items]) => {
      const severity = items.reduce((a, f) => (rank(f.severity) < rank(a) ? f.severity : a), items[0].severity);
      return { rule, severity, items };
    })
    .sort((a, b) => rank(a.severity) - rank(b.severity) || a.rule.localeCompare(b.rule));
}

export const findingKey = (f: Finding) => `${f.rule}|${f.id || f.message}`;

export const parentOf = (key: string) => (key.includes('/') ? key.slice(0, key.lastIndexOf('/')) : '');

export interface Crumb {
  label: string;
  prefix: string;
}

export function crumbs(root: string, focus: string): Crumb[] {
  const out: Crumb[] = [{ label: root.split('/').filter(Boolean).pop() || 'root', prefix: '' }];
  const parts = focus ? focus.split('/') : [];
  parts.forEach((p, i) => out.push({ label: p, prefix: parts.slice(0, i + 1).join('/') }));
  return out;
}

export interface DirectorySummary {
  packages: number;
  inside: ViewNode[];
  uses: [string, number][];
  usedBy: [string, number][];
}

export function directorySummary(view: View): DirectorySummary {
  const byId = new Map(view.nodes.map((n) => [n.id, n]));
  const inside = view.nodes.filter((n) => !n.context);
  const uses = new Map<string, number>();
  const usedBy = new Map<string, number>();
  for (const e of view.edges) {
    const a = byId.get(e.from);
    const b = byId.get(e.to);
    if (!a || !b) continue;
    if (!a.context && b.context) uses.set(b.key, (uses.get(b.key) ?? 0) + e.weight);
    if (a.context && !b.context) usedBy.set(a.key, (usedBy.get(a.key) ?? 0) + e.weight);
  }
  const sorted = (m: Map<string, number>) => [...m.entries()].sort((x, y) => y[1] - x[1]);
  return { packages: inside.reduce((s, n) => s + n.members, 0), inside, uses: sorted(uses), usedBy: sorted(usedBy) };
}
