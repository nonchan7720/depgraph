// Turns the built index.html into one self-contained file for MCP Apps: the
// host renders it in a sandbox that loads nothing, so every script and
// stylesheet the page references is inlined (used by vite.config.ts).

const SCRIPT = /<script type="module" crossorigin src="\/([^"]+)"><\/script>/g;
const STYLE = /<link rel="stylesheet" crossorigin href="\/([^"]+)">/g;

/** files maps an output path ("assets/index-x.js") to its content. */
export function inlineHtml(html: string, files: Record<string, string>): string {
  const get = (p: string) => {
    const f = files[p];
    if (f === undefined) throw new Error(`inlineHtml: no content for ${p}`);
    return f;
  };
  return html
    .replace(SCRIPT, (_, p: string) => `<script type="module">${get(p).replace(/<\/script/gi, '<\\/script')}</script>`)
    .replace(STYLE, (_, p: string) => `<style>${get(p)}</style>`);
}
