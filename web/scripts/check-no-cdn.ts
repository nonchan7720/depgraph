// usage: node scripts/check-no-cdn.ts <dir>...   (Node >= 23 runs TypeScript directly)
// Fails when the built UI (or any given dir) loads something from a URL.
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { externalRefs } from '../src/lib/nocdn.ts';

let bad = 0;
let files = 0;
const walk = (d: string) => {
  for (const f of readdirSync(d)) {
    const p = join(d, f);
    if (statSync(p).isDirectory()) walk(p);
    else {
      files++;
      for (const r of externalRefs(p, readFileSync(p, 'utf8'))) {
        console.error(`${p}: external reference ${r}`);
        bad++;
      }
    }
  }
};
for (const d of process.argv.slice(2)) walk(d);
if (bad) process.exit(1);
console.log(`no external references in ${files} files`);
