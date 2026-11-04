// Guard against loading anything from outside (CDN, web fonts): every byte the
// UI runs is bundled from the lockfile-pinned dependencies.
//
// - HTML / CSS, and our own sources (strict): no URL at all.
// - bundled JS: no code or data loaded from a URL (import, importScripts,
//   Worker, fetch, src=). Bare URL strings stay allowed there, as libraries
//   carry them as identifiers (XML namespaces, links in error messages).
// - inline <script> bodies of HTML are bundled JS (the single-file MCP App);
//   the markup around them is HTML.

const URL_RE = /https?:\/\/[^\s"'`)<>]*/g;
const LOAD_RES = [
  /\bimport\s*\(\s*["'`]https?:/g,
  /\bfrom\s*["'`]https?:/g,
  /\bimportScripts\s*\(\s*["'`]https?:/g,
  /\bnew\s+(?:Shared)?Worker\s*\(\s*["'`]https?:/g,
  /\bfetch\s*\(\s*["'`]https?:/g,
  /\bsrc\s*=\s*["'`]https?:/g,
];

const INLINE_SCRIPT = /<script(?![^>]*\bsrc\s*=)[^>]*>([\s\S]*?)<\/script>/gi;

const loads = (js: string) => LOAD_RES.flatMap((re) => js.match(re) ?? []);

export function externalRefs(path: string, content: string, opts: { strict?: boolean } = {}): string[] {
  if (opts.strict || /\.css$/i.test(path)) return content.match(URL_RE) ?? [];
  if (/\.html?$/i.test(path)) {
    // split, not strip: every byte is checked, either as markup or as script
    const refs: string[] = [];
    let at = 0;
    for (const m of content.matchAll(INLINE_SCRIPT)) {
      refs.push(...(content.slice(at, m.index).match(URL_RE) ?? []), ...loads(m[1]));
      at = m.index + m[0].length;
    }
    return [...refs, ...(content.slice(at).match(URL_RE) ?? [])];
  }
  if (!/\.m?js$/i.test(path)) return [];
  return loads(content);
}
