// Package treesitter is the default, zero-config resolver. It extracts
// imports and top-level declarations per file using tree-sitter grammars.
// It does not resolve calls/references; the lsp/scip resolvers add those.
package treesitter

import (
	"bytes"
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/typescript/tsx"
	"github.com/smacker/go-tree-sitter/typescript/typescript"

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
	_, _, ok := Detect(rel)
	return ok
}

// Detect maps a file path to a language and its tree-sitter grammar.
func Detect(rel string) (graph.Language, *sitter.Language, bool) {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".go":
		return graph.Go, golang.GetLanguage(), true
	case ".ts", ".mts", ".cts":
		return graph.TypeScript, typescript.GetLanguage(), true
	case ".tsx":
		return graph.TypeScript, tsx.GetLanguage(), true
	case ".js", ".mjs", ".cjs", ".jsx":
		return graph.JavaScript, javascript.GetLanguage(), true
	case ".py":
		return graph.Python, python.GetLanguage(), true
	}
	return "", nil, false
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

	p := sitter.NewParser()
	p.SetLanguage(tsLang)
	tree, err := p.ParseCtx(ctx, nil, src)
	if err != nil {
		return nil, err
	}
	defer tree.Close()

	fg := &graph.FileGraph{
		Path:     filepath.ToSlash(rel),
		Package:  graph.PackageKey(rel),
		Language: lang,
		Lines:    bytes.Count(src, []byte("\n")) + 1,
	}

	rootNode := tree.RootNode()
	for i := 0; i < int(rootNode.NamedChildCount()); i++ {
		n := rootNode.NamedChild(i)
		switch lang {
		case graph.Go:
			extractGo(n, src, fg)
		case graph.TypeScript, graph.JavaScript:
			extractTS(n, src, fg, false)
		case graph.Python:
			extractPy(n, src, fg)
		}
	}

	for i := range fg.Imports {
		fg.Imports[i].Target, fg.Imports[i].External = r.resolveImport(root, fg.Path, lang, fg.Imports[i].Spec)
	}
	return fg, nil
}

// ---- helpers -------------------------------------------------------------

func addSym(fg *graph.FileGraph, n *sitter.Node, src []byte, kind, name string, exported bool) {
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
	if id := n.ChildByFieldName("name"); id != nil {
		p := id.StartPoint()
		sym.NameLine = int(p.Row) + 1
		sym.Col = utf16Col(src, int(id.StartByte()), int(p.Column))
	}
	fg.Symbols = append(fg.Symbols, sym)
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

func fieldText(n *sitter.Node, field string, src []byte) string {
	c := n.ChildByFieldName(field)
	if c == nil {
		return ""
	}
	return c.Content(src)
}

func forEachDescendant(n *sitter.Node, typ string, fn func(*sitter.Node)) {
	var walk func(*sitter.Node)
	walk = func(x *sitter.Node) {
		if x.Type() == typ {
			fn(x)
			return
		}
		for i := 0; i < int(x.NamedChildCount()); i++ {
			walk(x.NamedChild(i))
		}
	}
	walk(n)
}

func unquote(s string) string { return strings.Trim(s, "`\"'") }

func goExported(name string) bool {
	return name != "" && name[0] >= 'A' && name[0] <= 'Z'
}

// ---- Go ------------------------------------------------------------------

func extractGo(n *sitter.Node, src []byte, fg *graph.FileGraph) {
	switch n.Type() {
	case "import_declaration":
		forEachDescendant(n, "import_spec", func(spec *sitter.Node) {
			if p := fieldText(spec, "path", src); p != "" {
				fg.Imports = append(fg.Imports, graph.Import{Spec: unquote(p)})
			}
		})
	case "function_declaration":
		name := fieldText(n, "name", src)
		addSym(fg, n, src, "function", name, goExported(name))
	case "method_declaration":
		name := fieldText(n, "name", src)
		addSym(fg, n, src, "method", name, goExported(name))
	case "type_declaration":
		forEachDescendant(n, "type_spec", func(ts *sitter.Node) {
			kind := "type"
			if t := ts.ChildByFieldName("type"); t != nil {
				switch t.Type() {
				case "struct_type":
					kind = "struct"
				case "interface_type":
					kind = "interface"
				}
			}
			name := fieldText(ts, "name", src)
			addSym(fg, ts, src, kind, name, goExported(name))
		})
	}
}

// ---- TypeScript / JavaScript ---------------------------------------------

func extractTS(n *sitter.Node, src []byte, fg *graph.FileGraph, exported bool) {
	switch n.Type() {
	case "import_statement":
		if s := fieldText(n, "source", src); s != "" {
			fg.Imports = append(fg.Imports, graph.Import{Spec: unquote(s)})
		}
	case "export_statement":
		// `export function f(){}` / `export class C{}` / `export const x = ...`
		if d := n.ChildByFieldName("declaration"); d != nil {
			extractTS(d, src, fg, true)
		}
		// `export { a } from './b'` / `export * from './b'`
		if s := fieldText(n, "source", src); s != "" {
			fg.Imports = append(fg.Imports, graph.Import{Spec: unquote(s)})
		}
	case "function_declaration", "generator_function_declaration":
		addSym(fg, n, src, "function", fieldText(n, "name", src), exported)
	case "class_declaration", "abstract_class_declaration":
		addSym(fg, n, src, "class", fieldText(n, "name", src), exported)
	case "interface_declaration":
		addSym(fg, n, src, "interface", fieldText(n, "name", src), exported)
	case "type_alias_declaration":
		addSym(fg, n, src, "type", fieldText(n, "name", src), exported)
	case "enum_declaration":
		addSym(fg, n, src, "enum", fieldText(n, "name", src), exported)
	case "lexical_declaration", "variable_declaration":
		for i := 0; i < int(n.NamedChildCount()); i++ {
			d := n.NamedChild(i)
			if d.Type() != "variable_declarator" {
				continue
			}
			kind := "variable"
			if v := d.ChildByFieldName("value"); v != nil {
				switch v.Type() {
				case "arrow_function", "function", "function_expression", "generator_function":
					kind = "function"
				}
			}
			addSym(fg, d, src, kind, fieldText(d, "name", src), exported)
		}
	}
}

// ---- Python --------------------------------------------------------------

func pyExported(name string) bool { return name != "" && !strings.HasPrefix(name, "_") }

func extractPy(n *sitter.Node, src []byte, fg *graph.FileGraph) {
	switch n.Type() {
	case "import_statement":
		for i := 0; i < int(n.NamedChildCount()); i++ {
			c := n.NamedChild(i)
			switch c.Type() {
			case "dotted_name":
				fg.Imports = append(fg.Imports, graph.Import{Spec: c.Content(src)})
			case "aliased_import":
				if nm := fieldText(c, "name", src); nm != "" {
					fg.Imports = append(fg.Imports, graph.Import{Spec: nm})
				}
			}
		}
	case "import_from_statement":
		if m := fieldText(n, "module_name", src); m != "" {
			fg.Imports = append(fg.Imports, graph.Import{Spec: m})
		}
	case "function_definition":
		name := fieldText(n, "name", src)
		addSym(fg, n, src, "function", name, pyExported(name))
	case "class_definition":
		name := fieldText(n, "name", src)
		addSym(fg, n, src, "class", name, pyExported(name))
	case "decorated_definition":
		if d := n.ChildByFieldName("definition"); d != nil {
			extractPy(d, src, fg)
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
