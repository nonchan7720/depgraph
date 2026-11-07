package imports

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/nonchan7720/depgraph/pkg/internal/rootcache"

	"github.com/evanw/esbuild/pkg/api"
)

// TypeScript resolves TypeScript / JavaScript imports with esbuild's resolver
// (the one its bundler uses), obtained as PluginBuild.Resolve from a plugin of
// a never-built esbuild context. esbuild reads tsconfig.json / jsconfig.json
// (paths, baseUrl, extends) from the importer's directory upwards, package.json
// (exports, imports, main / module) and node_modules, and follows symlinks, so
// a workspace package linked into node_modules resolves to its real directory.
// Workspace packages that are not linked (no install yet: a fresh clone, CI,
// the gate's worktrees) resolve through links depgraph makes itself (see
// linkWorkspace).
//
// Imports resolved into node_modules are External, keyed by package name.
type TypeScript struct {
	cache rootcache.Cache[*tsRoot]
}

var _ Resolver = (*TypeScript)(nil)

type tsRoot struct {
	real    string // root with symlinks resolved (esbuild reports real paths)
	ctx     api.BuildContext
	resolve func(string, api.ResolveOptions) api.ResolveResult
	err     string
	// links: the temporary directory of links to the workspace packages
	// ("" when none); workspace: their names.
	links     string
	workspace map[string]bool
}

// NewTypeScript returns a TypeScript / JavaScript resolver. Call Close when done.
func NewTypeScript() *TypeScript { return &TypeScript{} }

// tsExtensions are tried for extensionless imports, in esbuild's default
// order plus the module-specific TypeScript / JavaScript extensions.
var tsExtensions = []string{".tsx", ".ts", ".mts", ".cts", ".jsx", ".js", ".mjs", ".cjs", ".css", ".json"}

func loadTS(root string) *tsRoot {
	r := &tsRoot{real: realRoot(root), workspace: map[string]bool{}}
	pkgs := workspacePackages(r.real)
	for _, p := range pkgs {
		r.workspace[p.Name] = true
	}
	r.links = linkWorkspace(r.real, pkgs)
	var nodePaths []string
	if r.links != "" {
		nodePaths = []string{r.links}
	}
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
		NodePaths:         nodePaths,
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
	if r == nil {
		return
	}
	if r.ctx != nil {
		r.ctx.Dispose()
	}
	if r.links != "" {
		_ = os.RemoveAll(r.links)
	}
}

// Resolve implements Resolver.
func (t *TypeScript) Resolve(ctx context.Context, root, fromFile, spec string) Result {
	if err := ctx.Err(); err != nil {
		return unresolvedf(err.Error())
	}
	r := t.cache.Get(root, loadTS)
	if r.resolve == nil {
		return unresolvedf(r.err)
	}
	if bad := tsUnsafe(r.real, fromFile, map[string]string{}); bad != "" {
		return unresolvedf(bad + " is not a regular file (esbuild would block on it)")
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
		if r.workspace[packageName(spec)] {
			// a package of the repository: a subpath it does not export
			return unresolvedf(messagesText(res.Errors))
		}
		return tsFailure(spec, messagesText(res.Errors))
	}
	return tsResolved(r.real, spec, res.Path, res.External, res.Namespace)
}

// tsConfigNames are the files esbuild reads in the importer's directory
// and in every directory above it.
var tsConfigNames = []string{"package.json", "tsconfig.json", "jsconfig.json"}

// tsUnsafe returns the first of the importer and the configuration files
// esbuild reads for it (repo-relative) that exists but is not a regular
// file: esbuild reads them with a plain open, which blocks on a FIFO a pull
// request can add. seen caches the answer per directory.
func tsUnsafe(real, fromFile string, seen map[string]string) string {
	regular := func(rel string) bool {
		st, err := os.Stat(filepath.Join(real, filepath.FromSlash(rel)))
		return err != nil || st.Mode().IsRegular() // missing is fine
	}
	if !regular(fromFile) {
		return fromFile
	}
	for d := path.Dir(fromFile); ; d = path.Dir(d) {
		bad, ok := seen[d]
		if !ok {
			for _, name := range tsConfigNames {
				if rel := path.Join(d, name); !regular(rel) {
					bad = rel
					break
				}
			}
			seen[d] = bad
		}
		if bad != "" {
			return bad
		}
		if d == "." || d == "/" || d == "" {
			return ""
		}
	}
}

// tsFailure classifies a spec esbuild could not resolve.
func tsFailure(spec, reason string) Result {
	if isPathSpec(spec) || strings.HasPrefix(spec, "#") || !validPackageName(packageName(spec)) {
		// meant to be inside the project: a missing file, an unknown alias
		// or package.json "imports" entry
		return unresolvedf(reason)
	}
	// a package that is not installed: whether it is declared is the
	// manifests' call (package.json)
	return external(packageName(spec)).classed("", "not installed")
}

// tsResolved classifies what esbuild resolved spec to.
func tsResolved(realRoot, spec, p string, isExternal bool, namespace string) Result {
	if isExternal {
		// esbuild resolves a Node built-in ("fs", "node:fs") to itself,
		// before node_modules ("punycode/" is the installed package)
		return external(packageName(spec)).classed(ClassStandard, "Node built-in (esbuild)")
	}
	if namespace != "" && namespace != "file" {
		return external(packageName(spec)).classed("", "resolved to namespace "+namespace)
	}
	// a bundler query ("worker.ts?worker&inline", Vite) is not part of the
	// file: esbuild's metafile keeps it as the path's suffix
	if i := strings.IndexByte(p, '?'); i > 0 {
		p = p[:i]
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	slash := filepath.ToSlash(p)
	if _, after, ok := strings.CutLast(slash, "/node_modules/"); ok {
		name := packageName(after)
		if !validPackageName(name) {
			name = packageName(spec)
		}
		return external(name).classed(ClassThirdParty, "installed in node_modules")
	}
	if rel, ok := toRel(realRoot, p); ok {
		return inRepoFile(rel)
	}
	// resolved to a file outside the repository
	if isPathSpec(spec) {
		return external(spec).classed("", "a file outside the repository")
	}
	return external(packageName(spec)).classed("", "resolved outside the repository and node_modules")
}

// Invalidate implements Resolver.
func (t *TypeScript) Invalidate(root string) {
	for _, r := range t.cache.Drop(root) {
		r.dispose()
	}
}

// Close disposes every esbuild context.
func (t *TypeScript) Close() {
	for _, r := range t.cache.DropAll() {
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
