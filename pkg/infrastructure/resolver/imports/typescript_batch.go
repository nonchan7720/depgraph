package imports

import (
	"context"
	"encoding/json"
	"maps"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

// ResolveBatch resolves many imports at once with the same esbuild resolver
// as Resolve, but inside one esbuild build so that directory listings are
// cached for the batch (PluginBuild.Resolve of a long-lived context re-reads
// every directory from the file system root on each call).
//
// Each importer becomes an entry point whose contents are replaced by
// `import "<spec>"` lines; every resolved file loads as empty, and the
// resolutions are read back from the metafile. Specs esbuild cannot resolve
// fail the build, so they are collected from the errors and a second build
// runs without them.
func (t *TypeScript) ResolveBatch(ctx context.Context, root string, reqs []Request) []Result {
	out := make([]Result, len(reqs))
	if err := ctx.Err(); err != nil {
		for i := range out {
			out[i] = unresolvedf(err.Error())
		}
		return out
	}
	real := realRoot(root)

	// importer (repo-relative) -> specs, deduplicated, in first-seen order
	specs := map[string][]string{}
	var importers []string
	seen := map[Request]bool{}
	for _, rq := range reqs {
		rq.FromFile = filepath.ToSlash(rq.FromFile)
		if seen[rq] {
			continue
		}
		seen[rq] = true
		if _, ok := specs[rq.FromFile]; !ok {
			importers = append(importers, rq.FromFile)
		}
		specs[rq.FromFile] = append(specs[rq.FromFile], rq.Spec)
	}

	results := map[Request]Result{}
	failed := map[Request]string{}
	// an importer that does not exist would fail the whole build
	existing := importers[:0]
	for _, imp := range importers {
		if isFileAt(real, imp) {
			existing = append(existing, imp)
			continue
		}
		for _, s := range specs[imp] {
			results[Request{FromFile: imp, Spec: s}] = unresolvedf("importer " + imp + " not found")
		}
	}
	importers = existing
	// pass 1 usually succeeds; each failing pass removes the imports it
	// reported, so a later pass only fails on errors it cannot attribute
	for pass := 0; pass < 4 && len(importers) > 0; pass++ {
		br, err := tsBuild(ctx, real, importers, specs, failed)
		if err != nil {
			fillMissing(reqs, results, failed, unresolvedf(err.Error()))
			break
		}
		if len(br.failed) == 0 && len(br.other) == 0 {
			maps.Copy(results, br.resolved)
			break
		}
		maps.Copy(failed, br.failed)
		if len(br.failed) == 0 { // only errors we cannot attribute to an import
			fillMissing(reqs, results, failed, unresolvedf("esbuild: "+messagesText(br.other)))
			break
		}
	}
	for rq, reason := range failed {
		results[rq] = tsFailure(rq.Spec, reason)
	}
	for i, rq := range reqs {
		rq.FromFile = filepath.ToSlash(rq.FromFile)
		if res, ok := results[rq]; ok {
			out[i] = res
		} else {
			out[i] = unresolvedf("not resolved by esbuild")
		}
	}
	return out
}

// fillMissing sets res for every request without a result or failure yet.
func fillMissing(reqs []Request, results map[Request]Result, failed map[Request]string, res Result) {
	for _, rq := range reqs {
		rq.FromFile = filepath.ToSlash(rq.FromFile)
		if _, ok := results[rq]; ok {
			continue
		}
		if _, ok := failed[rq]; ok {
			continue
		}
		results[rq] = res
	}
}

type tsBuildResult struct {
	resolved map[Request]Result // when the build succeeded
	failed   map[Request]string // imports esbuild could not resolve
	other    []api.Message      // errors not attributable to an import
}

type tsMetafile struct {
	Inputs map[string]struct {
		Imports []struct {
			Path     string `json:"path"`
			External bool   `json:"external"`
			Original string `json:"original"`
		} `json:"imports"`
	} `json:"inputs"`
}

// probeLineRe matches the synthetic import lines (to attribute errors).
var probeLineRe = regexp.MustCompile(`^import ("(?:[^"\\]|\\.)*");$`)

// tsBuild runs one build and returns the resolutions (when the build
// succeeded) or the errors (when it did not).
func tsBuild(ctx context.Context, real string, importers []string, specs map[string][]string, skip map[Request]string) (tsBuildResult, error) {
	contents := map[string]string{} // absolute importer path -> synthetic source
	relOf := map[string]string{}
	var entries []string
	for _, imp := range importers {
		var b strings.Builder
		for _, s := range specs[imp] {
			if _, ok := skip[Request{FromFile: imp, Spec: s}]; ok {
				continue
			}
			q, _ := json.Marshal(s) // a JSON string is a valid JS string literal
			b.WriteString("import " + string(q) + ";\n")
		}
		abs := filepath.Join(real, filepath.FromSlash(imp))
		contents[abs] = b.String()
		relOf[abs] = imp
		entries = append(entries, abs)
	}
	plugin := api.Plugin{
		Name: "depgraph-probe",
		Setup: func(b api.PluginBuild) {
			b.OnLoad(api.OnLoadOptions{Filter: ".*", Namespace: "file"}, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
				if src, ok := contents[args.Path]; ok {
					dir := filepath.Dir(args.Path)
					return api.OnLoadResult{Contents: &src, ResolveDir: dir, Loader: api.LoaderJS}, nil
				}
				empty := ""
				return api.OnLoadResult{Contents: &empty, Loader: api.LoaderEmpty}, nil
			})
		},
	}
	bctx, cerr := api.Context(api.BuildOptions{
		AbsWorkingDir:     real,
		EntryPoints:       entries,
		Bundle:            true,
		Platform:          api.PlatformNode,
		ResolveExtensions: tsExtensions,
		Metafile:          true,
		Write:             false,
		Outdir:            filepath.Join(real, ".depgraph-esbuild-out"), // never written
		LogLevel:          api.LogLevelSilent,
		Plugins:           []api.Plugin{plugin},
	})
	if cerr != nil {
		return tsBuildResult{}, errorString("esbuild: " + messagesText(cerr.Errors))
	}
	defer bctx.Dispose()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			bctx.Cancel()
		case <-done:
		}
	}()
	res := bctx.Rebuild()
	if err := ctx.Err(); err != nil {
		return tsBuildResult{}, err
	}

	if len(res.Errors) > 0 {
		br := tsBuildResult{failed: map[Request]string{}}
		for _, m := range res.Errors {
			if rq, ok := attribute(real, relOf, m); ok {
				br.failed[rq] = m.Text
			} else {
				br.other = append(br.other, m)
			}
		}
		return br, nil
	}

	var meta tsMetafile
	if err := json.Unmarshal([]byte(res.Metafile), &meta); err != nil {
		return tsBuildResult{}, err
	}
	resolved := map[Request]Result{}
	for key, in := range meta.Inputs {
		imp, ok := relOf[filepath.Join(real, filepath.FromSlash(key))]
		if !ok {
			continue
		}
		for _, im := range in.Imports {
			spec := im.Original
			if spec == "" {
				spec = im.Path
			}
			ns, p := "file", im.Path
			if i := strings.Index(p, ":"); i > 1 && !filepath.IsAbs(p) {
				// "<namespace>:<path>" for non-file namespaces
				ns, p = p[:i], p[i+1:]
			}
			if ns == "file" && !im.External && !filepath.IsAbs(p) {
				p = filepath.Join(real, filepath.FromSlash(p))
			}
			resolved[Request{FromFile: imp, Spec: spec}] = tsResolved(real, spec, p, im.External, ns)
		}
	}
	return tsBuildResult{resolved: resolved}, nil
}

// attribute finds the synthetic import line an error points at.
func attribute(real string, relOf map[string]string, m api.Message) (Request, bool) {
	if m.Location == nil {
		return Request{}, false
	}
	sm := probeLineRe.FindStringSubmatch(m.Location.LineText)
	if sm == nil {
		return Request{}, false
	}
	spec, err := strconv.Unquote(sm[1])
	if err != nil {
		return Request{}, false
	}
	imp, ok := relOf[filepath.Join(real, filepath.FromSlash(m.Location.File))]
	if !ok {
		return Request{}, false
	}
	return Request{FromFile: imp, Spec: spec}, true
}

type errorString string

func (e errorString) Error() string { return string(e) }
