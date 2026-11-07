import { describe, expect, it } from 'vitest';
import { findingPackageKeys, highlightFinding, highlightPackage } from './highlight';
import { sampleView } from './fixture';
import type { Finding } from './api';

describe('highlightPackage', () => {
  const v = sampleView();
  const s = highlightPackage(v, 'pkg/a/x');

  it('marks the selection, what it imports (out) and what imports it (in)', () => {
    expect(s['pkg/a/x']).toEqual(['selected']);
    expect(s['pkg/a/y']).toEqual(['out']);
    expect(s['g:pkg/b']).toEqual(['out']);
    expect(s['lib']).toEqual(['in']);
    expect(s['pkg/a/x->pkg/a/y']).toEqual(['out']);
    expect(s['lib->pkg/a/x']).toEqual(['in']);
  });

  it('keeps the enclosing combo undimmed and dims the rest', () => {
    const t = highlightPackage(v, 'pkg/a/y');
    expect(t['g:pkg/a']).toEqual([]);
    expect(t['lib']).toEqual(['dim']);
    expect(t['lib->pkg/a/x']).toEqual(['dim']);
  });

  it('returns no states for an unknown node', () => {
    expect(highlightPackage(v, 'none')).toEqual({});
  });
});

describe('findingPackageKeys', () => {
  const f = (rule: string, data: Record<string, unknown>): Finding => ({ rule, severity: 'error', message: '', id: '', data });
  it('reads each rule\'s data', () => {
    expect(findingPackageKeys(f('import-cycle', { packages: ['a', 'b', 'a'] }))).toEqual(['a', 'b', 'a']);
    expect(findingPackageKeys(f('module-cycle', { modules: ['m', 'n', 'm'], imports: ['m/x -> n/y', 'n/y -> m/z'] }))).toEqual([
      'm/x',
      'n/y',
      'n/y',
      'm/z',
    ]);
    expect(findingPackageKeys(f('layer-violation', { from: 'a', to: 'b' }))).toEqual(['a', 'b']);
    expect(findingPackageKeys(f('god-package', { pkg: 'a' }))).toEqual(['a']);
    expect(findingPackageKeys(f('duplicate-symbol', { files: ['x/y/z.go', 'main.go'] }))).toEqual(['x/y', '.']);
    expect(findingPackageKeys(f('unknown', {}))).toEqual([]);
    expect(findingPackageKeys({ rule: 'god-package', severity: 'warn', message: '', id: '' })).toEqual([]);
  });
});

describe('highlightFinding', () => {
  it('marks the involved visible nodes and the edges between them', () => {
    const v = sampleView();
    const r = highlightFinding(v, { rule: 'layer-violation', severity: 'error', message: '', id: '', data: { from: 'lib', to: 'pkg/a/x' } });
    expect(r.ids).toEqual(['lib', 'pkg/a/x']);
    expect(r.states['lib']).toEqual(['focus']);
    expect(r.states['pkg/a/x']).toEqual(['focus']);
    expect(r.states['lib->pkg/a/x']).toEqual(['focus']);
    expect(r.states['pkg/a/y']).toEqual(['dim']);
    expect(r.states['g:pkg/a']).toEqual([]);
  });
  it('maps packages hidden in a folded group to the group', () => {
    const v = sampleView();
    const r = highlightFinding(v, { rule: 'god-package', severity: 'warn', message: '', id: '', data: { pkg: 'pkg/b/2' } });
    expect(r.ids).toEqual(['g:pkg/b']);
  });
});
