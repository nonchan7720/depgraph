import type { View, ViewNode } from './api';

export function node(p: Partial<ViewNode> & { id: string }): ViewNode {
  return {
    key: p.id.replace(/^g:/, ''),
    label: p.id.replace(/^g:/, ''),
    parent: '',
    group: false,
    expanded: false,
    external: false,
    context: false,
    members: 1,
    files: 1,
    symbols: 1,
    bad: false,
    god: false,
    ...p,
  };
}

/**
 * pkg/a (expanded box) contains pkg/a/x and pkg/a/y; pkg/b is a folded group;
 * lib is a package. Edges: x -> y, x -> g:pkg/b, lib -> x (bad).
 */
export function sampleView(): View {
  return {
    depth: 2,
    focus: '',
    nodes: [
      node({ id: 'g:pkg/a', group: true, expanded: true, members: 2 }),
      node({ id: 'g:pkg/b', group: true, members: 3, god: true }),
      node({ id: 'lib', bad: true }),
      node({ id: 'pkg/a/x', label: 'x', parent: 'g:pkg/a' }),
      node({ id: 'pkg/a/y', label: 'y', parent: 'g:pkg/a' }),
    ],
    edges: [
      { id: 'lib->pkg/a/x', from: 'lib', to: 'pkg/a/x', weight: 1, bad: true },
      { id: 'pkg/a/x->g:pkg/b', from: 'pkg/a/x', to: 'g:pkg/b', weight: 4, bad: false },
      { id: 'pkg/a/x->pkg/a/y', from: 'pkg/a/x', to: 'pkg/a/y', weight: 1, bad: false },
    ],
    nodeOf: {
      lib: 'lib',
      'pkg/a/x': 'pkg/a/x',
      'pkg/a/y': 'pkg/a/y',
      'pkg/b/1': 'g:pkg/b',
      'pkg/b/2': 'g:pkg/b',
      'pkg/b/3': 'g:pkg/b',
    },
  };
}
