# Go

English | [日本語](ja/go.md) · [README](../../README.md) · other languages: [TypeScript / JavaScript](typescript.md), [Python](python.md), [Ruby](ruby.md)

What depgraph reads in Go code, and what the language-specific rules mean for it. The rules
themselves, their options and `.depgraph.yml` are described in the [README](../../README.md#rules).

## Files

`.go` files. Test files (`_test.go`) are parsed too: they are entry points and use what they name.

## Import resolution

Imports resolve with `go.mod` / `go.work`, including nested modules, `use` and `replace`. An import
that points into the repository but at no package is unresolved, not external, so it is no dependency
(the compiler reports it).

Each directory with a `go.mod` is a module, one of the units of `module-cycle`.

A package is a directory, so the files of an external test package (`package x_test`) count as files of the
directory's package. For `import-cycle` and `module-cycle` an import only `_test.go` files make is left out: Go
compiles a test file with the tests of its own package only, and an external test package may import what imports
the package under test.

## Third-party packages (`ext:` in layers)

A module the importing module's `go.mod` (or its `go.work`) requires or replaces is third-party. An
import path whose first element has no dot is the standard library (the go command's rule). See
[Frameworks](../../README.md#frameworks-external-packages-in-layers).

## Unused code

- **Entry points**:
  - `main` packages and `_test.go` files.
  - A module without a `main` package is a library, and its packages outside `internal/` are its public API.
- **Uses**:
  - A package is used as a whole by its importers; a name is used through `pkg.Name`.
  - Files a `//go:embed` embeds are used, and so are source file names in strings (`"render.js"` in a
    Go program that bundles it).
- **What is reported**:
  - `unused-export`: exported functions and types of non-main packages.
  - `unreachable-package`: a package that no `main` package, test or public package of a library module imports.
  - Go files are never reported by `unreachable-file`: a package is reached as a whole.
- **Never reported**:
  - Exported methods (an interface may need them; interface satisfaction is invisible to a syntactic analysis).
  - A package that only holds documentation (`doc.go`).
  - An export that only its own file uses.

## Other rules

- `duplicate-symbol`: the files of one package count once (they can share a name only through build
  constraints).
- `concrete-dependency` and `type-leak` read struct fields, `New*` constructor parameters, parameters
  and results, and composite literals (`&infra.T{}`). An implementation satisfies an interface when it
  has every method of it. See [Inside the layers](../../README.md#inside-the-layers-dependency-inversion-and-leaking-types).
- `low-cohesion` splits a package by its files.

## Language server

During `watch`, `gopls` adds calls and references between symbols for the changed files (optional).
