// Synthetic function-level-sized graph for load testing the renderer (?bench=N).
import type { View, ViewNode } from './api';

function rng(seed: number) {
  let s = seed >>> 0;
  return () => ((s = (s * 1664525 + 1013904223) >>> 0) / 2 ** 32);
}

const pad = (i: number) => String(i).padStart(6, '0');

/** n leaves split into groups of `per`, each in an expanded box; ~1.5 edges per leaf. */
export function synthView(n: number, per: number): View {
  const r = rng(42);
  const nodes: ViewNode[] = [];
  const base = { group: false, expanded: false, external: false, context: false, files: 1, symbols: 1, bad: false, god: false };
  const groups = Math.ceil(n / per);
  for (let g = 0; g < groups; g++) {
    const key = `bench/g${pad(g)}`;
    nodes.push({ ...base, id: 'g:' + key, key, label: key, parent: '', group: true, expanded: true, members: Math.min(per, n - g * per) });
  }
  const leafId = (i: number) => `bench/g${pad(Math.floor(i / per))}/f${pad(i)}`;
  for (let i = 0; i < n; i++) {
    nodes.push({ ...base, id: leafId(i), key: leafId(i), label: `fn${i}`, parent: `g:bench/g${pad(Math.floor(i / per))}`, members: 1 });
  }
  const edges = new Map<string, View['edges'][number]>();
  for (let i = 0; i < n - 1; i++) {
    const k = r() < 0.5 ? 1 : 2;
    for (let j = 0; j < k; j++) {
      // mostly local calls, sometimes far ones
      const span = r() < 0.8 ? 1 + Math.floor(r() * per) : 1 + Math.floor(r() * (n - i - 1));
      const t = Math.min(n - 1, i + span);
      const id = `${leafId(i)}->${leafId(t)}`;
      edges.set(id, { id, from: leafId(i), to: leafId(t), weight: 1, bad: false });
    }
  }
  return { depth: 2, focus: '', nodes, edges: [...edges.values()], nodeOf: Object.fromEntries(nodes.filter((x) => !x.group).map((x) => [x.key, x.id])) };
}
