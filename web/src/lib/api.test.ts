import { describe, expect, it } from 'vitest';
import { graphQuery } from './api';

describe('graphQuery', () => {
  it('omits empty options', () => {
    expect(graphQuery({})).toBe('');
  });
  it('encodes all options', () => {
    expect(graphQuery({ depth: 2, expand: ['a', 'b/c'], ext: true, q: 'x', focus: 'pkg' })).toBe(
      'depth=2&expand=a%2Cb%2Fc&ext=1&q=x&focus=pkg',
    );
  });
});
