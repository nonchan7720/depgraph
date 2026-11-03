import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { externalRefs } from './nocdn';

describe('externalRefs', () => {
  it('rejects any URL in HTML and CSS', () => {
    expect(externalRefs('index.html', '<script src="https://cdn.example/x.js"></script>')).toHaveLength(1);
    expect(externalRefs('a.css', '@import url(https://fonts.example/css);')).toHaveLength(1);
    expect(externalRefs('a.css', 'body { font: 12px system-ui; }')).toEqual([]);
  });

  it('rejects loading code from a URL in JS', () => {
    for (const js of [
      'import x from "https://cdn.example/x.js";',
      'await import("https://cdn.example/x.js")',
      "importScripts('https://cdn.example/elk.js')",
      'new Worker("https://cdn.example/w.js")',
      'fetch(`https://cdn.example/data`)',
      's.src = "http://cdn.example/x.js"',
    ])
      expect(externalRefs('a.js', js), js).toHaveLength(1);
  });

  it('accepts URLs that are only identifiers in bundled libraries', () => {
    expect(externalRefs('a.js', 'const ns = "http://www.w3.org/2001/XMLSchema#int"; throw Error("see https://example.org/doc")')).toEqual([]);
  });

  it('is strict for our own sources: any URL counts', () => {
    expect(externalRefs('src/main.ts', 'const x = "https://example.org"', { strict: true })).toHaveLength(1);
  });
});

describe('web sources', () => {
  it('reference nothing outside (no CDN, system fonts only)', () => {
    const root = join(__dirname, '..', '..');
    const files = [join(root, 'index.html')];
    const walk = (d: string) => {
      for (const f of readdirSync(d)) {
        const p = join(d, f);
        if (statSync(p).isDirectory()) walk(p);
        else if (!p.endsWith('.test.ts')) files.push(p);
      }
    };
    walk(join(root, 'src'));
    for (const f of files) expect(externalRefs(f, readFileSync(f, 'utf8'), { strict: true }), f).toEqual([]);
  });
});
