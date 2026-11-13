# Python

English | [日本語](ja/python.md) · [README](../../README.md) · other languages: [Go](go.md), [TypeScript / JavaScript](typescript.md), [Ruby](ruby.md)

What depgraph reads in Python code, and what the language-specific rules mean for it. The rules
themselves, their options and `.depgraph.yml` are described in [Rules](../rules.md) and [Configuration](../configuration.md).

## Files

`.py` files.

## Import resolution

Imports resolve the way the import system does, over the package roots the repository implies:

- the repo root and `src/`
- the package directories of `pyproject.toml` / `setup.cfg`
- the parent of the importing file's top-level package
- the parent of every top-level package

A directory without `__init__.py` is a namespace package and does not shadow the standard library.
A relative import that points at nothing is unresolved, not external, so it is no dependency.

Each directory with a `pyproject.toml`, `setup.py` or `setup.cfg` is a project, one of the units of
`module-cycle`.

## Third-party packages (`ext:` in layers)

An import is third-party when it is not in the standard library of the version that `languages.python`
names. The standard library is taken from the typeshed copy of the project's type checker. An external
import is either the standard library or an installed package, and in imports `ext:sqlalchemy` also
matches the submodules (`sqlalchemy.orm`). Which Python version to use is set per directory; see
[Python: `languages`](../configuration.md#frameworks-external-packages-in-layers).

## Unused code

- **Entry points**:
  - `__main__.py`, a `__main__` guard, `conftest.py`, `test_*.py` and `tests/`.
  - Migrations, `setup.py` / `manage.py` / `wsgi.py` / `asgi.py`.
  - The targets of `pyproject.toml` / `setup.cfg` scripts and entry points.
  - In a Django project, its autodiscovered modules (`models`, `admin`, `apps`, `urls`, `views`,
    `wagtail_hooks`, ...).
- **Uses**:
  - `import` / `from ... import`, also inside functions.
  - `module.attr` through an import, and `from pkg import sub; sub.f`.
  - Dotted module names in strings (`"app.tasks.send"`, `"pkg.mod:attr"`), and
    `importlib.import_module(f"app.plugins.{name}")` (every module under `app/plugins`).
  - Modules that commands run (`python -m pkg.tool`, `uvicorn app.main:app`, `celery -A app.worker`).
- **What is reported**:
  - `unused-export`: public (not `_`-prefixed) module-level functions and classes.
  - `unreachable-file`: a file that no entry point reaches.
- **Never reported**:
  - `__init__.py` (never unreachable).
  - Names in `__all__`, and decorated definitions (routes, fixtures, tasks that a framework registers).
  - Dunder names.
  - An export that only its own file uses.

## Other rules

- `concrete-dependency` and `type-leak` read class attributes, `__init__` parameters, parameters and
  results (annotations) and `T()`. An implementation satisfies an interface when its class extends it.
  Protocols, ABCs, dataclasses, pydantic models and Enums are not implementations. See
  [Inside the layers](../configuration.md#inside-the-layers-dependency-inversion-and-leaking-types).
- `low-cohesion` splits a module by its top-level declarations.
- `file-cycle` reads the `import` and `from ... import` statements of a module, also those inside functions, but not
  those under `if TYPE_CHECKING:`. A cycle that only closes through an import inside a function is reported as hidden by
  that file (a deferred import hides a cycle, it does not remove it); `rules: {file-cycle: {deferred: false}}` leaves
  those out. `from pkg import name`
  loads the submodule `pkg/name.py` when there is one; else the module `name` comes from: `pkg/__init__.py` when it
  declares the name, or the module `pkg/__init__.py` imports it from (a module that takes a name its own package only
  passes on depends on the module that declares it). A module that imports its own package as a module (`import pkg`,
  `from .. import pkg`) adds nothing: the package is loaded before its modules anyway. Only cycles inside one directory are
  reported; across directories it is an `import-cycle`.

## Language server

During `watch`, `pyright-langserver` adds calls and references between symbols for the changed files
(optional).
