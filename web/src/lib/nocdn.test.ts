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

  it('checks inline scripts of an HTML file as bundled JS (the single-file MCP App)', () => {
    const page = (js: string, rest = '') => `<html>${rest}<script type="module">${js}</script></html>`;
    expect(externalRefs('index.html', page('const ns = "http://www.eclipse.org/elk/ElkGraph"'))).toEqual([]);
    expect(externalRefs('index.html', page('fetch("https://cdn.example/d")'))).toHaveLength(1);
    // the markup around the scripts stays strict
    expect(externalRefs('index.html', page('', '<link href="https://fonts.example/css">'))).toHaveLength(1);
    expect(externalRefs('index.html', page('') + '<script src="https://cdn.example/x.js"></script>')).toHaveLength(1);
    // a tag split around an inline script still shows its URL
    expect(externalRefs('index.html', '<scr<script>1</script>ipt src="https://cdn.example/x.js"></script>')).toHaveLength(1);
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
