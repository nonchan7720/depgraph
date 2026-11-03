package imports

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

// TypeScript resolves TypeScript / JavaScript imports with esbuild's resolver
// (the one its bundler uses), obtained as PluginBuild.Resolve from a plugin of
// a never-built esbuild context. esbuild reads tsconfig.json / jsconfig.json
// (paths, baseUrl, extends) from the importer's directory upwards, package.json
// (exports, imports, main / module) and node_modules, and follows symlinks, so
// a workspace package linked into node_modules resolves to its real directory.
//
// Imports resolved into node_modules are External, keyed by package name.
type TypeScript struct {
	cache rootCache[*tsRoot]
}

var _ Resolver = (*TypeScript)(nil)

type tsRoot struct {
	real    string // root with symlinks resolved (esbuild reports real paths)
	ctx     api.BuildContext
	resolve func(string, api.ResolveOptions) api.ResolveResult
	err     string
}

// NewTypeScript returns a TypeScript / JavaScript resolver. Call Close when done.
func NewTypeScript() *TypeScript { return &TypeScript{} }

// tsExtensions are tried for extensionless imports, in esbuild's default
// order plus the module-specific TypeScript / JavaScript extensions.
var tsExtensions = []string{".tsx", ".ts", ".mts", ".cts", ".jsx", ".js", ".mjs", ".cjs", ".css", ".json"}

func loadTS(root string) *tsRoot {
	r := &tsRoot{real: realRoot(root)}
	plugin := api.Plugin{
		Name: "depgraph-resolve",
		Setup: func(b api.PluginBuild) {
			r.resolve = b.Resolve
		},
	}
	ctx, cerr := api.Context(api.BuildOptions{
		AbsWorkingDir:     r.real,
		Bundle:            true, // resolve like the bundler does (node builtins external, ...)
		Platform:          api.PlatformNode,
		ResolveExtensions: tsExtensions,
		LogLevel:          api.LogLevelSilent,
		Write:             false,
		Plugins:           []api.Plugin{plugin},
	})
	if cerr != nil {
		r.err = "esbuild: " + messagesText(cerr.Errors)
		r.resolve = nil
		return r
	}
	r.ctx = ctx
	return r
}

func (r *tsRoot) dispose() {
	if r != nil && r.ctx != nil {
		r.ctx.Dispose()
	}
}

// Resolve implements Resolver.
func (t *TypeScript) Resolve(ctx context.Context, root, fromFile, spec string) Result {
	if err := ctx.Err(); err != nil {
		return unresolvedf(err.Error())
	}
	r := t.cache.get(root, loadTS)
	if r.resolve == nil {
		return unresolvedf(r.err)
	}
	importer := filepath.Join(r.real, filepath.FromSlash(fromFile))
	opts := api.ResolveOptions{
		Importer:   importer,
		ResolveDir: filepath.Dir(importer),
		Namespace:  "file",
		Kind:       api.ResolveJSImportStatement,
	}
	res := r.resolve(spec, opts)
	if len(res.Errors) > 0 || res.Path == "" {
		return tsFailure(spec, messagesText(res.Errors))
	}
	return tsResolved(r.real, spec, res.Path, res.External, res.Namespace)
}

// tsFailure classifies a spec esbuild could not resolve.
func tsFailure(spec, reason string) Result {
	if isPathSpec(spec) || strings.HasPrefix(spec, "#") || !validPackageName(packageName(spec)) {
		// meant to be inside the project: a missing file, an unknown alias
		// or package.json "imports" entry
		return unresolvedf(reason)
	}
	// a package that is not installed
	return external(packageName(spec))
}

// tsResolved classifies what esbuild resolved spec to.
func tsResolved(realRoot, spec, p string, isExternal bool, namespace string) Result {
	if isExternal || (namespace != "" && namespace != "file") {
		// node builtins ("fs", "node:fs"), URLs
		return external(packageName(spec))
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	slash := filepath.ToSlash(p)
	if i := strings.LastIndex(slash, "/node_modules/"); i >= 0 {
		if name := packageName(slash[i+len("/node_modules/"):]); validPackageName(name) {
			return external(name)
		}
		return external(packageName(spec))
	}
	if rel, ok := toRel(realRoot, p); ok {
		return inRepoFile(rel)
	}
	// resolved to a file outside the repository
	if isPathSpec(spec) {
		return external(spec)
	}
	return external(packageName(spec))
}

// Invalidate implements Resolver.
func (t *TypeScript) Invalidate(root string) {
	for _, r := range t.cache.drop(root) {
		r.dispose()
	}
}

// Close disposes every esbuild context.
func (t *TypeScript) Close() {
	for _, r := range t.cache.dropAll() {
		r.dispose()
	}
}

func isPathSpec(spec string) bool {
	return spec == "." || spec == ".." || strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") ||
		strings.HasPrefix(spec, "/") || filepath.IsAbs(spec)
}

// packageName is the npm package of a bare spec: "lodash/fp" -> "lodash",
// "@scope/pkg/sub" -> "@scope/pkg". "node:fs" stays "node:fs".
func packageName(spec string) string {
	parts := strings.SplitN(spec, "/", 3)
	if strings.HasPrefix(spec, "@") && len(parts) >= 2 {
		return parts[0] + "/" + parts[1]
	}
	return parts[0]
}

// validPackageName rejects names npm cannot publish, which are therefore
// aliases ("@/x", "~/x") rather than missing packages.
func validPackageName(name string) bool {
	if name == "" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || strings.HasPrefix(name, "~") {
		return false
	}
	if strings.HasPrefix(name, "@") {
		scope, pkg, ok := strings.Cut(name[1:], "/")
		return ok && scope != "" && pkg != ""
	}
	return true
}

func messagesText(msgs []api.Message) string {
	texts := make([]string, 0, len(msgs))
	for _, m := range msgs {
		texts = append(texts, m.Text)
	}
	return strings.Join(texts, "; ")
}
