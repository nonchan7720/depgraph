// Package treesitter is the default, zero-config resolver. It extracts
// imports and top-level declarations per file using tree-sitter grammars.
// It does not resolve calls/references; the lsp/scip resolvers add those.
//
// Parsing uses gotreesitter (pure Go, no cgo). Only the per-language grammar
// packages are imported, never the aggregate grammars package, so the binary
// carries just the grammars listed in Detect.
package treesitter

import (
	"bytes"
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"

	gts "github.com/odvcencio/gotreesitter"
	golang "github.com/odvcencio/gotreesitter/grammars/go"
	"github.com/odvcencio/gotreesitter/grammars/javascript"
	"github.com/odvcencio/gotreesitter/grammars/python"
	"github.com/odvcencio/gotreesitter/grammars/tsx"
	"github.com/odvcencio/gotreesitter/grammars/typescript"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver"
)

// Resolver implements resolver.Resolver.
type Resolver struct {
	mu       sync.Mutex
	goModule map[string]string // repo root -> go module path (cached)
}

// New returns a tree-sitter resolver.
func New() *Resolver { return &Resolver{goModule: map[string]string{}} }

// Name implements resolver.Resolver.
func (r *Resolver) Name() string { return "treesitter" }

// Supports implements resolver.Resolver.
func (r *Resolver) Supports(rel string) bool {
	_, ok := detectLanguage(rel)
	return ok
}

// Detect maps a file path to a language and its tree-sitter grammar.
func Detect(rel string) (graph.Language, *gts.Language, bool) {
	lang, ok := detectLanguage(rel)
	if !ok {
		return "", nil, false
	}
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".go":
		return lang, golang.Language(), true
	case ".ts", ".mts", ".cts":
		return lang, typescript.Language(), true
	case ".tsx":
		return lang, tsx.Language(), true
	case ".js", ".mjs", ".cjs", ".jsx":
		return lang, javascript.Language(), true
	case ".py":
		return lang, python.Language(), true
	}
	return "", nil, false
}

// detectLanguage maps a file path to a language without loading its grammar
// (grammar tables are decoded lazily on first use).
func detectLanguage(rel string) (graph.Language, bool) {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".go":
		return graph.Go, true
	case ".ts", ".mts", ".cts", ".tsx":
		return graph.TypeScript, true
	case ".js", ".mjs", ".cjs", ".jsx":
		return graph.JavaScript, true
	case ".py":
		return graph.Python, true
	}
	return "", false
}

// parse parses src as a whole file. It stops early, returning ctx.Err(), when
// ctx is cancelled. The caller must Release the returned tree.
func parse(ctx context.Context, lang *gts.Language, src []byte) (*gts.Tree, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p := gts.NewParser(lang)
	if ctx.Done() != nil {
		var cancelled uint32
		p.SetCancellationFlag(&cancelled)
		stop := context.AfterFunc(ctx, func() { atomic.StoreUint32(&cancelled, 1) })
		defer stop()
	}
	tree, err := p.Parse(src)
	if err != nil {
		return nil, err
	}
	if tree.ParseStopReason() == gts.ParseStopCancelled {
		tree.Release()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, context.Canceled
	}
	return tree, nil
}

// ResolveFile implements resolver.Resolver.
func (r *Resolver) ResolveFile(ctx context.Context, root, rel string) (*graph.FileGraph, error) {
	lang, tsLang, ok := Detect(rel)
	if !ok {
		return nil, resolver.ErrUnsupported
	}
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, err
	}

	tree, err := parse(ctx, tsLang, src)
	if err != nil {
		return nil, err
	}
	defer tree.Release()

	fg := &graph.FileGraph{
		Path:     filepath.ToSlash(rel),
		Package:  graph.PackageKey(rel),
		Language: lang,
		Lines:    bytes.Count(src, []byte("\n")) + 1,
	}

	x := &extractor{lang: tsLang, src: src, fg: fg}
	rootNode := tree.RootNode()
	for i := 0; i < rootNode.NamedChildCount(); i++ {
		n := rootNode.NamedChild(i)
		switch lang {
		case graph.Go:
			x.goDecl(n)
		case graph.TypeScript, graph.JavaScript:
			x.tsDecl(n, false)
		case graph.Python:
			x.pyDecl(n)
		}
	}

	for i := range fg.Imports {
		fg.Imports[i].Target, fg.Imports[i].External = r.resolveImport(root, fg.Path, lang, fg.Imports[i].Spec)
	}
	return fg, nil
}

// ---- helpers -------------------------------------------------------------

// extractor collects symbols and imports of one parsed file. gotreesitter
// nodes need the grammar to report their type and fields, so it is kept here
// together with the source.
type extractor struct {
	lang *gts.Language
	src  []byte
	fg   *graph.FileGraph
}

func (x *extractor) typ(n *gts.Node) string { return n.Type(x.lang) }

func (x *extractor) field(n *gts.Node, name string) *gts.Node {
	return n.ChildByFieldName(name, x.lang)
}

func (x *extractor) text(n *gts.Node) string { return n.Text(x.src) }

func (x *extractor) fieldText(n *gts.Node, field string) string {
	c := x.field(n, field)
	if c == nil {
		return ""
	}
	return x.text(c)
}

func (x *extractor) addSym(n *gts.Node, kind, name string, exported bool) {
	if name == "" {
		return
	}
	sym := graph.Symbol{
		Name:     name,
		Kind:     kind,
		Exported: exported,
		Line:     int(n.StartPoint().Row) + 1,
		EndLine:  int(n.EndPoint().Row) + 1,
	}
	if id := x.field(n, "name"); id != nil {
		p := id.StartPoint()
		sym.NameLine = int(p.Row) + 1
		sym.Col = utf16Col(x.src, int(id.StartByte()), int(p.Column))
	}
	x.fg.Symbols = append(x.fg.Symbols, sym)
}

// utf16Col converts a tree-sitter byte column into an LSP column (UTF-16 code
// units). startByte is the byte offset of the node, byteCol its byte column.
func utf16Col(src []byte, startByte, byteCol int) int {
	lineStart := startByte - byteCol
	if lineStart < 0 || startByte > len(src) {
		return byteCol
	}
	n := 0
	for _, r := range string(src[lineStart:startByte]) {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

func (x *extractor) forEachDescendant(n *gts.Node, typ string, fn func(*gts.Node)) {
	var walk func(*gts.Node)
	walk = func(c *gts.Node) {
		if x.typ(c) == typ {
			fn(c)
			return
		}
		for i := 0; i < c.NamedChildCount(); i++ {
			walk(c.NamedChild(i))
		}
	}
	walk(n)
}

func unquote(s string) string { return strings.Trim(s, "`\"'") }

func goExported(name string) bool {
	return name != "" && name[0] >= 'A' && name[0] <= 'Z'
}

// ---- Go ------------------------------------------------------------------

func (x *extractor) goDecl(n *gts.Node) {
	switch x.typ(n) {
	case "import_declaration":
		x.forEachDescendant(n, "import_spec", func(spec *gts.Node) {
			if p := x.fieldText(spec, "path"); p != "" {
				x.fg.Imports = append(x.fg.Imports, graph.Import{Spec: unquote(p)})
			}
		})
	case "function_declaration":
		name := x.fieldText(n, "name")
		x.addSym(n, "function", name, goExported(name))
	case "method_declaration":
		name := x.fieldText(n, "name")
		x.addSym(n, "method", name, goExported(name))
	case "type_declaration":
		// type_spec (`type A B`) and type_alias (`type A = B`), in source order
		for i := 0; i < n.NamedChildCount(); i++ {
			ts := n.NamedChild(i)
			switch x.typ(ts) {
			case "type_spec":
				kind := "type"
				if t := x.field(ts, "type"); t != nil {
					switch x.typ(t) {
					case "struct_type":
						kind = "struct"
					case "interface_type":
						kind = "interface"
					}
				}
				name := x.fieldText(ts, "name")
				x.addSym(ts, kind, name, goExported(name))
			case "type_alias":
				name := x.fieldText(ts, "name")
				x.addSym(ts, "type", name, goExported(name))
			}
		}
	}
}

// ---- TypeScript / JavaScript ---------------------------------------------

func (x *extractor) tsDecl(n *gts.Node, exported bool) {
	switch x.typ(n) {
	case "import_statement":
		if s := x.fieldText(n, "source"); s != "" {
			x.fg.Imports = append(x.fg.Imports, graph.Import{Spec: unquote(s)})
		}
	case "export_statement":
		// `export function f(){}` / `export class C{}` / `export const x = ...`
		if d := x.field(n, "declaration"); d != nil {
			x.tsDecl(d, true)
		}
		// `export { a } from './b'` / `export * from './b'`
		if s := x.fieldText(n, "source"); s != "" {
			x.fg.Imports = append(x.fg.Imports, graph.Import{Spec: unquote(s)})
		}
	case "function_declaration", "generator_function_declaration":
		x.addSym(n, "function", x.fieldText(n, "name"), exported)
	case "class_declaration", "abstract_class_declaration":
		x.addSym(n, "class", x.fieldText(n, "name"), exported)
	case "interface_declaration":
		x.addSym(n, "interface", x.fieldText(n, "name"), exported)
	case "type_alias_declaration":
		x.addSym(n, "type", x.fieldText(n, "name"), exported)
	case "enum_declaration":
		x.addSym(n, "enum", x.fieldText(n, "name"), exported)
	case "lexical_declaration", "variable_declaration":
		for i := 0; i < n.NamedChildCount(); i++ {
			d := n.NamedChild(i)
			if x.typ(d) != "variable_declarator" {
				continue
			}
			kind := "variable"
			if v := x.field(d, "value"); v != nil {
				switch x.typ(v) {
				case "arrow_function", "function", "function_expression", "generator_function":
					kind = "function"
				}
			}
			x.addSym(d, kind, x.fieldText(d, "name"), exported)
		}
	}
}

// ---- Python --------------------------------------------------------------

func pyExported(name string) bool { return name != "" && !strings.HasPrefix(name, "_") }

func (x *extractor) pyDecl(n *gts.Node) {
	switch x.typ(n) {
	case "import_statement":
		for i := 0; i < n.NamedChildCount(); i++ {
			c := n.NamedChild(i)
			switch x.typ(c) {
			case "dotted_name":
				x.fg.Imports = append(x.fg.Imports, graph.Import{Spec: x.text(c)})
			case "aliased_import":
				if nm := x.fieldText(c, "name"); nm != "" {
					x.fg.Imports = append(x.fg.Imports, graph.Import{Spec: nm})
				}
			}
		}
	case "import_from_statement":
		if m := x.fieldText(n, "module_name"); m != "" {
			x.fg.Imports = append(x.fg.Imports, graph.Import{Spec: m})
		}
	case "function_definition":
		name := x.fieldText(n, "name")
		x.addSym(n, "function", name, pyExported(name))
	case "class_definition":
		name := x.fieldText(n, "name")
		x.addSym(n, "class", name, pyExported(name))
	case "decorated_definition":
		if d := x.field(n, "definition"); d != nil {
			x.pyDecl(d)
		}
	}
}

// ---- import resolution ---------------------------------------------------

func (r *Resolver) goModulePath(root string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m, ok := r.goModule[root]; ok {
		return m
	}
	m := ""
	if b, err := os.ReadFile(filepath.Join(root, "go.mod")); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "module ") {
				m = strings.TrimSpace(strings.TrimPrefix(line, "module "))
				break
			}
		}
	}
	r.goModule[root] = m
	return m
}

func isDir(root, rel string) bool {
	st, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil && st.IsDir()
}

func isFile(root, rel string) bool {
	st, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil && !st.IsDir()
}

func external(spec string) (string, bool) { return "ext:" + spec, true }

// resolveImport maps an import spec to an in-repo package key, or an external key.
func (r *Resolver) resolveImport(root, fromFile string, lang graph.Language, spec string) (string, bool) {
	switch lang {
	case graph.Go:
		mod := r.goModulePath(root)
		if mod == "" {
			return external(spec)
		}
		if spec == mod {
			return ".", false
		}
		if strings.HasPrefix(spec, mod+"/") {
			return strings.TrimPrefix(spec, mod+"/"), false
		}
		return external(spec)

	case graph.TypeScript, graph.JavaScript:
		var cand string
		switch {
		case strings.HasPrefix(spec, "."):
			cand = path.Clean(path.Join(path.Dir(fromFile), spec))
		case strings.HasPrefix(spec, "@/"): // common tsconfig alias: "@/*" -> "src/*"
			rest := strings.TrimPrefix(spec, "@/")
			if isDir(root, "src") {
				cand = path.Join("src", rest)
			} else {
				cand = rest
			}
		default:
			return external(spec)
		}
		return resolvePathToPackage(root, cand, []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"}, "index")

	case graph.Python:
		var cand string
		if strings.HasPrefix(spec, ".") {
			dots := 0
			for dots < len(spec) && spec[dots] == '.' {
				dots++
			}
			base := path.Dir(fromFile)
			for i := 1; i < dots; i++ {
				base = path.Dir(base)
			}
			rest := strings.ReplaceAll(spec[dots:], ".", "/")
			cand = path.Clean(path.Join(base, rest))
		} else {
			cand = strings.ReplaceAll(spec, ".", "/")
			if !isDir(root, cand) && !isFile(root, cand+".py") && isDir(root, "src") {
				cand = path.Join("src", cand)
			}
		}
		if t, ext := resolvePathToPackage(root, cand, []string{".py"}, "__init__"); !ext {
			return t, false
		}
		return external(spec)
	}
	return external(spec)
}

// resolvePathToPackage turns a repo-relative module path (without extension)
// into a package key: the directory containing the matched file/dir.
func resolvePathToPackage(root, cand string, exts []string, indexName string) (string, bool) {
	if isDir(root, cand) {
		return cand, false
	}
	for _, e := range exts {
		if isFile(root, cand+e) {
			return graph.PackageKey(cand + e), false
		}
	}
	// directory with index file
	for _, e := range exts {
		if isFile(root, path.Join(cand, indexName+e)) {
			return cand, false
		}
	}
	return external(cand)
}
