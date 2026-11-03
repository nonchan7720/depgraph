import { describe, expect, it } from 'vitest';
import { edgeWidth, labelOf, nodeSize } from './transform';
import { node } from './fixture';

describe('labelOf', () => {
  it('shows a folded group with its member count', () => {
    expect(labelOf(node({ id: 'g:pkg/b', group: true, members: 3 }))).toBe('pkg/b/ ·3');
  });
  it('shows an expanded group as a directory', () => {
    expect(labelOf(node({ id: 'g:pkg/a', group: true, expanded: true }))).toBe('pkg/a/');
  });
  it('shows a package by its label', () => {
    expect(labelOf(node({ id: 'pkg/a/x', label: 'x' }))).toBe('x');
  });
});

describe('nodeSize', () => {
  it('grows with the label length and has a minimum', () => {
    const [w1, h1] = nodeSize('x');
    const [w2, h2] = nodeSize('a-very-long-package-name');
    expect(w1).toBeGreaterThanOrEqual(40);
    expect(w2).toBeGreaterThan(w1);
    expect(h1).toBe(h2);
  });
});

describe('edgeWidth', () => {
  it('is thicker for heavier edges and capped', () => {
    expect(edgeWidth(1)).toBeLessThan(edgeWidth(8));
    expect(edgeWidth(100000)).toBeLessThanOrEqual(6);
  });
});
