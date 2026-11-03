import { describe, expect, it } from 'vitest';
import { affectedEdges, leavesUnder, manualKey, mergeManual, parseManual } from './manual';
import { node, sampleView } from './fixture';

describe('manualKey', () => {
  it('scopes positions by project root, depth and focus', () => {
    expect(manualKey('/r', 2, '')).toBe('depgraph:ui:pos:/r|2|');
    expect(manualKey('/r', 3, 'pkg/a')).not.toBe(manualKey('/r', 2, 'pkg/a'));
  });
});

describe('parseManual', () => {
  it('keeps only well-formed points', () => {
    expect(parseManual(JSON.stringify({ a: { x: 1, y: 2 }, b: { x: 'no' }, c: null }))).toEqual({ a: { x: 1, y: 2 } });
    expect(parseManual(null)).toEqual({});
    expect(parseManual('{bad')).toEqual({});
    expect(parseManual('[1]')).toEqual({});
  });
});

describe('mergeManual', () => {
  it('overrides layout positions of nodes that still exist and reports them', () => {
    const r = mergeManual({ a: { x: 0, y: 0 }, b: { x: 5, y: 5 } }, { a: { x: 10, y: 20 }, gone: { x: 1, y: 1 } });
    expect(r.nodes).toEqual({ a: { x: 10, y: 20 }, b: { x: 5, y: 5 } });
    expect([...r.moved]).toEqual(['a']);
  });
});

describe('affectedEdges', () => {
  it('picks edges touching a moved node', () => {
    const edges = [
      { id: 'e1', source: 'a', target: 'b' },
      { id: 'e2', source: 'b', target: 'c' },
      { id: 'e3', source: 'c', target: 'a' },
    ];
    expect(affectedEdges(edges, new Set(['a']))).toEqual(['e1', 'e3']);
    expect(affectedEdges(edges, new Set())).toEqual([]);
  });
});

describe('leavesUnder', () => {
  it('collects the packages inside a box, through nested boxes', () => {
    const v = sampleView();
    v.nodes.push(node({ id: 'g:pkg/a/z', parent: 'g:pkg/a', group: true, expanded: true }), node({ id: 'pkg/a/z/1', parent: 'g:pkg/a/z' }));
    expect(leavesUnder(v, 'g:pkg/a').sort()).toEqual(['pkg/a/x', 'pkg/a/y', 'pkg/a/z/1']);
    expect(leavesUnder(v, 'lib')).toEqual(['lib']);
  });
});

