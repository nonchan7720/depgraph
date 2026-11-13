# Configuration (`.depgraph.yml`)

English | [日本語](ja/configuration.md) · [README](../README.md)

Put `.depgraph.yml` in the scanned root to declare the architecture. Every key is optional;
`scan`, `check`, `watch`, `serve` and `hook` read it, and `watch` reloads it when it changes
(a change of `project` needs a restart).

Editors complete and check the file with its JSON Schema,
[`schema/depgraph.schema.json`](../schema/depgraph.schema.json) (also printed by `depgraph schema`).
With yaml-language-server (VS Code's YAML extension, Neovim, Zed, ...), put this line at the top;
use a release tag instead of `main` to match the depgraph you run:

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/nonchan7720/depgraph/main/schema/depgraph.schema.json
```

The schema is generated from the rule registry and the decoder (`mise run schema`; a test fails when it is
stale), so it accepts exactly what depgraph accepts.

```yaml
project: my-repo            # project key (see Projects)

layers:                     # layer name -> package path globs
  domain: ["pkg/domain/**"]
  services: ["pkg/services/**"]
  infrastructure: ["pkg/infrastructure/**"]
  interfaces: ["pkg/interfaces/**", "cmd/**"]
  frameworks: ["ext:*", "!ext:github.com/google/uuid"]   # third-party packages (see below)

forbidden:                  # layer -> layers it must not import
  domain: [infrastructure, services, interfaces, frameworks]
  services: [interfaces]
  infrastructure: [interfaces, services]

abstract:                   # layer -> layers it may use only through interfaces
  services: [infrastructure]

rules:                      # per rule: enabled, severity (error | warn | info), options
  import-cycle: {severity: error}
  layer-violation: {severity: error}
  god-package: {severity: warn, fanIn: 10, fanOut: 3}
  duplicate-symbol: {enabled: false}
  duplicate-code: {minTokens: 50, similarity: 0.8}   # body size and fingerprint similarity
  wide-function: {maxPackages: 8}                    # most packages a function may call into
  file-cycle: {deferred: true}                       # also the cycles hidden by an import inside a function (Python)
  # duplicate-symbol: {ignore: [main, New, "Test*", "*Handler"]}  # names not checked (replaces the defaults)
  unused-export: {ignore: ["pkg/api/**"]}            # paths not reported (a library's public API)
  unreachable-file: {entries: ["plugins/**"]}       # entry points depgraph cannot see
  # entries / ignore are shared by unused-export, unreachable-file and unreachable-package
  unused-dependency: {ignore: [tslib, "@types/*"]}   # dependencies never reported (name patterns)

ignore: ["**/testdata/**"]  # packages left out of every rule

store:                      # how Neo4j is provided (see Neo4j); flags and env take precedence
  persist: false
  provider: container
  containerCli: docker
```

- **Package globs** match the package key (the directory from the root, `.` for the root) by
  whole path segments: `**` is any number of segments, `*` and `?` stay within one segment.
  `pkg/domain/**` matches `pkg/domain` and everything below it; `**/db/**` matches `db` and
  `internal/db/sql` but not `pkg/feedback`.
- **Name patterns** (`duplicate-symbol`'s and `unused-dependency`'s `ignore`) match the whole symbol / package name: `*` is any run
  of characters, `?` one character, everything else is literal. `New` is that name only,
  `Test*` every name starting with `Test` (a prefix), `*Handler` every name ending in `Handler`.
- A package may be in several layers. An import is a violation only when it crosses the pair:
  it is not reported when both packages are in the forbidden layer, or both in the importing one.
- `layers` replaces the default layers, and with them the default forbidden and abstract pairs;
  `forbidden` or `abstract` alone refers to the default layers.
- `ignore` excludes packages from the findings (cycles through them, fan-in from them, ...);
  unlike `.depgraphignore` they are still scanned and shown in the UI.
- Decoding is strict: an unknown key, rule, option, severity or a `forbidden` entry naming an
  undefined layer is an error that names the file, instead of a setting silently ignored.

Without `.depgraph.yml` the defaults are the layers `controller`, `db`, `domain`, `handler`,
`infra`, `repository` and `usecase`, each matching a path segment of that name (plus
`controllers`, `handlers`, `infrastructure`, `repositories`, `usecases`), with the forbidden
pairs `handler → repository / infra / db`, `controller → repository / db`,
`domain → infra / handler / repository` and `usecase → handler`, and the abstract pairs
`usecase → infra / db`.
This repository's own [`.depgraph.yml`](../.depgraph.yml) is an example.

## Frameworks: external packages in layers

A layer pattern that starts with `ext:` matches packages outside the repository, so `forbidden`
keeps a domain free of frameworks:

| pattern | matches |
|---|---|
| `ext:gorm.io/gorm` | that package and what is under it (`gorm.io/gorm/clause`; in Python imports also its submodules: `ext:sqlalchemy` matches `sqlalchemy.orm`, while `lodash.debounce` is not under `ext:lodash`), the standard library included (`ext:net/http`, `ext:database/sql`) |
| `ext:*`, `ext:github.com/aws/*`, `ext:@nestjs/*` | packages known to be third-party, by a pattern (`*` is any run of characters, `/` included; `?` one character). See below for how an import is known to be third-party |
| `!<pattern>` | leaves packages out of the layer (`"!ext:github.com/google/uuid"`, `"!pkg/domain/legacy/**"`) |

```yaml
layers:
  domain: ["internal/domain/**"]
  frameworks: ["ext:*", "!ext:github.com/google/uuid", "!ext:github.com/shopspring/decimal"]
  http: ["ext:net/http"]
forbidden:
  domain: [frameworks, http]   # internal/domain -> ext:gorm.io/gorm (domain must not import frameworks)
```

A wildcard matches only an import known to be third-party. That is decided from the project's own
files and the resolution, never by a list of names shipped with depgraph, and nothing is executed:

| language | third-party | not third-party |
|---|---|---|
| Go | a module the importing module's `go.mod` (or its `go.work`) requires or replaces | a path whose first element has no dot (the go command's rule for the standard library) |
| TypeScript / JavaScript | resolved into `node_modules`, or declared in a `package.json` of the file or above it when not installed | a Node built-in, as esbuild resolves it (`punycode` is the built-in, `punycode/` the installed package) |
| Python | not the standard library of the version `languages.python` names, by the typeshed copy of the project's type checker (an external import is the standard library or an installed package) | in that version's standard library |
| Ruby | a `require` or a constant (by its top-level name: `ActiveRecord` of `ActiveRecord::Base`) whose name is a gem the `Gemfile` asks for (`DEPENDENCIES` of the bundle's `Gemfile.lock`) | anything else is unknown; `external-unclassified` lists the gems and constants worth naming in an `ext:` pattern ([details](languages/ruby.md#third-party-packages-ext-in-layers)) |

Each import records its class and why (`depgraph parse` shows them). An import a wildcard would match
if it were third-party but that could not be classified is not reported as a violation, and not dropped
either: `external-unclassified` (info) lists them per language with the reason.

**Python: `languages`.** The standard library depends on the Python version, and a repository often pins
it in several places (`.python-version`, `mise.toml`, a Dockerfile, ...), not always the same way. depgraph
does not choose: name the file that pins the version, relative to the file's directory (recommended), or
the version itself. A monorepo whose parts run different versions (an application on 3.12, a library kept
on 3.9 for its users) sets it per component, or in the part's own `.depgraph.yml`; a file uses the setting
of the deepest directory holding it.

```yaml
languages:
  python: {versionFile: .python-version}       # the root
components:
  lib/foo:
    languages:
      python: {versionFile: .python-version}   # lib/foo/.python-version
  "services/*":                                # each service's own file
    languages:
      python: {versionFile: Dockerfile}
```

- Version files: `.python-version`, `.tool-versions`, `mise.toml` (`.mise.toml`, `.config/mise.toml`,
  `mise.local.toml`), `pyproject.toml` (`[tool.pyright] pythonVersion`, `[tool.mypy] python_version`;
  `requires-python` is a range and does not count), `pyrightconfig.json`, `mypy.ini` / `setup.cfg`, a
  Dockerfile with a single `FROM python:<version>...`. Only an exact version is accepted: `latest`,
  `system`, `3`, ranges, several versions, a multi-stage Dockerfile or an `ARG` are errors, not guesses.
- The file may be a symlink (a dotfiles manager), also to a file outside the repository. It is read
  without following it into a FIFO, a device or a pseudo file, up to 64 KiB, parsed strictly, and only its
  relative path is ever shown (never its content).
- A directory between a file and its setting that pins another version (`lib/foo/.python-version` under
  a root setting), or a `mise.<env>.toml` that sets python, leaves that file's imports unclassified with
  the reason: set `languages.python` for that part instead of letting depgraph pick.
- The standard library of the version comes from typeshed's `stdlib/VERSIONS` in the project's own type
  checker, `node_modules/pyright` or `node_modules/basedpyright` from the setting's directory up to the
  root (several copies must agree on a module), or the directory `stdlib:` names. depgraph keeps no list
  of its own.
- Imports are classified when the rules are evaluated, so a change of the setting needs no rescan.
- `depgraph init` writes these settings: for every directory with Python files and a file that pins
  its version, it names that file (the files of one directory that agree: `.python-version` first, a
  Dockerfile last). Where they disagree it asks on a terminal and otherwise leaves the directory out; it
  keeps the rest of `.depgraph.yml` and the settings already there, warns about a version file that is a
  symlink out of the repository (a checkout without its target cannot classify) and about a missing
  typeshed copy. `--dry-run` prints the result.

## Inside the layers: dependency inversion and leaking types

The direction of imports is the Dependency Rule, but a use case may import the package of its
repository (it declares the port there, or uses its types) and still hold the PostgreSQL implementation
instead of the port, or return the ORM row from its API. `abstract:` names, per layer, the layers it may
use only through interfaces; the import itself stays allowed:

```yaml
abstract:
  usecase: [infra, db]          # the default, over the default layers
```

depgraph reads the members of every type and function (Go, TypeScript, Python): the types of fields,
constructor parameters (Go `New*`, `constructor`, `__init__`), parameters and results, and where a type of
another package is built (`&infra.T{}`, `new T()`, `T()`).

- **concrete-dependency**: a struct / class of `usecase` holds or builds an implementation of `infra`
  (a struct or class with behaviour: not an interface, an abstract class, a Protocol / ABC, a dataclass /
  pydantic model / Enum, or a struct whose only methods are `String`, `MarshalJSON`, `Scan`, ...), **and**
  that implementation satisfies an interface declared in the holder's package, the implementation's
  package or the inner layer: a Go struct with every method of the interface, a TypeScript / Python class
  that says it implements / extends it. Without such an interface nothing is reported: that the code
  should have one is a design call, not a fact. Collections, test files and the composition root (a
  layer not in the pair: `cmd/`, `main`) are left out.

  ```text
  internal/usecase depends on the concrete type internal/infra.PostgresOrders
  (internal/usecase/order.go:13 OrderUsecase.repo, and 1 more) although it implements
  internal/usecase.OrderRepository: hold the interface (usecase must depend on infra through an interface)
  ```
- **type-leak**: the API of `usecase` (parameters and results of exported functions and methods,
  exported fields holding data) names a struct or class of `infra`, so callers of the use case depend on
  the infrastructure's types. Interfaces are fine; constructor parameters and held implementations are
  concrete-dependency's.

A component takes `abstract` like `forbidden`, within its directory. Ruby has no types to read and no
interfaces but duck typing, so neither rule applies to it (nor `low-cohesion`, whose parts would be
methods called by dynamic dispatch): depgraph does not guess.

**Upgrading**: both rules are on by default (warn), and so are the abstract pairs `usecase → infra / db`
when `.depgraph.yml` has no `layers:` (none, or only `forbidden:`). A project that matches the default
layers gets new findings in the report, `CLAUDE.md` and the hooks after it upgrades. To keep the earlier
behaviour, write `abstract: {}` or turn the rules off (`rules: {concrete-dependency: {enabled: false}}`).
`low-cohesion` is on too, as info: `--fail-on warn` does not fail on it.

## Monorepos (`components`)

In a monorepo each part has its own architecture, often in another language: `pkg/` in Go,
`lib/auth` in Go and `lib/ui` in TypeScript, an npm package per directory under `packages/`.
Declare each part as a **component**, with layers relative to its directory, in one of two ways:

```yaml
# .depgraph.yml at the root
components:
  "packages/*":             # a pattern: one component per directory (packages/web, packages/kit, ...)
    layers:
      api: ["src/api/**"]
      ui: ["src/components/**"]
    forbidden:
      api: [ui]
  lib/ui:
    forbidden:              # forbidden alone: the default layers, inside lib/ui
      handler: [repository]
```

```yaml
# lib/auth/.depgraph.yml: the same file works with --root lib/auth
layers:
  domain: ["domain/**"]
  adapter: ["adapter/**"]
forbidden:
  domain: [adapter]
ignore: ["**/testdata/**"]                 # lib/auth/**/testdata/**
rules:
  unused-export: {ignore: ["api/**"]}       # lib/auth/api/**
```

- A component's forbidden pairs apply to imports between two packages of the same directory:
  `packages/web/src/api -> packages/web/src/components` is reported as
  `(packages/web: api must not import ui)`, an import from `packages/web` into `packages/kit` is not.
  The language does not matter: the layers are directories.
- The default layers are a guess from directory names, so they are not applied inside a component
  (nor to an import into one). Layers the root declares (`layers:`) are the repository's own
  architecture and apply everywhere, across components too (`apps` must not import each other).
- A `.depgraph.yml` below the root is read with the root's (`scan`, `check`, `watch`, `hook`, `gate`;
  `watch` reloads it when it changes). Its `layers`, `forbidden`, `components`, `ignore` and the
  `ignore` / `entries` of the unused-code rules are relative to its directory. A directory's own file
  takes precedence over a pattern of `components:` that matches it; declaring a directory both in
  `components:` and in its own file is an error. `project` and `store` apply only where it is the
  root, and so do rule switches, severities and thresholds: those are printed as a warning, not applied.
  Files in directories the scan skips (`node_modules`, `.git`, `.depgraphignore`, ...) are not read.
- A component directory is a path below the root or a pattern with `*` and `?` (no `**`).
