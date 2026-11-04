import { describe, expect, it } from 'vitest';
import { inlineHtml } from './inline';

describe('inlineHtml', () => {
  const html =
    '<head><script type="module" crossorigin src="/assets/index-a1.js"></script>\n' +
    '<link rel="stylesheet" crossorigin href="/assets/index-b2.css"></head>';

  it('inlines the entry script and the stylesheet', () => {
    const out = inlineHtml(html, { 'assets/index-a1.js': 'console.log(1)', 'assets/index-b2.css': 'a{color:red}' });
    expect(out).toBe('<head><script type="module">console.log(1)</script>\n<style>a{color:red}</style></head>');
  });

  it('keeps $ patterns and escapes closing script tags', () => {
    const out = inlineHtml(html, { 'assets/index-a1.js': 'x="$&$1</script>"', 'assets/index-b2.css': '' });
    expect(out).toContain('<script type="module">x="$&$1<\\/script>"</script>');
  });

  it('fails on a reference it cannot inline', () => {
    expect(() => inlineHtml(html, { 'assets/index-a1.js': '' })).toThrow('assets/index-b2.css');
  });
});
