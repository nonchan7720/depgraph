---
name: fix
description: Fix the debt depgraph reports (import-cycle, module-cycle, file-cycle, function-cycle, pass-through, wide-function, misplaced-function, layer-violation, concrete-dependency, type-leak, duplicate-code, duplicate-symbol, god-package, low-cohesion, unused-export, unreachable-file, unreachable-package) the way the codebase needs, not by silencing the rule. Use when depgraph findings appear in the context (session start, a prompt, or a failing `depgraph gate`), or when the user asks to clean up what depgraph found.
argument-hint: "[rule or finding]"
---

# Fixing depgraph findings

Arguments: `$ARGUMENTS` (a rule name or a pasted finding narrows the work to it; in Codex, which
has no arguments, what the user asked for).

Findings reach you from the plugin's hooks (a summary at session start, what changed at each
prompt) and from `depgraph gate` when the project uses claude-hook-gate: the gate lists only
what **this session added** and blocks the end of the turn until it is fixed or explained.
`depgraph check --claude-md ""` prints every current finding while `depgraph watch` runs. Run
depgraph as `sh "${CLAUDE_PLUGIN_ROOT}/scripts/depgraph" <args>`: the plugin's wrapper runs the
binary of the plugin's version (installing it when it is missing), else `depgraph` on `PATH`. In
Codex, where `CLAUDE_PLUGIN_ROOT` is not set, use the absolute path of the plugin root instead: the
directory two levels above this `SKILL.md` (`<plugin root>/skills/fix/SKILL.md`).

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
- **file-cycle** (warn): files of one directory (TypeScript / JavaScript or Python modules) that
  load each other. Find what the two sides need from each other: move the shared part into a
  module neither imports back, or pass the dependency in (a parameter, a callback) instead of
  importing it. An import that is only for types becomes `import type` / goes under
  `if TYPE_CHECKING:`. Do not hide the cycle behind an import inside a function, and do not
  merge the files into one that does everything.
- **function-cycle** (info): Go functions declared in different files of a package that call
  each other in a cycle. When it is one recursive algorithm, keep its functions in one file.
  When one side only needs a step of the other, pass that step in as a function parameter (or
  an interface the caller implements) instead of calling back. Do not inline one function into
  the other to make the finding disappear.
- **pass-through** (warn): a chain of two or more functions that only pass their parameters on
  to the next; the last function of the finding does the work. Make the callers of the first
  call the last one (or the one layer that is the intended API) and delete the functions in
  between. When a layer is there on purpose (a public API in front of an internal package),
  keep that one and remove the others. Do not add a statement to a wrapper to make it look like
  more than a wrapper.
- **wide-function** (info): a function that calls functions of more packages of the repository
  than `maxPackages` (default 8). Group the calls by what they are for and move each group into a
  function of the package that owns that concern, so the function coordinates a few steps
  instead of knowing every package. Only a function that is wide on purpose (the composition
  root that wires the application) keeps its shape: put `depgraph:ignore wide-function` and the
  reason in a comment on it. Do not add that comment to make a finding go away.
- **layer-violation** (error): a layer imports one it must not (`.depgraph.yml` `forbidden`, or
  the defaults such as handler → repository). Route the call through the layer in between
  (handler → usecase → repository) or depend on an interface the inner layer declares. Change the
  layer rules only when the user confirms the architecture changed. When the imported package is
  external (`ext:...`, a framework in an inner layer), keep the framework in an adapter of the outer
  layer behind an interface the inner layer declares, and pass plain types across.
- **concrete-dependency** (warn): a struct / class holds (a field, a constructor parameter) or builds
  an implementation of a layer it may use only through interfaces (`.depgraph.yml` `abstract`), and the
  finding names the interface that implementation already satisfies. Type the field / parameter with that
  interface and let the composition root (`main`, `cmd/`, the DI setup) build and pass the implementation.
  Do not wrap or rename the concrete type.
- **type-leak** (warn): the API of a layer (parameters and results of exported functions / methods,
  exported fields) exposes a struct or class of a layer it may use only through interfaces. Return or
  take a type of the layer itself (or the domain's), and map at the boundary; or expose an interface.
- **duplicate-code** (warn): the same logic in several places, also under different names. Keep one
  implementation in the place both callers can import, and call it from both. Before writing new
  code, search for an existing function that does the job; this rule exists because agents
  re-implement instead of reusing.
- **duplicate-symbol** (warn): an exported function or class with the same name in several files.
  If they do the same, keep one; if they differ, give them names that say how.
- **god-package** (warn): imported by many packages while importing many itself. Split it by what
  its importers use; move the stable, dependency-free parts (types, interfaces) into their own
  package.
- **low-cohesion** (warn): a package (Go) or module (TS / Python file) whose parts nothing inside
  connects is used by different code for each part. Move each part to the package / module its users
  need (or a new one named after what it does), keep the names, update the imports. Do not add code
  that joins the parts just to silence it.
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
