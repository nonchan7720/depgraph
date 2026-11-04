import { describe, expect, it } from 'vitest';
import { graphQuery } from './api';
import { collapseUnder, defaultState, parseState, toGraphQuery } from './state';

describe('parseState', () => {
  it('falls back to defaults on bad input', () => {
    expect(parseState(null)).toEqual(defaultState());
    expect(parseState('{oops')).toEqual(defaultState());
    expect(parseState('42')).toEqual(defaultState());
  });
  it('restores saved fields and clamps spacing', () => {
    const s = parseState(JSON.stringify({ depth: 3, expanded: ['a'], showExt: true, focus: 'pkg', spacing: 99 }));
    expect(s).toEqual({ depth: 3, expanded: ['a'], showExt: true, focus: 'pkg', spacing: 5 });
  });
});

describe('toGraphQuery', () => {
  it('builds the /api/graph query from the state', () => {
    expect(graphQuery(toGraphQuery({ ...defaultState(), depth: 2, expanded: ['x'], focus: 'f' }, ' q '))).toBe('depth=2&expand=x&q=q&focus=f');
  });
});

describe('collapseUnder', () => {
  it('removes the prefix and everything below it', () => {
    expect(collapseUnder(['a', 'a/b', 'ab', 'c'], 'a')).toEqual(['ab', 'c']);
  });
});
