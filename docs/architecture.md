# Architecture

English | [日本語](ja/architecture.md) · [README](../README.md)

How depgraph is built: how each feature works, what is stored in Neo4j, which Neo4j features
the rules use, and why the store is Neo4j and not an embedded database such as SQLite.

- [The pipeline](#the-pipeline)
- [Reading the code](#reading-the-code)
- [Storing the graph](#storing-the-graph)
- [How the rules are evaluated](#how-the-rules-are-evaluated)
- [Delivering the findings](#delivering-the-findings)
- [Graph schema](#graph-schema)
- [Import resolution](#import-resolution)
- [What Neo4j does, and the Neo4j features used](#what-neo4j-does-and-the-neo4j-features-used)
- [Why Neo4j](#why-neo4j)
- [Why not SQLite](#why-not-sqlite)
- [What Neo4j costs](#what-neo4j-costs)
- [Code layout](#code-layout)
- [Querying by hand](#querying-by-hand)

## The pipeline

Every command is a part of one pipeline:

```
 source files        resolver             graph store        rules              findings
 Go, TS / JS,   ──►  tree-sitter     ──►  Neo4j         ──►  Cypher        ──►  CLAUDE.md section
 Python, Ruby        + import             (one graph                            hooks / gate  (the agent)
                     resolution           per project)                          browser UI / MCP App
                                                                                exit code     (CI)
        ▲                                      ▲
        └── watch: fsnotify, only the ─────────┘
            changed files are re-read
```

| command | reads the code | writes the graph | evaluates the rules | delivers |
|---|---|---|---|---|
| `scan` | every file | yes | yes | stderr, `CLAUDE.md`, exit code |
| `watch` | every file, then the changed ones | yes | after every change | stderr, `CLAUDE.md`, browser UI |
| `check` | no | no | yes | stderr, `CLAUDE.md`, exit code |
| `serve` / `mcp` | no | no | yes | browser UI / MCP App |
| `hook` | no | session state only | yes | context for the agent |
| `gate` | the changed files | yes | yes | exit code for the agent |
| `parse` | the given files | no (no Neo4j at all) | no | JSON |

The use case behind `scan`, `watch` and `gate` is `pkg/services/analysis`: `FullScan`,
`HandleChanges`, `Evaluate`. It knows the store and the resolver only as interfaces
(`graph.Repository`, `rules.Repository`, `graph.Resolver`); `pkg/interfaces/cmd` builds the
implementations.

## Reading the code

`pkg/infrastructure/resolver` turns one source file into a `graph.FileGraph`. Nothing here needs
Neo4j (`depgraph parse` prints the result as JSON).

1. **Walk.** The repository is walked once. Built-in directory names (`node_modules`, `vendor`,
   `.git`, ...) and the patterns of `.depgraphignore` are skipped.
2. **Parse.** Each file is parsed with tree-sitter
   ([gotreesitter](https://github.com/odvcencio/gotreesitter), pure Go: no cgo, so the binary
   cross-compiles with plain `go build`). A full scan parses files on a worker pool, but hands the
   results on in path order, so the output does not depend on the number of workers.
3. **Extract.** From the syntax tree of the file, and only that file:

   | what | used by |
   |---|---|
   | the package (the directory) and the top-level declarations (`Symbol`) | everything |
   | the imports | cycles, layers, god-package |
   | the *shape* of every function / method / class body: tokens with identifiers and literals masked, as a hash and as winnowed fingerprints | `duplicate-code` |
   | the names the file uses from other files, what it exports, why it is an entry point | `unused-export`, `unreachable-*`, `low-cohesion` |
   | the files its imports load (and whether at load time or inside a function) | `file-cycle` |
   | the functions each function calls | `function-cycle`, `pass-through`, `wide-function` |
   | the types of fields, parameters and results, and where types are built | `concrete-dependency`, `type-leak` |

4. **Resolve imports.** Each import is resolved the way its ecosystem resolves it, from the
   project's own configuration (see [Import resolution](#import-resolution)). The result is a
   package of the repository, a package outside it (`ext:<name>`), or *unresolved*. An unresolved
   import is dropped: the project's compiler reports it, depgraph does not.
5. **Refine (watch only).** After a change has been stored and evaluated, language servers are
   asked in the background for the calls and references of the changed files
   (`callHierarchy`, `textDocument/references`), which are stored as `CALLS` / `REFERENCES` edges
   between symbols. They are counted in the statistics; no rule depends on them (a full scan
   never has them). The rules use the call graph read from the syntax instead.

depgraph reads what the code says and does not guess: a call through an interface with several
implementations is not followed, an import that cannot be classified is listed as unclassified
instead of being reported. A finding that is not true is treated as a bug.

## Storing the graph

`pkg/infrastructure/repository/graph` writes `FileGraph`s to Neo4j. **The unit of every update
is one file**:

1. In one transaction, the file's node is merged, its old symbols and import edges are deleted
   and the new ones are created (`UpsertFile`).
2. The package-level `IMPORTS` edges are derived from the file-level ones, for the packages that
   were touched (`RebuildPackageImports`).
3. Packages without files and external packages nothing imports any more are pruned.

That is what makes `watch` incremental: fsnotify events are debounced (400 ms), only the changed
files are parsed again and replaced, and the rules run on the result. Two cases fall back to a
full scan: a file import resolution depends on changed (`go.mod`, `tsconfig.json`,
`package.json`, ...), and the first scan of a run, which also removes files that were deleted
while depgraph was not running.

**Per-file facts, then a linked graph.** What a file uses from other files (`uses`, `calls`,
`loads`, `typeUses`, ...) is first stored as list properties on its own node, as written in the
file. An edge from this file to a symbol of another file would be deleted whenever the *other*
file is re-written, and restoring it would mean re-parsing every importer. With per-file
properties a file is replaced without touching any other.

Before the rules run, those names are resolved across the project and stored as nodes and
relationships: the **linked graph** ([schema](#graph-schema)). Linking only resolves names (which
function a call reaches, which file an import loads, which type a field has); it decides nothing
a rule reports. It is redone when the files or the settings changed, and writes only the
difference to what is stored, so a file created later is seen without re-parsing the files that
already imported it.

Some facts are not in the source files: the dependencies a manifest declares, and whether a
configuration, a script or an installed package says one of them is used. They are read from the
project's files when the graph is linked and stored as `Dependency` nodes, so the rule is still a
query (`unused-dependency`).

One database holds many repositories: every node carries the project key, and every query is
scoped by it (see [Projects](neo4j.md#projects)).

## How the rules are evaluated

`rules.Repository.Evaluate` (`pkg/infrastructure/repository/rules`) brings the linked graph up
to date and then runs every enabled rule. **Every rule is a Cypher query over the graph**; Go
only resolves names when linking and turns the rows of a query into the text of a finding.

| rule | the query |
|---|---|
| `import-cycle` | for every import between packages, the shortest way back (`allShortestPaths`, at most 8 packages) |
| `module-cycle` | the same over `(Module)-[:MODULE_IMPORTS]->(Module)`, without the cycles `import-cycle` reports |
| `file-cycle` | the same over `(File)-[:LOADS]->(File)`: first over the eager loads, then the cycles that only close through an import inside a function |
| `function-cycle` | the same over `(Function)-[:CALLS]->(Function)` for calls between two files of a Go package, without the cycles among the methods of one type |
| `pass-through` | paths of two or more `FORWARDS` from a function nothing forwards to, to the first that does something else |
| `wide-function` | the distinct packages of the functions a function `CALLS` (constructors apart) and of the types it calls through (`CALLS_INTO`) |
| `layer-violation` | every forbidden pair at once: package imports matched against the layers' regular expressions; external imports by their class |
| `external-unclassified` | the external imports a wildcard layer would match if they were third-party, without a class |
| `concrete-dependency` | the uses (`(Use)-[:OF]->(Type)`) of an implementation across an abstract pair, and the smallest interface the type implements that covers the calls |
| `type-leak` | the exported uses of a struct / class across an abstract pair |
| `duplicate-code` | the groups `(Symbol)-[:SIMILAR]->(Symbol)` connects; SIMILAR itself is written by Cypher (same masked hash, or a shared rare fingerprint and a Jaccard similarity of all fingerprints) |
| `duplicate-symbol` | exported names declared in several files, by aggregation |
| `god-package` | fan-in and fan-out, by aggregation |
| `low-cohesion` | the connected groups of `(Part)-[:LINKS]-(Part)` of a unit, each used from outside, with no user in two groups |
| `unreachable-file`, `unreachable-package` | one walk from the entry points marks what is reached; what is not is grouped under the file / package nothing imports |
| `unused-export` | exported declarations without a `USES_NAMES` / `USES_GO` naming them, outside entry points and dead files |
| `unused-dependency` | the `Dependency` nodes without a use that no file imports: no `(Dependency)-[:PROVIDES {scopes}]->(Package)<-[:IMPORTS]-(File)` with a file of one of the scopes |

Layers and `ignore` are globs in `.depgraph.yml`. They are translated once into regular
expressions that mean the same in Go (RE2) and in Cypher (`=~`, Java), so a pattern matches the
same packages in a Go unit test and in the query.

A cycle is always reported the same way: of several shortest cycles through an import, the
smallest by its keys is taken. `shortestPath` alone returns any of them, which made the same
graph report different cycles in two runs.

## Delivering the findings

A finding has a rule, a severity, a message and a stable identity, so two evaluations can be
compared: what was added, what was resolved.

| where | how |
|---|---|
| `CLAUDE.md` | `scan`, `watch` and `check` rewrite the section between `<!-- depgraph:start -->` and `<!-- depgraph:end -->`; hand-written content is kept |
| hooks (`depgraph hook`) | bounded (3 s) and never scanning: evaluates the rules on the stored graph and prints a summary at session start, then only the difference since the last prompt of that session. The last findings of each session are kept in Neo4j (`HookSession`), never in the repository |
| gate (`depgraph gate`) | applies the changed files to the graph itself (so it does not race `watch`), evaluates, and fails on the findings the session added since its baseline. A git worktree is its own project, built from the worktree's base commit under a lease |
| browser UI (`watch`, `serve`) | an HTTP server with the UI embedded in the binary; `watch` pushes every evaluation over SSE |
| MCP App (`depgraph mcp`) | the same UI as one self-contained HTML, read through MCP tools |
| CI | `scan` / `check --fail-on <severity>` exit 1 |

Details: [Coding agents](agents.md), [Browser UI and MCP App](ui.md), [Commands](commands.md).

## Graph schema

What a scan writes, file by file:

```
(Project     {key, name, root, origin, base, worktree, lastScanAt, commit, dirty, baseCommit, syncUntil})
(Package     {project, key, name, external})
(File        {project, path, language, lines, updatedAt,
              scope, entry, internal, generated, uses, exports, keep, mentions,  # what the file uses and exports
              loads, calls, ownMembers, typeUses, constructs,
              decls, idents, declRefs, extConstants})
(Symbol      {project, file, name, kind, exported, line, endLine, language,
              tokens, cloneHash, fingerprints,                                   # the shape of its body
              receiver, methods, abstract, data, implements, embeds})            # types and their members
(HookSession {project, id, findings, baseline, updatedAt})

(Package)-[:CONTAINS]->(File)-[:DECLARES]->(Symbol)
(File)-[:IMPORTS {spec, class, basis}]->(Package)
(Package)-[:IMPORTS]->(Package)          # derived from the file-level imports
(Symbol)-[:CALLS|REFERENCES]->(Symbol)   # added by LSP for changed files during watch
```

The linked graph, resolved from those facts before the rules run:

```
(Function {key, file, name, dir, lang, line, recv, ignore})        # a function that calls or is called
(Function)-[:CALLS {via, construct}]->(Function)                   # via: through a member's declared type
(Function)-[:FORWARDS]->(Function)                                 # only passes its parameters on
(Function)-[:CALLS_INTO]->(Package)                                # the package of a member's type
(File)-[:LOADS {eager}]->(File)                                    # in one directory; eager: at load time
(Module {key})-[:MODULE_IMPORTS {from, to}]->(Module)                   # modules of a monorepo
(Type {key, pkg, name, kind, abstract, data, embeds, methods})
(Type)-[:IMPLEMENTS {exact}]->(Type)
(Use {file, pkg, owner, member, role, line, exported, collection, construct, calls})-[:OF]->(Type)
(Symbol {cloneGroup})-[:SIMILAR {score}]->(Symbol)                 # copies
(Part {unit, what, id, data, used})-[:LINKS]->(Part)               # the parts of a package / module
(ExtImport {pkg, file, ext, lang, python, class, basis})           # external imports of Python / Ruby files
(Dependency {manifest, dir, eco, name, section, line, languages,   # a dependency a manifest declares
             checked, use, doubt})-[:PROVIDES {scopes}]->(Package) # and the external packages it provides (to the files of scopes)
(Root {key: 'unused'})-[:ENTRY {why}]->(File)                      # entry points
(File)-[:USES_FILE]->(File), (File)-[:GO_PKG]->(File)              # what a file reaches
(File)-[:USES_NAMES {names}]->(File), (File)-[:USES_GO {names}]->(Package)
(Root)-[:USES_ALL]->(File), (Root)-[:USES_ALL_GO]->(Package), (Package)-[:USES_GO_PKG]->(Package)
File.reached, File.dead, File.deadRoot, Package.dead, Package.deadRoot   # marks of the unused-code rules
(LinkState {project, key})                                         # what the linked graph was made from
```

- `project` is the project key. `Package` is unique by `(project, key)`, `File` by
  `(project, path)`, `HookSession` by `(project, id)`, `Project` by `key`; the nodes of the
  linked graph by `(project, key)`.
- `Package.key` is the directory path from the repository root (the root is `.`). An import
  outside the repository becomes a `Package` with the key `ext:<package>` and `external=true`
  (per project too; `elkjs/lib/elk-api` is the package `ext:elkjs`). An import that resolves to
  nothing gets no `Package` and no edge.
- `class` and `basis` on a file's `IMPORTS` edge say whether the import is third-party or the
  standard library, and why (see
  [Frameworks](configuration.md#frameworks-external-packages-in-layers)).
- The usage properties of a `File` are what the file itself says: the names it uses from which
  file or package (`uses`), the names it exports under another name (`exports`), the names it
  keeps on purpose (`keep`: a Python `__all__` entry, a decorated definition, an STI subclass),
  whether it is generated or vendored, and why it is an entry point (`entry`). They are replaced
  with the file.
- `Project.syncUntil` is the lease a background build of a worktree's project holds.

## Import resolution

Imports are resolved the way each ecosystem resolves them, from the project's own configuration:

| language | how | details |
|---|---|---|
| TypeScript / JavaScript | esbuild's resolver: `tsconfig.json` / `jsconfig.json`, `package.json` (`exports`, `imports`, workspaces, also before an install), `node_modules` | [TypeScript / JavaScript](languages/typescript.md#import-resolution) |
| Go | `go.mod` / `go.work` (nested modules, `use`, `replace`) | [Go](languages/go.md#import-resolution) |
| Python | the import system over the repository's package roots (the repo root, `src/`, `pyproject.toml` / `setup.cfg` package dirs) | [Python](languages/python.md#import-resolution) |
| Ruby | `require` / `require_relative` over the load path the repository implies, and constants the way Zeitwerk loads them (applications, engines, packwerk packs) | [Ruby](languages/ruby.md#import-resolution) |

A relative path, an alias or a module of the project that points at nothing is unresolved, not
external, so it never shows up as a dependency. `watch` rescans when one of these configuration
files changes.

## What Neo4j does, and the Neo4j features used

Neo4j is the only store. It holds the graph of every project, the project registry and the
hooks' session state, and it answers three kinds of question: the rules, the views of the UI
(`Overview`, `PackageDetail`, `Stats`), and whatever you ask by hand.

depgraph uses plain Cypher over the Bolt driver (`neo4j-go-driver`). **No APOC or Graph Data
Science procedure is called**, so any Neo4j 5 works: the Community image depgraph starts, a
server, or Aura.

| Neo4j feature | where depgraph uses it |
|---|---|
| Labelled property graph (nodes, typed relationships, properties, list properties) | the [schema](#graph-schema) above |
| Uniqueness constraints on several properties (`REQUIRE (p.project, p.key) IS UNIQUE`) and range indexes | one database for many projects; two processes that scan at once cannot create a package twice |
| `MERGE ... ON CREATE SET` | idempotent upserts of packages, files, projects and sessions |
| `SET f += $map` (a `null` value removes the property) | replacing all usage properties of a file in one statement |
| `UNWIND $list` | writing all symbols / imports of a file, and evaluating all layer pairs, in one statement |
| `DETACH DELETE` | removing a file with its symbols and every edge at once |
| Managed transactions with retry (`ExecuteWrite`) | a file is replaced atomically; a reader never sees it half-written |
| `allShortestPaths((b)-[:IMPORTS*1..7]->(a))` with predicates on the nodes and relationships of the path | the four cycle rules |
| Variable-length patterns with `DISTINCT` end nodes (the planner walks each node once) | what the entry points reach, the groups of copies, the parts of a unit |
| `CALL { ... UNION ... }`, `COLLECT { ... ORDER BY ... }` and `COUNT { ... }` subqueries, `EXISTS { ... }` | `wide-function`, sorted lists inside a row, `unused-export` |
| Path and list functions (`nodes(p)`, list comprehension, slicing, `reduce`, `range`) | rotating a cycle to start at its smallest key, so the same cycle is found once |
| Predicates over lists (`any` / `none` / `all`) with regular expressions (`=~`) | layers, `ignore` and name patterns, matched inside the query |
| Aggregation (`count(DISTINCT ...)`, `collect`) and `OPTIONAL MATCH` | fan-in / fan-out, same-named declarations, reading a file with its symbols |
| Pattern comprehension | statistics and the UI's overview in one round trip |
| `UNION` | in-repo and external layer violations in one query |
| Temporal values (`datetime()`) | `updatedAt`, pruning old sessions, the lease `syncUntil` |
| Conditional write (`MERGE ... WITH p WHERE p.syncUntil < $now SET ...`) | a lease that only one process can take |

### Example: `import-cycle`

The whole rule is this query (`pkg/infrastructure/repository/rules/implement.go`):

```cypher
// every import a -> b between packages of the repository that are not ignored
MATCH (a:Package {project: $project})-[:IMPORTS]->(b:Package {project: $project})
WHERE a.external = false AND b.external = false AND a <> b
  AND none(rx IN $ignore WHERE a.key =~ rx OR b.key =~ rx)
// the shortest way back from b to a closes the shortest cycle through that import
MATCH p = allShortestPaths((b)-[:IMPORTS*1..7]->(a))
WHERE all(x IN nodes(p) WHERE x.external = false AND none(rx IN $ignore WHERE x.key =~ rx))
// of several shortest ways back, always the same one: the smallest
WITH a, b, [n IN nodes(p) | n.key] AS back ORDER BY back
WITH a, b, head(collect(back)) AS back
// the cycle as a list of keys, rotated to start at its smallest key, deduplicated
WITH [a.key] + back AS cyc
WITH cyc[0..size(cyc)-1] AS ring
WITH ring, reduce(m = 0, i IN range(0, size(ring) - 1) | CASE WHEN ring[i] < ring[m] THEN i ELSE m END) AS s
WITH ring[s..] + ring[..s] AS r
WITH DISTINCT r + [r[0]] AS cyc
RETURN cyc ORDER BY size(cyc), cyc LIMIT 50
```

The first version enumerated every cycle (`(a)-[:IMPORTS*2..8]->(a)`). That is exponential: 17 s
on a 700-file repository, far beyond the 3 s the hook has. Asking for one shortest path per
import is polynomial (0.17 s on a dense graph of 40 packages and 280 imports) and still reports
every import that lies on a cycle of at most 8 packages. `allShortestPaths` is used rather than
GQL's `SHORTEST`, which the Neo4j 5.26 planner ran about 500 times slower on this query.

### Example: `layer-violation`

All forbidden pairs are evaluated in one query. Each pair carries the regular expressions of its
two layers:

```cypher
UNWIND $pairs AS pr
MATCH (a:Package {project: $project})-[:IMPORTS]->(b:Package)
WHERE a.external = false AND b.external = false
  AND NOT any(rx IN $ignore WHERE a.key =~ rx OR b.key =~ rx)
  AND any(rx IN pr.fromSet.rx WHERE a.key =~ rx)       // a is in the importing layer
  AND any(rx IN pr.toSet.rx   WHERE b.key =~ rx)       // b is in the forbidden layer
  AND NOT any(rx IN pr.toSet.rx   WHERE a.key =~ rx)   // and the import crosses the pair
  AND NOT any(rx IN pr.fromSet.rx WHERE b.key =~ rx)
RETURN DISTINCT a.key AS from, b.key AS to, pr.from AS fromLayer, pr.to AS toLayer
```

(Shortened: the real query also handles exclusion patterns, components and layers that name
external packages.)

### Example: `pass-through`

A chain of functions that only pass their parameters on is a path of `FORWARDS`:

```cypher
MATCH (head:Function {project: $project})-[:FORWARDS]->(:Function)
WHERE NOT (:Function)-[:FORWARDS]->(head)          // nothing forwards to the first
MATCH p = (head)-[:FORWARDS*2..]->(last:Function)  // two or more forwarders
WHERE NOT (last)-[:FORWARDS]->(:Function)          // the last one does something else
RETURN [n IN nodes(p) | [n.file, n.name]] AS chain
ORDER BY head.file, head.name LIMIT 50
```

## Why Neo4j

Neo4j has been the store since the first commit. The principle behind the choice is that **a
rule is a Cypher query**. The reasons, in the order they matter:

1. **The data is a graph and the rules are path questions.** "Is this import on a
   cycle, and which is the shortest one?", "does a package of this layer reach a package of that
   layer?", "who depends on this package?" are patterns and paths. In Cypher the query has the
   shape of the question, and the shortest-path search is the database's, not code to maintain.
2. **The graph can be explored, not only reported.** The product is not just a list of
   findings: when a finding is surprising, you open Neo4j Browser and ask the graph why
   ([Querying by hand](#querying-by-hand)). MCP tools that let an agent query the graph
   directly are on the [roadmap](development.md#roadmap).
3. **It is a server that several processes share.** While an agent works, `watch` writes the
   graph, the hook reads it on every prompt, the gate applies files and reads it after every
   edit, a background process builds the graph of a worktree, and `serve` / `mcp` show it. They
   are separate processes, possibly started from different directories, and they find the same
   store by the labels of its container. Constraints and transactions make the concurrent
   writers safe; nothing has to be written into the repository.
4. **Per-file replacement is cheap to express.** `MERGE`, `DETACH DELETE` and `UNWIND` replace a
   file's subgraph in one transaction without bookkeeping of foreign keys.

## Why not SQLite

SQLite was ruled out for one reason: **the rules are meant to be written in Cypher.** A rule is
a question about a graph (a path, a pattern, a neighbourhood), and depgraph wants that question
written in a graph query language, where it can be read, changed and tried by hand against the
stored graph. SQLite has no such language: every rule would be recursive SQL or application
code. **SQLite could store this data**; what it cannot do is run the rules as they are meant to
be written. The table shows what each choice means in practice (this repository has no SQLite
implementation to benchmark against, so it is a comparison of designs).

| | Neo4j (today) | SQLite |
|---|---|---|
| Shortest cycle through every import, with filters on the nodes of the path | one `shortestPath` clause | a recursive CTE. Enumerating paths in it is the exponential search the first version of `import-cycle` had; a polynomial one needs the breadth-first search and the reconstruction of each path written by hand in SQL, or the search moved to Go |
| Patterns over edges (`(layer a)-[:IMPORTS]->(layer b)`, fan-in) | a pattern | joins over an edge table: possible and fast |
| Glob patterns matched in the query | `=~` built in | `REGEXP` is not built in: a function registered by the application |
| List properties (`fingerprints`, `uses`, `calls`) | native lists | JSON columns or child tables |
| Replace one file | `DETACH DELETE` + `MERGE` | `DELETE` with cascading foreign keys: possible |
| Several processes at once (`watch`, hook, gate, `serve`) | a server: concurrent readers and writers | one writer at a time; works in WAL mode on a local disk, with retries |
| Where the data lives | a container found by its labels; nothing in the repository | a file: a path every process must agree on, outside the repository, cleaned up by depgraph |
| Looking at the graph | Neo4j Browser, Cypher | SQL over node and edge tables |
| Build | the Bolt driver is pure Go | a cgo-free driver exists, so `CGO_ENABLED=0` would hold, but it is a large dependency to take on |
| What the user needs | a container runtime, a 1 GB image, 10-20 s to start, 330-480 MiB of memory | nothing |

So the trade is: SQLite would remove the container runtime and the start-up time, which is the
largest cost depgraph has ([next section](#what-neo4j-costs)). In exchange every rule, which is
one Cypher query today, would become recursive SQL or application code, and the graph would no
longer be something a person or an agent can explore with a graph query language.

The decision is not locked in by the code: the store is behind
interfaces (`graph.Repository`, `rules.Repository`, `view.Reader`, `project.Repository`, the
hook's `SessionStore`), and the domain logic does not import the driver (`.depgraph.yml` of this
repository forbids it). Another store would be another implementation of those interfaces,
verified against the golden output of the current one.

## What Neo4j costs

The choice has a price, and several pull requests were about paying it:

| cost | what was done |
|---|---|
| A Neo4j had to be installed or started with docker compose before the first scan | depgraph starts Neo4j itself in a container (docker, podman, nerdctl), ephemeral by default, and a watchdog process removes it however depgraph ends ([Neo4j store](neo4j.md)) |
| A container runtime is still required; the image is about 1 GB and starts in 10-20 s | memory is capped (heap 512 MiB, page cache 64 MiB); a `local` provider that runs an installed Neo4j as a child process is on the [roadmap](development.md#roadmap) |
| Enumerating cycles was exponential | the shortest paths of one import at a time ([above](#example-import-cycle)) |
| `shortestPath` returns any of several shortest paths, so a cycle changed between runs | the smallest of `allShortestPaths` |
| Deleting a large project in one transaction exceeded the transaction memory (a 14,000-file repository) | deletes run in batches of 10,000 nodes |
| `IF NOT EXISTS` does not protect two sessions that create the same constraint at once | `EnsureSchema` treats "already exists" as success |
| `collect()` has no order, so a message changed between runs | lists are sorted in Go before a finding is built |

## Code layout

The code follows the layers its own [`.depgraph.yml`](../.depgraph.yml) declares, and depgraph
checks them on itself: `domain` imports nothing of the other layers and no third-party package,
`services` uses `infrastructure` only through interfaces.

```
main.go                                  entry point (just calls cmd.Execute())

pkg/domain/                              no I/O, no third-party imports
  graph/                                 language-agnostic graph model, Repository / Resolver interfaces
  rules/                                 the rules' registry, Config (layers, globs), Finding and diff, Repository interface
  project/                               Project, key rules, move-or-conflict decision, Repository interface
  view/                                  what the UI reads (overview, package detail, stats)

pkg/services/analysis/                   the use case: scan, apply changes, evaluate, LSP refinement

pkg/infrastructure/
  resolver/                              repository walk + exclude patterns
    treesitter/                          structural analysis per file (gotreesitter)
    imports/                             import resolution per ecosystem (esbuild, go.mod / go.work, Python, Ruby)
    deps/                                the dependencies the manifests declare (go.mod, package.json) and what else uses them
    lsp/                                 refinement with language servers (callHierarchy / references)
  repository/graph/                      Neo4j: writes, schema, UI views
  repository/rules/                      Neo4j: linking and the rules (Cypher)
  repository/project/                    Neo4j: Project nodes, leases
  repository/hooksession/                Neo4j: the hooks' session state
  neo4jprovider/                         how Neo4j is provided: Provider contract, selection, external
    container/                           Neo4j in a docker / podman / nerdctl container (lifecycle, labels, watchdog)
  config/                                .depgraph.yml (strict decoding, nested files of a monorepo, JSON Schema)
  workspace/                             project identity of a root (.depgraph.yml, git origin / worktree)
  watch/                                 fsnotify + debounce
  claudemd/                              the managed section of CLAUDE.md
  codexhooks/                            copies the Codex plugin hooks into a Codex hooks.json

pkg/interfaces/
  cmd/                                   CLI (cobra) and the composition root
  handler/ui/                            HTTP server + SSE + embedded UI
  handler/mcpapp/                        MCP server: the UI as an MCP App
  handler/hook/                          hook and quality gate (summary, diff, findings a session added)

web/                                     browser UI source (Vite + TypeScript, Cytoscape + elkjs), embedded into the binary
plugin/                                  Claude Code and Codex plugin (hooks, skills)
```

The analysis backend is pluggable through `graph.Resolver`: tree-sitter today, refined by LSP
during `watch`; a SCIP index import is on the roadmap.

## Querying by hand

With an [external Neo4j](neo4j.md#external-your-own-neo4j-docker-compose-a-server-aura) (the
`docker-compose.yml` of this repository serves Neo4j Browser on http://localhost:7474), the
graph can be queried directly. Scope every query by the project key:

```cypher
// import cycles
MATCH p=(a:Package {project: 'my-repo'})-[:IMPORTS*2..8]->(a) WHERE a.external=false
RETURN [n IN nodes(p) | n.key] AS cycle LIMIT 20;

// highest fan-in
MATCH (p:Package {project: 'my-repo'})<-[:IMPORTS]-(q) WHERE p.external=false
RETURN p.key, count(q) AS fanIn ORDER BY fanIn DESC LIMIT 10;

// who imports a package, and from which files
MATCH (a:Package {project: 'my-repo'})-[:CONTAINS]->(f:File)-[:IMPORTS]->(b:Package {key: 'pkg/domain/graph'})
RETURN a.key, collect(f.path) AS files ORDER BY a.key;
```
