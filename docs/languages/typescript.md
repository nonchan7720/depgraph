# TypeScript / JavaScript

English | [日本語](ja/typescript.md) · [README](../../README.md) · other languages: [Go](go.md), [Python](python.md), [Ruby](ruby.md)

What depgraph reads in TypeScript and JavaScript code, and what the language-specific rules mean for
it. The rules themselves, their options and `.depgraph.yml` are described in the
[README](../../README.md#rules).

## Files

`.ts`, `.tsx`, `.mts`, `.cts`, `.js`, `.jsx`, `.mjs`, `.cjs`. Component files that need a compiler of their
own (`.vue`, `.svelte`, `.astro`, `.marko`) are not parsed.

## Import resolution

Imports resolve with esbuild's resolver:

- `tsconfig.json` / `jsconfig.json` (`paths`, `baseUrl`, `extends`)
- `package.json` (`exports`, `imports`, workspaces)
- `node_modules`
- ESM `./x.js` written for `x.ts`
- a bundler query is not part of the file (`./worker?worker&inline` is `./worker.ts`)

Workspace packages (`workspaces` of `package.json`, `pnpm-workspace.yaml`) resolve to their directory
even before an install (a fresh clone, CI, the gate's worktrees); an installed package of the same name
wins.

A relative path or an alias that points at nothing is unresolved, not external, so it is no dependency.
An import of a package the project does not declare (a bundler or site-generator alias such as
`@site/...`) is matched against the project's paths instead of being dropped.

Each directory with a `package.json` is a module, one of the units of `module-cycle`.

## Third-party packages (`ext:` in layers)

A package resolved into `node_modules` is third-party; so is one that a `package.json` of the file or
above it declares, when it is not installed. A Node built-in, as esbuild resolves it, is not (`punycode`
is the built-in, `punycode/` the installed package). See
[Frameworks](../../README.md#frameworks-external-packages-in-layers).

## Unused code

- **Entry points**:
  - `package.json`: `main` / `module` / `browser` / `types` / `bin` / `exports`, and the files its
    `scripts` run (`dist/` paths are mapped back to their sources with `tsconfig.json`).
  - HTML pages: `<script src>`, and the scripts that page templates name.
  - Config files (`*.config.*`, `.*rc.*`), tests, stories, type tests, `testdata/` and `.d.ts`.
  - The directories `scripts/` `bin/` `tools/` `docs/` `public/` `static/`, `examples/` and benchmarks.
  - `index` / `main` / `server` / `app` / `cli` at the package root or in `src/`.
  - File-system routes: `app/`, `pages/`, `routes/` of Next.js, Remix, Gatsby, Expo Router and others.
  - Docusaurus theme components, React Email templates, and the files MDX files import.
  - In a Rails application, the JavaScript its asset tool starts from (see [Ruby](ruby.md#javascript-of-a-rails-application)).
- **Uses**:
  - `import` / `export ... from` / `require`, followed through re-exports and barrels.
  - Dynamic imports, `require.context` and `import.meta.glob`. With a computed path
    (`` import(`./locales/${l}`) ``, also under an alias: `` `@/widgets/${w}.tsx` ``), every file under
    the directory is used.
  - `new URL('./worker.ts', import.meta.url)`, and source file names in strings.
- **What is reported**:
  - `unused-export`: `export`ed declarations.
  - `unreachable-file`: a file that no entry point reaches.
- **Never reported**:
  - A package that uses Vue, Svelte, Astro, Qwik, Marko, Ember or auto-imports: its component files
    are not parsed, so their imports are not seen.
  - shadcn/ui's copied component kit (vendored code).
  - An export that only its own file uses.

### Private workspace packages

In a monorepo, a `private` workspace package that another package of the repository depends on is never
published, so only the repository uses it:

- Its `main` / `exports` / `types` and `index` files stay entry points for reachability, but their
  exports are reported when no other package uses them.
- Its `bin` and `scripts` files are still run from outside.
- Published packages, private apps that nothing depends on, and packages whose files a framework loads by
  name (an Ember app) keep their entries' exports.

## Other rules

- `concrete-dependency` and `type-leak` read class fields, `constructor` parameters, parameters and
  results, and `new T()`. An implementation satisfies an interface when its class says it implements or
  extends it. See [Inside the layers](../../README.md#inside-the-layers-dependency-inversion-and-leaking-types).
- `low-cohesion` splits a module by its top-level declarations.

## Language server

During `watch`, `typescript-language-server` adds calls and references between symbols for the changed
files (optional).
