// Guard against loading anything from outside (CDN, web fonts): every byte the
// UI runs is bundled from the lockfile-pinned dependencies.
//
// - HTML / CSS, and our own sources (strict): no URL at all.
// - bundled JS: no code or data loaded from a URL (import, importScripts,
//   Worker, fetch, src=). Bare URL strings stay allowed there, as libraries
//   carry them as identifiers (XML namespaces, links in error messages).

const URL_RE = /https?:\/\/[^\s"'`)<>]*/g;
const LOAD_RES = [
  /\bimport\s*\(\s*["'`]https?:/g,
  /\bfrom\s*["'`]https?:/g,
  /\bimportScripts\s*\(\s*["'`]https?:/g,
  /\bnew\s+(?:Shared)?Worker\s*\(\s*["'`]https?:/g,
  /\bfetch\s*\(\s*["'`]https?:/g,
  /\bsrc\s*=\s*["'`]https?:/g,
];

export function externalRefs(path: string, content: string, opts: { strict?: boolean } = {}): string[] {
  if (opts.strict || /\.(html?|css)$/i.test(path)) return content.match(URL_RE) ?? [];
  if (!/\.m?js$/i.test(path)) return [];
  return LOAD_RES.flatMap((re) => content.match(re) ?? []);
}
