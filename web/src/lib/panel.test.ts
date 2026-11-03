import { describe, expect, it } from 'vitest';
import { crumbs, directorySummary, findingKey, groupFindings, parentOf } from './panel';
import { node, sampleView } from './fixture';
import type { Finding } from './api';

const f = (rule: string, severity: string, id = rule): Finding => ({ rule, severity, message: id, id });

describe('groupFindings', () => {
  it('groups by rule, most severe first then by name', () => {
    const g = groupFindings([f('dup', 'warn', '1'), f('cycle', 'error'), f('dup', 'warn', '2'), f('god', 'warn'), f('x', 'info')]);
    expect(g.map((x) => [x.rule, x.severity, x.items.length])).toEqual([
      ['cycle', 'error', 1],
      ['dup', 'warn', 2],
      ['god', 'warn', 1],
      ['x', 'info', 1],
    ]);
  });
});

describe('findingKey', () => {
  it('uses rule and id, falling back to message', () => {
    expect(findingKey(f('r', 'warn', 'i'))).toBe('r|i');
    expect(findingKey({ rule: 'r', severity: 'warn', message: 'm', id: '' })).toBe('r|m');
  });
});

describe('parentOf', () => {
  it('drops the last segment', () => {
    expect(parentOf('a/b/c')).toBe('a/b');
    expect(parentOf('a')).toBe('');
  });
});

describe('crumbs', () => {
  it('starts at the repository and ends at the focus', () => {
    expect(crumbs('/home/me/repo', 'pkg/a')).toEqual([
      { label: 'repo', prefix: '' },
      { label: 'pkg', prefix: 'pkg' },
      { label: 'a', prefix: 'pkg/a' },
    ]);
    expect(crumbs('', '')).toEqual([{ label: 'root', prefix: '' }]);
  });
});

describe('directorySummary', () => {
  it('sums connections between inside and context nodes', () => {
    const v = sampleView();
    v.focus = 'pkg/a';
    v.nodes.push(node({ id: 'ctx/1', context: true }), node({ id: 'ctx/2', context: true }));
    v.edges.push(
      { id: 'pkg/a/x->ctx/1', from: 'pkg/a/x', to: 'ctx/1', weight: 2, bad: false },
      { id: 'pkg/a/y->ctx/1', from: 'pkg/a/y', to: 'ctx/1', weight: 3, bad: false },
      { id: 'ctx/2->pkg/a/y', from: 'ctx/2', to: 'pkg/a/y', weight: 1, bad: false },
    );
    const s = directorySummary(v);
    expect(s.uses).toEqual([['ctx/1', 5]]);
    expect(s.usedBy).toEqual([['ctx/2', 1]]);
    expect(s.packages).toBe(1 + 3 + 1 + 1 + 1 + 1); // members of every non-context node incl. the box
    expect(s.inside.map((n) => n.id)).not.toContain('ctx/1');
  });
});
