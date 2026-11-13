# Rules

English | [日本語](ja/rules.md) · [README](../README.md)

What depgraph reports. Every rule can be switched off or given another severity in [`.depgraph.yml`](configuration.md); what a rule means in each language is on the language pages ([Go](languages/go.md), [TypeScript / JavaScript](languages/typescript.md), [Python](languages/python.md), [Ruby](languages/ruby.md)). How the rules are evaluated is in [Architecture](architecture.md#how-the-rules-are-evaluated).

## All rules

| rule | severity | reports |
|---|---|---|
| [`import-cycle`](#import-cycle) | error | packages that import each other in a cycle |
| [`module-cycle`](#module-cycle) | error | modules of a monorepo that import each other in a cycle |
| [`file-cycle`](#file-cycle) | warn | files of one directory that import each other in a cycle |
| [`function-cycle`](#function-cycle) | info | Go functions in different files of a package that call each other in a cycle |
| [`layer-violation`](#layer-violation) | error | a package imports a layer it must not import |
| [`concrete-dependency`](#concrete-dependency) | warn | a type holds or builds an implementation where it may only use an interface |
| [`type-leak`](#type-leak) | warn | the API of a layer exposes a type of a layer it may only use through interfaces |
| [`external-unclassified`](#external-unclassified) | info | imports an `ext:` wildcard could not classify as third-party |
| [`duplicate-code`](#duplicate-code) | warn | the same logic in several places, whatever the names |
| [`duplicate-symbol`](#duplicate-symbol) | warn | an exported function / class with the same name in several files |
| [`unused-export`](#unused-export) | warn | an exported declaration nothing in the project uses |
| [`unreachable-file`](#unreachable-file) | warn | a file no entry point reaches through imports |
| [`unreachable-package`](#unreachable-package) | warn | a Go package no entry point imports |
| [`unused-dependency`](#unused-dependency) | error / warn | a dependency a manifest declares that nothing imports or names |
| [`god-package`](#god-package) | warn | a hub: imported by many packages and importing many |
| [`low-cohesion`](#low-cohesion) | info | a package or module made of unrelated parts used by different code |
| [`wide-function`](#wide-function) | info | a function that calls into too many packages |
| [`pass-through`](#pass-through) | warn | a chain of functions that only pass their parameters on |

## Cycles

### `import-cycle`

Default severity: `error`

Import cycle between packages: for every import on a cycle of at most 8 packages, the shortest cycle through it (up to 50)

### `module-cycle`

Default severity: `error`

Import cycle between the modules of a monorepo (Go modules, npm workspace packages, Python projects, Ruby applications / gems / engines / packwerk packs: directories with `go.mod`, `package.json`, `pyproject.toml`, `setup.py`, `setup.cfg`, `Gemfile`, `*.gemspec` or `package.yml`, plus [components](configuration.md#monorepos-components)) through directories that do not form a cycle themselves (`packages/web/src/a -> packages/kit/src -> packages/web/src/b`). One import is listed per hop; a cycle `import-cycle` already reports is not repeated.

### `file-cycle`

Default severity: `warn`

Import cycle between the files of one directory (TypeScript / JavaScript and Python modules), which `import-cycle` cannot see because it compares directories: for every import on a cycle of at most 8 files, the shortest cycle through it (up to 50). Only imports that load the module count: not one that is only for types (`import type`, names used only as types, `if TYPE_CHECKING:`) and not `import()`. A Python cycle that only closes through an import inside a function is reported as hidden by that file (a deferred import hides a cycle, it does not remove it); `deferred: false` leaves those out. Go has no such cycles and Ruby constants refer to each other by design, so neither is checked.

### `function-cycle`

Default severity: `info`

Go functions declared in different files of a package that call each other in a cycle (`parse.go Parse -> expr.go expr -> parse.go Parse`): one piece of logic spread over files. Recursion inside one file is not reported, nor a cycle among the methods of one type (a type declared over several files is one unit). Calls are read from the syntax: `f()`, `pkg.F()` and, in a method, `recv.m()`. A method called on a field of the receiver or on a parameter (`s.repo.Find()`, `r.Find()`) is followed by the member's declared type: to the method of a struct / class, or through an interface to its implementation when exactly one type of the project implements it. With several implementations which one runs is not known, and the call is not followed. In TypeScript / JavaScript and Python such a cycle needs imports, so it is a `file-cycle` or `import-cycle` already.

## Layers

Layers are declared in [`.depgraph.yml`](configuration.md).

### `layer-violation`

Default severity: `error`

A package imports a layer it must not import (`handler → repository`, ... or your own layers). Layers may name external packages, so a domain that imports a framework is one too (see [Frameworks](configuration.md#frameworks-external-packages-in-layers))

### `concrete-dependency`

Default severity: `warn`

A struct / class holds (field, constructor parameter) or builds an implementation of a layer it may use only through interfaces (`abstract:`), although that implementation satisfies an interface declared in the holder's package, its own package or the inner layer (see [Inside the layers](configuration.md#inside-the-layers-dependency-inversion-and-leaking-types))

### `type-leak`

Default severity: `warn`

The API of a layer (parameters and results of exported functions / methods, exported fields) exposes a struct or class of a layer it may use only through interfaces (`abstract:`)

### `external-unclassified`

Default severity: `info`

The imports a wildcard external layer (`ext:*`, `ext:github.com/aws/*`) would match if they were known to be third-party, but that could not be classified: listed per language with the reason, instead of being reported as a violation or silently dropped (see [Frameworks](configuration.md#frameworks-external-packages-in-layers))

## Duplication

How copies are found is in [Duplicated code](#duplicated-code).

### `duplicate-code`

Default severity: `warn`

The same logic in several places, whatever the names: identical, or a copy changed a little (see [Duplicated code](#duplicated-code))

### `duplicate-symbol`

Default severity: `warn`

An exported function/class with the same name exists in several files (the files of one Go package count once: they can only share a name through build constraints; Ruby is left out, reopening a class is how Ruby extends it)

## Unused code rules

What counts as an entry point and as a use is in [Unused code](#unused-code).

### `unused-export`

Default severity: `warn`

An exported declaration nothing in the project uses, not even its own file (Go: exported functions and types of non-main packages, not methods; TS/JS: `export`ed declarations, followed through re-exports and barrels; Python: public module-level functions and classes; Ruby: classes, modules with something in them and `Struct.new` / `Class.new` constants, never methods)

### `unreachable-file`

Default severity: `warn`

A TS/JS/Python/Ruby file no entry point reaches through imports; one finding per file nothing imports, listing the files only it leads to.

### `unreachable-package`

Default severity: `warn`

A Go package no `main` package, test or public package of a library module imports.

### `unused-dependency`

Default severity: `error` when the finding is certain, `warn` when it is not

A dependency that `go.mod` (a direct `require`), `package.json` (`dependencies`, `devDependencies`, `optionalDependencies`), `pyproject.toml` / a requirements file or a `Gemfile` declares, that no code imports and that nothing else names. What counts as a use, and when a finding is certain, is in [Unused dependencies](#unused-dependencies).

## Structure that rots

### `god-package`

Default severity: `warn`

A hub: imported by 10 or more packages and importing 3 or more in-repo packages. A package many depend on that depends on nothing in the repository (a domain model) is stable, not a god package (Stable Dependencies Principle)

### `low-cohesion`

Default severity: `info`

A Go package (its files) or a TypeScript / Python module (its top-level declarations) splits into parts that nothing inside connects, and every part is used by different code (no user takes two parts): more than one responsibility in one place. A size limit is a linter's; only the whole project shows who uses which part. Tests, generated files, entry points, a unit used whole (a dot import, `export *`, a namespace used as a value) and a unit of data only (entities / DTOs used one per consumer: types without behaviour, data classes, constants, constructors) are left out.

### `wide-function`

Default severity: `info`

A function or method that calls functions of more than `maxPackages` (default 8) packages of the repository, its own not counted: it knows too much of the project to change or test alone. A method called on a field or a parameter counts the package that declares the member's type (the interface), not the packages that implement it. Calls of constructors (Go `NewX`, `new C()`, a Python class) are not counted: building its parts from many packages is wiring, not knowing what they do. Calls are read as in `function-cycle`, in Go, TypeScript / JavaScript and Python. A function that is wide on purpose (the composition root) is exempted by a comment, see [Exempting a function](#exempting-a-function-depgraphignore). Test code is left out.

### `pass-through`

Default severity: `warn`

A chain of two or more functions that only pass their parameters on to the next (`api.Get -> service.Get -> load`): the body is one call with exactly the function's parameters, in order, whose result is returned. Layers that add a name and nothing else. One wrapper is an API choice and is not reported; methods are never counted (they may be there to satisfy an interface), nor decorated Python functions. Calls are followed as in `function-cycle`, in Go, TypeScript / JavaScript and Python. Test code is left out.

## Unused code

`unused-export` and `unreachable-*` report what only the whole project shows: the compiler,
`go vet` / staticcheck, `tsc --noUnusedLocals`, eslint and ruff see an unused local or unexported
name, never an export nothing imports. A finding that is not true is a bug, so a file is an entry
point (reachable, its exports used from outside, never reported) whenever there is a sign that
something outside the import graph loads it:

- Per language, what is an entry point, what counts as a use, what is reported and what is kept on
  purpose:
  - [Go](languages/go.md#unused-code): `main` packages, tests, the public packages of a library module.
  - [TypeScript / JavaScript](languages/typescript.md#unused-code): `package.json` entries and scripts,
    HTML pages, config files, tests, file-system routes, private workspace packages.
  - [Python](languages/python.md#unused-code): `__main__`, tests, scripts and entry points of
    `pyproject.toml` / `setup.cfg`, Django's autodiscovered modules, `__all__`.
  - [Ruby](languages/ruby.md#unused-code): constants as uses (also in associations, views and names built at run time), Rails
    conventions, gems, packwerk public paths, concerns, library namespaces.
- Everywhere: files and modules that Dockerfiles, Procfiles, Makefiles, compose files, CI workflows,
  `mise.toml` and shell scripts run (`node dist/server.js`, `python -m pkg.tool`,
  `uvicorn app.main:app`), and paths or dotted module names in strings (`new URL('./worker.ts', ...)`,
  `"app.tasks.send"`, `importlib.import_module(f"app.plugins.{name}")`, `` import(`./locales/${l}`) ``),
  and source file names in strings of any language (`"render.js"` in a Go program that bundles it).
- A file whose header says it is generated is not reported.

When a file is loaded in a way depgraph cannot see (a plugin loaded by name, a file run by an
external tool), put a `depgraph:entry` comment in it (`// depgraph:entry`, `# depgraph:entry`). A
library's public API that its own repository does not use is left out with `ignore`, and entry points
are added with `entries`, in `.depgraph.yml` (path globs over files and Go package directories, shared
by the three rules; see [Configuration](configuration.md)). An export only its own file uses is not reported (the
export is unneeded, but that is style: Go handlers registered in the same file, for example).

Layers, forbidden imports, severities and thresholds are set in `.depgraph.yml` ([Configuration](configuration.md)).

## Unused dependencies

`unused-dependency` reports what is left in a manifest when code is removed or rewritten and the
manifest is not: packages that are installed, audited and updated for nothing. A compiler sees the
imports of one file, never the manifest.

In `go.mod` and `package.json` an import names its package exactly (a Go import path starts with
the module's path, a JavaScript import with the package's name). Python distributions and Ruby gems
are imported under names their manifests do not give (`PyYAML` is `yaml`, `rack-cors` is
`Rack::Cors`), so those names are read from where the project installs them **inside the
repository**: the `dist-info` of a virtual environment (`.venv`, `venv`) for `pyproject.toml`
(`[project]`, `[dependency-groups]`, Poetry) and `requirements*.txt` / `requirements/*.txt`, and
the files of a vendored bundle (`vendor/bundle`, or `BUNDLE_PATH` of `.bundle/config`) for a
`Gemfile`. A dependency whose names are not known is skipped, never reported: one that is not
installed there, or a distribution that shares its only module with another (`google.*`). Without
such an environment, nothing of that manifest is reported.

A dependency is **used**, and never reported, when:

- a file imports it. An import uses the declaration of the nearest manifest that names the package:
  the file's own `go.mod`, or the nearest `package.json` at or above it (a workspace package uses
  what a `package.json` above it installs, unless it declares the package itself). When no manifest
  of the importing file names the package, something else provides it (a workspace that hoists, a
  `go.work`), and every manifest that declares it counts as used. A Go import belongs to the
  required module with the longest matching path;
- **Go**: `go.mod` names a `tool` of it, or a Go file of the module that depgraph does not scan
  (an excluded directory) imports it. This is what `go mod tidy` decides from;
- **JavaScript / TypeScript**: a file of the repository other than documentation and lockfiles names
  it: a script of `package.json` runs its binary (`tsc` of `typescript`), a configuration lists it
  (also by the short name add-ons go by: `"plugins": ["x"]` for `babel-plugin-x`), a file depgraph
  does not parse imports it (`.vue`, `.svelte`, `.scss`), code gives it as a string (`client: 'pg'`,
  a path into `node_modules`), a CI workflow or a hook runs it. A file names a package for the same
  manifest its imports would use. It is also used when another declared package needs it as a peer,
  and `@types/x` when `x` is used;
- **Python**: a file imports one of its modules (anywhere in the repository: the files share one
  environment), another installed distribution imports it (Django imports the database driver the
  settings choose; a pinned transitive dependency), or a file names it: a module in a string
  (`INSTALLED_APPS = ['storages']`, a dotted path), its name or one of its commands in a file that
  is run (`gunicorn` in a `Procfile`);
- **Ruby**: a file requires it, refers to one of the constants its files declare (Rails code uses
  a gem through its constants), or names it or one of its commands (a `Procfile`, a script, a CI
  workflow).

Not dependencies at all: indirect requirements, `peerDependencies`, and what the repository provides
itself (a module or workspace package of it, a local `replace`, `workspace:` / `file:` / `link:`).

**Severity.** What is left is reported, and the severity says how sure depgraph is:

| | when | severity |
|---|---|---|
| certain | nothing imports it, nothing names it, and nothing can load it on its own | the rule's severity (`error`) |
| not certain | a use that is not an import cannot be ruled out; the finding says why | one level lower (`warn`) |

A finding is not certain when the package is an add-on by its name or its peer (`eslint-plugin-x`,
`@vitest/coverage-v8`, `sass-loader`: its host may load it by name), holds type declarations
(`@types/x` without a visible `x`), is a helper compilers import from what they build (`tslib`,
`core-js`), or is not installed, so that its binaries and the peers of the other packages could not
be read from `node_modules`. A Python distribution is not certain when it registers entry points
another package loads (`pytest-cov`) or holds type stubs (`types-*`). A gem is certain only with
`require: false`: any other gem is required by Bundler when the application boots and may act
without being referenced (it extends other classes, or Rails loads its railtie). Every `go.mod`
finding is certain. With `severity: warn` the certain
findings are `warn` and the others `info`.

A dependency used in a way nothing in the repository shows is left out by name:
`unused-dependency: {ignore: [tslib, "@types/*"]}` in `.depgraph.yml`
([Configuration](configuration.md)).

What files outside the code say (a configuration, `node_modules`) is read when the graph is linked:
after a change of a source file, of a manifest or of what is installed.

## Exempting a function (`depgraph:ignore`)

A function-level finding that is intended is exempted where the function is, with the reason next to it:
a comment containing `depgraph:ignore <rule> [<rule>...]` directly above the function (no blank line in
between) or inside it. Text after the rule names is for the reader.

```go
// Wire builds the application.
//
// depgraph:ignore wide-function (the composition root: it wires every package on purpose)
func Wire(cfg Config) (*App, error) { ... }
```

```python
# depgraph:ignore wide-function, pass-through
def create_app(settings): ...
```

- Rules it applies to: `wide-function` and `pass-through` (the first function of the chain). The other rules
  report packages, files or declarations nothing uses, and are configured in `.depgraph.yml`.
- A comment without a rule name exempts nothing: what is ignored is always written down.
- It is read with the file, in Go, TypeScript / JavaScript and Python.

## Duplicated code

For every function, method and class, depgraph keeps the shape of its body: the syntax tokens with
comments dropped and every identifier and literal masked, so a renamed copy has the same shape. Copies
are grouped when their shapes are identical, or when the winnowed fingerprints of their token 4-grams
(Schleimer et al., *Winnowing*) are at least 80% the same (Jaccard), which catches a copy with a
statement added or changed. Bodies under 50 tokens are ignored, as are fingerprints shared by more
than 10 functions (error handling and loops every function has) and test code (`_test.go`, `*.test.*`,
`*.spec.*`, `test_*.py`, `tests/`, ...), whose table tests look alike on purpose. This is what
compilers and linters do not report: two working functions that do the same thing.
