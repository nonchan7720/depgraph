// Package imports resolves import specs to the package they point at, using
// each ecosystem's own resolution rules and project configuration:
//
//   - TypeScript / JavaScript: esbuild's resolver (tsconfig paths / baseUrl /
//     extends, package.json exports / imports, node_modules, workspaces)
//   - Go: go.mod / go.work (nested modules, use, replace)
//   - Ruby: require / require_relative over the load path the repository
//     implies, and constants by Zeitwerk's convention (Rails, engines,
//     packwerk packs)
//   - Python: the import system over the package roots of the repository
//     (repo root, src/, pyproject.toml / setup.cfg package dirs, parents of
//     top-level packages)
//
// Results distinguish an in-repo package, an external package and an import
// that could not be resolved at all. Project configuration is cached per root;
// call Invalidate when a file for which IsConfigFile reports true changes.
package imports

import (
	"context"
	"path"
	"path/filepath"
	"strings"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
)

// Kind is what an import resolved to.
type Kind int

const (
	// Unresolved: the import should point into the repository (a relative
	// path, an alias, a known module) but no file / package was found.
	Unresolved Kind = iota
	// InRepo: a package inside the repository; Target is its package key.
	InRepo
	// External: a package outside the repository (standard library,
	// third-party); Target is "ext:<name>".
	External
)

func (k Kind) String() string {
	switch k {
	case InRepo:
		return "in-repo"
	case External:
		return "external"
	default:
		return "unresolved"
	}
}

// ExternalPrefix prefixes the Target of external packages.
const ExternalPrefix = "ext:"

// Result is the resolution of one import spec.
type Result struct {
	Kind Kind
	// Target is the package key (graph.PackageKey form, "." for the root) for
	// InRepo, "ext:<name>" for External and empty for Unresolved.
	Target string
	// File is the repo-relative, slash-separated file or directory the spec
	// resolved to, when known (InRepo only).
	File string
	// Reason explains an Unresolved result.
	Reason string
	// Class says what an External package is, from the project's own files
	// and the resolution only (nothing is executed, no version lists):
	// ClassThirdParty, ClassStandard or "" when that is not known.
	Class string
	// Basis says why (what declared or resolved it).
	Basis string
}

// Classes of external packages (graph.Import.Class).
const (
	ClassThirdParty = graph.ClassThirdParty
	ClassStandard   = graph.ClassStandard
)

func (r Result) classed(class, basis string) Result {
	r.Class, r.Basis = class, basis
	return r
}

func inRepoFile(rel string) Result {
	return Result{Kind: InRepo, Target: graph.PackageKey(rel), File: rel}
}

func inRepoDir(rel string) Result {
	if rel == "" {
		rel = "."
	}
	return Result{Kind: InRepo, Target: rel, File: rel}
}

func external(name string) Result { return Result{Kind: External, Target: ExternalPrefix + name} }

func unresolvedf(reason string) Result { return Result{Kind: Unresolved, Reason: reason} }

// Resolver resolves import specs of one language.
type Resolver interface {
	// Resolve resolves spec as written in fromFile (repo-relative,
	// slash-separated). root is the absolute repository root.
	Resolve(ctx context.Context, root, fromFile, spec string) Result
	// Invalidate drops what was cached for root (project configuration).
	Invalidate(root string)
}

// Request is one import to resolve in a batch.
type Request struct {
	FromFile string // repo-relative, slash-separated
	Spec     string
}

// BatchResolver resolves many imports of one root at once (a full scan, or
// every import of a changed file); out[i] is the result of reqs[i].
type BatchResolver interface {
	Resolver
	ResolveBatch(ctx context.Context, root string, reqs []Request) []Result
}

// resolveEach is ResolveBatch for resolvers without a faster batch path.
func resolveEach(ctx context.Context, r Resolver, root string, reqs []Request) []Result {
	out := make([]Result, len(reqs))
	for i, rq := range reqs {
		out[i] = r.Resolve(ctx, root, rq.FromFile, rq.Spec)
	}
	return out
}

// ResolveBatch implements BatchResolver.
func (g *Go) ResolveBatch(ctx context.Context, root string, reqs []Request) []Result {
	return resolveEach(ctx, g, root, reqs)
}

// ResolveBatch implements BatchResolver.
func (p *Python) ResolveBatch(ctx context.Context, root string, reqs []Request) []Result {
	return resolveEach(ctx, p, root, reqs)
}

var (
	_ BatchResolver = (*Go)(nil)
	_ BatchResolver = (*Python)(nil)
	_ BatchResolver = (*TypeScript)(nil)
	_ BatchResolver = (*Ruby)(nil)
	_ BatchResolver = (*Multi)(nil)
)

// ResolveBatch implements BatchResolver: requests are grouped by the
// language of the importing file and each group resolved in one batch.
func (m *Multi) ResolveBatch(ctx context.Context, root string, reqs []Request) []Result {
	out := make([]Result, len(reqs))
	groups := map[BatchResolver][]int{}
	var order []BatchResolver
	for i, rq := range reqs {
		r := m.forFileBatch(rq.FromFile)
		if r == nil {
			out[i] = unresolvedf("unsupported language")
			continue
		}
		if _, seen := groups[r]; !seen {
			order = append(order, r)
		}
		groups[r] = append(groups[r], i)
	}
	for _, r := range order {
		idx := groups[r]
		sub := make([]Request, len(idx))
		for j, i := range idx {
			sub[j] = reqs[i]
		}
		for j, res := range r.ResolveBatch(ctx, root, sub) {
			out[idx[j]] = res
		}
	}
	return out
}

// Multi dispatches to the resolver of the importing file's language.
type Multi struct {
	TS *TypeScript
	Go *Go
	Py *Python
	Rb *Ruby
}

var _ Resolver = (*Multi)(nil)

// New returns resolvers for every supported language. Call Close when done.
func New() *Multi {
	return &Multi{TS: NewTypeScript(), Go: NewGo(), Py: NewPython(), Rb: NewRuby()}
}

// Resolve implements Resolver; files of unknown languages are Unresolved.
func (m *Multi) Resolve(ctx context.Context, root, fromFile, spec string) Result {
	r := m.forFile(fromFile)
	if r == nil {
		return unresolvedf("unsupported language")
	}
	return r.Resolve(ctx, root, fromFile, spec)
}

// ResolveLang is Resolve with the language given explicitly.
func (m *Multi) ResolveLang(ctx context.Context, lang graph.Language, root, fromFile, spec string) Result {
	switch lang {
	case graph.Go:
		return m.Go.Resolve(ctx, root, fromFile, spec)
	case graph.TypeScript, graph.JavaScript:
		return m.TS.Resolve(ctx, root, fromFile, spec)
	case graph.Python:
		return m.Py.Resolve(ctx, root, fromFile, spec)
	case graph.Ruby:
		return m.Rb.Resolve(ctx, root, fromFile, spec)
	}
	return unresolvedf("unsupported language")
}

func (m *Multi) forFile(rel string) Resolver {
	if r := m.forFileBatch(rel); r != nil {
		return r
	}
	return nil // not a typed nil inside the interface
}

func (m *Multi) forFileBatch(rel string) BatchResolver {
	switch strings.ToLower(path.Ext(rel)) {
	case ".go":
		return m.Go
	case ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs":
		return m.TS
	case ".py", ".pyi":
		return m.Py
	}
	if IsRubyFile(rel) {
		return m.Rb
	}
	return nil
}

// Invalidate implements Resolver.
func (m *Multi) Invalidate(root string) {
	m.TS.Invalidate(root)
	m.Go.Invalidate(root)
	m.Py.Invalidate(root)
	m.Rb.Invalidate(root)
}

// InvalidateFor invalidates root when rel (repo-relative) is a configuration
// file; it reports whether it did.
func (m *Multi) InvalidateFor(root, rel string) bool {
	if !IsConfigFile(rel) {
		return false
	}
	m.Invalidate(root)
	return true
}

// Close releases the esbuild contexts.
func (m *Multi) Close() { m.TS.Close() }

// IsConfigFile reports whether a change to rel (a path; only the base name is
// looked at) can change how imports resolve, i.e. whether the cache of its
// root should be invalidated.
func IsConfigFile(rel string) bool {
	base := path.Base(filepath.ToSlash(rel))
	switch base {
	case "go.mod", "go.work", "package.json", "pnpm-workspace.yaml", "jsconfig.json",
		"pyproject.toml", "setup.cfg", "__init__.py",
		"Gemfile", "package.yml":
		return true
	}
	if strings.HasSuffix(base, ".gemspec") {
		return true
	}
	return strings.HasPrefix(base, "tsconfig") && strings.HasSuffix(base, ".json")
}

// toRel turns an absolute path into a slash-separated path relative to root,
// or reports false when it is outside root.
func toRel(root, abs string) (string, bool) {
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", false
	}
	return rel, true
}

// realRel is rel (repo-relative) with its symlinks resolved when it is a
// link, or under one, to a path inside the repository (realRoot: root with
// its symlinks resolved): the scan reads files at their real path, not
// through a link to a directory, so a target named through the link would
// leave the real one unreached. A link out of the repository stays as
// written.
func realRel(realRoot, rel string) string {
	real, err := filepath.EvalSymlinks(filepath.Join(realRoot, filepath.FromSlash(rel)))
	if err != nil {
		return rel
	}
	if r, ok := toRel(realRoot, real); ok {
		return r
	}
	return rel
}

// realRoot resolves symlinks of root (t.TempDir on macOS is under /var ->
// /private/var; esbuild reports real paths).
func realRoot(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r
	}
	return abs
}
