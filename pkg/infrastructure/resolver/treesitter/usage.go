package treesitter

import (
	"bytes"
	"context"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	gts "github.com/odvcencio/gotreesitter"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver/imports"
)

// Usage extraction: per file, which names it uses from which file or
// package, the names it exports under another name, and the facts that make
// it an entry point. The unused-code rules join these across the project.
//
// Specs are resolved together with the file's imports (one batch), so the
// targets are exactly what the import resolver gives the dependency edges.

// pendingUse is a use whose target is the resolution of spec.
type pendingUse struct {
	spec     string
	name     string
	reexport bool
	as       string
	// prefix: the target is every file under the resolved directory.
	prefix bool
}

// goSelector is `q.Name` in a Go file; q is matched against the imports
// once they are resolved (the package name of an import is only known from
// its directory).
type goSelector struct{ q, name string }

// goImport is one import spec of a Go file.
type goImport struct{ alias, spec string }

// usageCollector gathers the usage of one file while its tree is walked.
type usageCollector struct {
	x    *extractor
	rel  string
	lang graph.Language

	pending []pendingUse
	direct  []graph.Use
	exports []graph.ExportName
	keep    []string
	// source file names in strings (see graph.Usage.Mentions)
	mentions []string

	// facts for entry detection
	goMain    bool
	mainGuard bool // Python: if __name__ == "__main__"; Ruby: if __FILE__ == $0
	shebang   bool // Ruby: the file starts with #!
	template  bool // Ruby: a view template (ERB, Haml, Slim)

	rbRefs  []rbRef     // Ruby constant references
	rbGlobs []rbGlobDir // Ruby: directories of file globs (rbGlobUses)
	rbEntry string      // Ruby: why a class of the file is loaded by Rails itself

	goImports   []goImport
	goSelectors []goSelector
	dirPrefixes []string                   // repo-relative directories every file of which is used
	members     []memberRef                // members.go
	memberCalls map[string]map[string]bool // members.go: owner + "\x00" + member -> methods called
	tsBind      map[string]tsBinding       // TS/JS: local names bound by imports
	pyBind      map[string]*pyBinding      // Python: local names bound by imports
}

func (u *usageCollector) use(spec, name string) {
	if spec != "" {
		u.pending = append(u.pending, pendingUse{spec: spec, name: name})
	}
}

func (u *usageCollector) reExport(spec, name, as string) {
	if spec != "" {
		u.pending = append(u.pending, pendingUse{spec: spec, name: name, reexport: true, as: as})
	}
}

func (u *usageCollector) usePrefix(spec string) {
	if spec != "" {
		u.pending = append(u.pending, pendingUse{spec: spec, name: "*", prefix: true})
	}
}

// self records a use of name declared by this file (or, for Go, by its
// package) from outside its own declaration.
func (u *usageCollector) self(target, name string) {
	u.direct = append(u.direct, graph.Use{Target: target, Name: name})
}

// requests are the specs to resolve with the file's imports.
func (u *usageCollector) requests() []imports.Request {
	seen := map[string]bool{}
	var out []imports.Request
	add := func(spec string) {
		if spec != "" && !seen[spec] {
			seen[spec] = true
			out = append(out, imports.Request{FromFile: u.rel, Spec: spec})
		}
	}
	for _, p := range u.pending {
		add(p.spec)
	}
	for _, gi := range u.goImports {
		add(gi.spec)
	}
	u.memberSpecs(add)
	u.rbRequests(add)
	return out
}

// collectUsage walks the parsed file and returns its collector.
func (x *extractor) collectUsage(lang graph.Language, root *gts.Node) *usageCollector {
	u := &usageCollector{x: x, rel: x.fg.Path, lang: lang}
	switch lang {
	case graph.Go:
		u.goFile(root)
		u.goMembers(root)
	case graph.TypeScript, graph.JavaScript:
		u.tsFile(root)
		u.tsMembers(root)
	case graph.Python:
		u.pyFile(root)
		u.pyMembers(root)
	case graph.Ruby:
		u.rbFile(root)
	}
	u.collectMentions(root)
	return u
}

// mentionRe: a string that names a source file ("render.js", "./a/b.py").
var mentionRe = regexp.MustCompile(`^[\w@.-][\w@./-]*\.(js|jsx|mjs|cjs|ts|tsx|mts|cts|py|rb)$`)

// collectMentions records the source file names in the file's strings.
func (u *usageCollector) collectMentions(root *gts.Node) {
	x := u.x
	x.walk(root, func(n *gts.Node) bool {
		switch x.typ(n) {
		case "import_statement", "import_declaration", "import_from_statement":
			return false // specs, resolved as imports
		case "export_statement":
			if x.field(n, "source") != nil {
				return false
			}
		}
		if s, ok := x.stringContent(n); ok {
			if len(s) < 256 && mentionRe.MatchString(s) && !strings.Contains(s, "..") {
				u.mentions = append(u.mentions, strings.TrimPrefix(s, "./"))
			}
			return false
		}
		return true
	})
}

// finishUsage turns the collected usage into fg.Usage, with resolved holding
// the resolution of each of reqs.
func (r *Resolver) finishUsage(ctx context.Context, root string, fg *graph.FileGraph, u *usageCollector,
	reqs []imports.Request, resolved []imports.Result) {
	bySpec := make(map[string]imports.Result, len(reqs))
	for i, rq := range reqs {
		if i < len(resolved) {
			bySpec[rq.Spec] = resolved[i]
		}
	}
	m := r.manifests(ctx, root)
	if fg.Language == graph.TypeScript || fg.Language == graph.JavaScript {
		// a package that is not installed is third-party when a package.json
		// of the file declares it
		for i := range fg.Imports {
			im := &fg.Imports[i]
			if im.External && im.Class == "" {
				if by := m.declaredBy(fg.Path, strings.TrimPrefix(im.Target, imports.ExternalPrefix)); by != "" {
					im.Class, im.Basis = graph.ClassThirdParty, "declared in "+by
				}
			}
		}
	}
	us := append([]graph.Use(nil), u.direct...)
	for _, p := range u.pending {
		res, ok := bySpec[p.spec]
		if !ok {
			continue
		}
		if res.Kind != imports.InRepo || res.File == "" {
			switch {
			case p.prefix && (fg.Language == graph.TypeScript || fg.Language == graph.JavaScript) && m.unknownTarget(fg, p.spec, res):
				// a directory under an alias the resolver cannot place
				us = append(us, graph.Use{Target: UnknownPrefix + strings.TrimSuffix(p.spec, "/") + "/", Name: "*"})
			case !p.prefix && m.unknownTarget(fg, p.spec, res):
				us = append(us, graph.Use{Target: UnknownPrefix + p.spec, Name: p.name, ReExport: p.reexport, As: p.as})
			}
			continue
		}
		target := res.File
		if p.prefix {
			if !isDir(root, target) {
				target = path.Dir(target)
			}
			target = strings.TrimSuffix(target, "/") + "/"
		}
		us = append(us, graph.Use{Target: target, Name: p.name, ReExport: p.reexport, As: p.as})
	}
	var goBinds map[string]string
	if fg.Language == graph.Go {
		var goUses []graph.Use
		goUses, goBinds = u.goUses(root, bySpec)
		us = append(us, goUses...)
	}
	u.resolveMembers(fg, bySpec, goBinds)
	if fg.Language == graph.Ruby {
		fg.Imports = append(fg.Imports, u.rbConstImports(bySpec)...)
	}
	if fg.Language == graph.Ruby {
		u.rbGlobUses(root, m.scope(fg.Path, fg.Language))
	}
	us = append(us, u.dirPrefixUses(root)...)

	entry := m.entry(fg.Path, fg.Language, u)
	fg.Usage = &graph.Usage{
		Scope:     m.scope(fg.Path, fg.Language),
		Entry:     entry,
		Internal:  m.internalAPI(fg.Path, fg.Language, entry),
		Generated: generated(u.x.src) || m.isVendored(fg.Path),
		Uses:      dedupUses(us),
		Exports:   u.exports,
		Keep:      dedupStrings(u.keep),
		Mentions:  dedupStrings(u.mentions),
	}
}

// UnknownPrefix marks the Target of a use whose import the resolver could
// not place inside the repository although it is not a declared dependency
// (an alias of a bundler or site generator, a missing file): the rule
// matches the spec against the project's paths.
const UnknownPrefix = "?"

// unknownTarget reports whether an import of a TS/JS file that did not
// resolve into the repository may still name a file of it.
func (m *manifests) unknownTarget(fg *graph.FileGraph, spec string, res imports.Result) bool {
	if fg.Language != graph.TypeScript && fg.Language != graph.JavaScript {
		return false
	}
	if spec == "" || strings.Contains(spec, ":") || strings.HasPrefix(spec, "/") {
		return false // node:fs, virtual:x, https://, absolute paths
	}
	if ext := path.Ext(spec); ext != "" && !strings.Contains(ext, "/") && !hasSourceExt(spec) {
		return false // ./App.module.css, ../config.json: not code
	}
	if res.Kind == imports.Unresolved {
		return true
	}
	pkg := npmPackageName(spec)
	return !nodeBuiltins[pkg] && m.undeclared(fg.Path, pkg)
}

// npmPackageName is the package of a bare spec ("@a/b" of "@a/b/c", "x" of "x/y").
func npmPackageName(spec string) string {
	parts := strings.SplitN(spec, "/", 3)
	if strings.HasPrefix(spec, "@") && len(parts) > 1 {
		return parts[0] + "/" + parts[1]
	}
	return parts[0]
}

var nodeBuiltins = func() map[string]bool {
	m := map[string]bool{}
	for n := range strings.FieldsSeq(`assert async_hooks buffer child_process cluster console constants
		crypto dgram diagnostics_channel dns domain events fs http http2 https inspector module net os
		path perf_hooks process punycode querystring readline repl stream string_decoder sys timers tls
		trace_events tty url util v8 vm wasi worker_threads zlib test sqlite`) {
		m[n] = true
	}
	return m
}()

func isDir(root, rel string) bool {
	st, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil && st.IsDir()
}

func isFile(root, rel string) bool {
	st, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil && !st.IsDir()
}

func dedupUses(us []graph.Use) []graph.Use {
	seen := make(map[graph.Use]bool, len(us))
	out := us[:0]
	for _, x := range us {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Encode() < out[j].Encode() })
	return out
}

func dedupStrings(ss []string) []string {
	if len(ss) == 0 {
		return nil
	}
	sort.Strings(ss)
	out := ss[:1]
	for _, s := range ss[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}

// generatedRe matches the markers code generators put at the top of a file
// (Go's "Code generated ... DO NOT EDIT.", "@generated", ...).
var generatedRe = regexp.MustCompile(`(?i)(code generated .* do not edit|@generated|auto-?generated|do not edit)`)

// generated reports whether the file's header says it is generated.
func generated(src []byte) bool {
	head := src
	if len(head) > 1024 {
		head = head[:1024]
	}
	// only comment lines count: a string in code is not a marker
	for line := range bytes.SplitSeq(head, []byte("\n")) {
		t := bytes.TrimSpace(line)
		if bytes.HasPrefix(t, []byte("//")) || bytes.HasPrefix(t, []byte("#")) ||
			bytes.HasPrefix(t, []byte("/*")) || bytes.HasPrefix(t, []byte("*")) {
			if generatedRe.Match(t) {
				return true
			}
		}
	}
	return false
}

// entryMarkerRe is the explicit marker for an entry point nothing in the
// repository shows (a plugin loaded by name, a file run by an external tool).
var entryMarkerRe = regexp.MustCompile(`(?m)^\s*(//|#|/\*|\*)\s*depgraph:entry\b`)

// walk calls fn for every named node under n (n included), depth first.
// fn returns false to skip the node's children.
func (x *extractor) walk(n *gts.Node, fn func(*gts.Node) bool) {
	if n == nil || !fn(n) {
		return
	}
	for i := 0; i < n.NamedChildCount(); i++ {
		x.walk(n.NamedChild(i), fn)
	}
}

// hasToken reports whether n has an anonymous child token tok
// ("default", "type", "=").
func (x *extractor) hasToken(n *gts.Node, tok string) bool {
	for i := 0; i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c != nil && !c.IsNamed() && x.typ(c) == tok {
			return true
		}
	}
	return false
}

// stringContent is the text of a string literal without quotes ("" for a
// string with interpolations).
func (x *extractor) stringContent(n *gts.Node) (string, bool) {
	switch x.typ(n) {
	case "string", "interpreted_string_literal", "raw_string_literal":
	default:
		return "", false
	}
	var b strings.Builder
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		switch x.typ(c) {
		case "string_fragment", "string_content", "interpreted_string_literal_content", "raw_string_literal_content":
			b.WriteString(x.text(c))
		case "string_start", "string_end", "escape_sequence":
		default: // interpolation
			return "", false
		}
	}
	if n.NamedChildCount() == 0 {
		return unquote(x.text(n)), true
	}
	return b.String(), true
}

// staticPrefix is the literal text before the first interpolation of a
// template / f-string ("./locales/" of `./locales/${l}.ts`).
func (x *extractor) staticPrefix(n *gts.Node) string {
	var b strings.Builder
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		switch x.typ(c) {
		case "string_fragment", "string_content":
			b.WriteString(x.text(c))
		case "string_start":
		default:
			return b.String()
		}
	}
	return b.String()
}

// declNames are the names a top-level statement declares (to tell a use of
// a name from its own declaration).
func (x *extractor) declNames(n *gts.Node) map[string]bool {
	out := map[string]bool{}
	var add func(*gts.Node)
	add = func(d *gts.Node) {
		if d == nil {
			return
		}
		switch x.typ(d) {
		case "export_statement":
			add(x.field(d, "declaration"))
		case "decorated_definition":
			add(x.field(d, "definition"))
		case "lexical_declaration", "variable_declaration":
			for i := 0; i < d.NamedChildCount(); i++ {
				if c := d.NamedChild(i); x.typ(c) == "variable_declarator" {
					for _, id := range x.patternNames(x.field(c, "name")) {
						out[x.text(id)] = true
					}
				}
			}
		case "type_declaration", "const_declaration", "var_declaration":
			for i := 0; i < d.NamedChildCount(); i++ {
				c := d.NamedChild(i)
				switch x.typ(c) {
				case "type_spec", "type_alias", "const_spec", "var_spec":
					if nm := x.fieldText(c, "name"); nm != "" {
						out[nm] = true
					}
				}
			}
		default:
			if nm := x.fieldText(d, "name"); nm != "" {
				out[nm] = true
			}
		}
	}
	add(n)
	return out
}
