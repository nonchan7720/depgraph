---
name: fix
description: Fix the debt depgraph reports (import-cycle, module-cycle, layer-violation, concrete-dependency, duplicate-code, duplicate-symbol, god-package, large-package, unused-export, unreachable-file, unreachable-package) the way the codebase needs, not by silencing the rule. Use when depgraph findings appear in the context (session start, a prompt, or a failing `depgraph gate`), or when the user asks to clean up what depgraph found.
argument-hint: "[rule or finding]"
---

# Fixing depgraph findings

Arguments: `$ARGUMENTS` (a rule name or a pasted finding narrows the work to it).

Findings reach you from the plugin's hooks (a summary at session start, what changed at each
prompt) and from `depgraph gate` when the project uses claude-hook-gate: the gate lists only
what **this session added** and blocks the end of the turn until it is fixed or explained.
`depgraph check --claude-md ""` prints every current finding while `depgraph watch` runs. Run
depgraph as `sh "${CLAUDE_PLUGIN_ROOT}/scripts/depgraph" <args>`: the plugin's wrapper runs the
binary of the plugin's version (installing it when it is missing), else `depgraph` on `PATH`.

Fix what you added first. Pre-existing debt is the user's call: mention it, do not start a
refactor nobody asked for.

## Per rule

- **import-cycle** (error): packages that import each other. Find the import that points the
  wrong way (usually a lower-level package that started importing a higher one). Move the shared
  type or function down into the package both can depend on, or invert the dependency with an
  interface owned by the lower package. Never break a cycle by copying code.
- **module-cycle** (error): modules of a monorepo (Go modules, npm workspace packages, Python
  projects, components) that import each other, through directories that do not form a cycle
  themselves. The finding lists one import per hop. Decide which module is the lower one (the
  library the other builds on) and move what it imports from the higher one down into it or into
  a shared package both depend on; never re-implement it on the other side.
- **layer-violation** (error): a layer imports one it must not (`.depgraph.yml` `forbidden`, or
  the defaults such as handler → repository). Route the call through the layer in between
  (handler → usecase → repository) or depend on an interface the inner layer declares. Change the
  layer rules only when the user confirms the architecture changed. When the imported package is
  external (`ext:...`, a framework in an inner layer), keep the framework in an adapter of the outer
  layer behind an interface the inner layer declares, and pass plain types across.
- **concrete-dependency** (warn): a struct / class holds an implementation (a struct or class with
  methods) of a layer it may use only through abstractions (`.depgraph.yml` `abstract`). Declare the
  interface the holder needs on the holder's side (or use the existing port of that package), type
  the field / constructor parameter with it, and let the composition root (`main`, `cmd/`, the DI
  setup) pass the implementation in. Do not just rename or wrap the concrete type.
- **duplicate-code** (warn): the same logic in several places, also under different names. Keep one
  implementation in the place both callers can import, and call it from both. Before writing new
  code, search for an existing function that does the job; this rule exists because agents
  re-implement instead of reusing.
- **duplicate-symbol** (warn): an exported function or class with the same name in several files.
  If they do the same, keep one; if they differ, give them names that say how.
- **god-package** (warn): imported by many packages while importing many itself. Split it by what
  its importers use; move the stable, dependency-free parts (types, interfaces) into their own
  package.
- **large-package** (warn): a package over its line limit. Do not add more to it: put new code
  where its responsibility belongs, and split the package along what its callers use. In an outer
  layer (handlers, commands), move the logic into the use case it belongs to and keep the outer code
  to parsing input and presenting output.
- **unused-export / unreachable-file / unreachable-package** (warn): code nothing in the project
  uses. If you just wrote it and nothing calls it yet, wire it up or remove it; do not leave
  speculative code. If something outside the import graph really uses it (a plugin loaded by name,
  a file an external tool runs, a library's public API), mark it: a `depgraph:entry` comment in the
  file (`// depgraph:entry`, `# depgraph:entry`), or `entries` / `ignore` path globs for the rule in
  `.depgraph.yml`. Say which one it is and why.

## Rules of thumb

- Fix the code, not the report: do not disable a rule, raise a threshold or add an `ignore` to make
  a finding disappear. If a finding is wrong (depgraph misreads the code), say so and show why;
  that is a bug in depgraph worth reporting at https://github.com/nonchan7720/depgraph/issues.
- Keep each fix to what the finding needs, and run the project's own build and tests after it.
- When the gate failed and the fix is not right for this change, say why the finding is intended
  instead of looping on it.
