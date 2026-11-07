package treesitter

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	gts "github.com/odvcencio/gotreesitter"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver/imports"
)

// Ruby usage. Rails code (Zeitwerk) names other files' code by constant, not
// by require, so a constant reference is the use:
//
//   - User, Admin::Role, ::Billing (in their lexical nesting), superclasses
//     (in the nesting outside the class), include / extend Concern
//   - associations: has_many :comments -> Comment (singularized),
//     belongs_to :owner -> Owner, unless class_name: or polymorphic: say
//     otherwise; validates :x, email: true -> EmailValidator
//   - strings: "Admin::User" (constantize, class_name:), and
//     "Importers::#{x}" -> every constant under Importers
//   - require / require_relative / load / autoload: the file itself
//
// A constant use is a graph.Use with Target graph.RubyConstPrefix + the nesting
// (innermost first, space-separated) and Name the constant as written: the
// rule looks it up the way Ruby does (Admin::UsersController::User,
// Admin::User, User) among the constants the project declares. Constants the
// import resolver places in a file of the repository (Zeitwerk's convention)
// are dependency edges (imports) too.

// rbRef is one constant reference: written in levels (innermost first).
type rbRef struct {
	levels  []string
	written string
}

// candidates are the full names written may stand for, innermost first.
func (r rbRef) candidates() []string {
	if a, ok := strings.CutPrefix(r.written, "::"); ok {
		return []string{a}
	}
	if strings.HasSuffix(r.written, "::*") || strings.HasPrefix(r.written, "*") {
		return nil
	}
	out := make([]string, 0, len(r.levels)+1)
	for _, l := range r.levels {
		out = append(out, l+"::"+r.written)
	}
	return append(out, r.written)
}

func (u *usageCollector) rbConst(levels []string, written string) {
	if written == "" {
		return
	}
	u.rbRefs = append(u.rbRefs, rbRef{levels: levels, written: written})
	u.direct = append(u.direct, graph.Use{Target: graph.RubyConstPrefix + strings.Join(levels, " "), Name: written})
}

// rbRequire records a required file (spec as the Ruby resolver takes it).
func (u *usageCollector) rbRequire(spec string) {
	if spec == "" {
		return
	}
	u.x.fg.Imports = append(u.x.fg.Imports, graph.Import{Spec: spec})
	u.use(spec, "")
}

var (
	rbConstRe       = regexp.MustCompile(`^(::)?[A-Z]\w*(::[A-Z]\w*)*$`)
	rbConstPrefixRe = regexp.MustCompile(`^(::)?([A-Z]\w*::)+$`)
	rbParentConstRe = regexp.MustCompile(`^"#\{[^}]*module_parent(_name)?[^}]*\}::`)
	// the base of an application's models: subclassed, never named elsewhere
	// in a new application
	rbAbstractRe  = regexp.MustCompile(`(?m)^\s*(primary_abstract_class|self\.abstract_class\s*=\s*true)\b`)
	rbAppRecordRe = regexp.MustCompile(`(?m)^\s*class\s+(?:[\w:]*::)?ApplicationRecord\b`)
	rbMainGuardRe = regexp.MustCompile(`(?m)^\s*(if|unless)\s+(__FILE__\s*==\s*\$(0|PROGRAM_NAME)|\$(0|PROGRAM_NAME)\s*==\s*__FILE__)`)
)

var rbAssociations = map[string]bool{ // true: the name is plural
	"has_many": true, "has_and_belongs_to_many": true, "embeds_many": true,
	"has_one": false, "belongs_to": false, "embeds_one": false, "embedded_in": false,
}

// rbBuiltinValidators are the validates options that are not a validator class.
var rbBuiltinValidators = map[string]bool{
	"presence": true, "uniqueness": true, "length": true, "format": true, "inclusion": true,
	"exclusion": true, "numericality": true, "acceptance": true, "confirmation": true,
	"absence": true, "comparison": true, "allow_nil": true, "allow_blank": true, "if": true,
	"unless": true, "on": true, "message": true, "strict": true, "size": true,
}

// rbSubclassRegistered: superclasses whose subclasses Rails or a tool loads
// by themselves.
func rbSubclassRegistered(super string) bool {
	s := strings.TrimPrefix(super, "::")
	switch s {
	case "Rails::Railtie", "Rails::Engine", "Rails::Application", "Thor":
		return true
	}
	return strings.HasPrefix(s, "Rails::Generators::")
}

func (u *usageCollector) rbFile(root *gts.Node) {
	if rbMainGuardRe.Match(u.x.src) {
		u.mainGuard = true
	}
	if strings.HasPrefix(string(u.x.src), "#!") {
		u.shebang = true
	}
	if rbAbstractRe.Match(u.x.src) && rbAppRecordRe.Match(u.x.src) {
		u.rbEntry = "the application's abstract model base class"
	}
	u.rbVisit(root, nil)
}

func (u *usageCollector) rbVisit(n *gts.Node, levels []string) {
	x := u.x
	switch x.typ(n) {
	case "comment":
		return
	case "class", "module":
		outer := ""
		if len(levels) > 0 {
			outer = levels[0]
		}
		fq := rbQualify(outer, x.fieldText(n, "name"))
		if sc := x.field(n, "superclass"); sc != nil {
			u.rbVisitChildren(sc, levels)
			super := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(x.text(sc)), "<"))
			switch {
			case rbSubclassRegistered(super):
				u.keep = append(u.keep, fq)
				u.rbEntry = "registered by subclassing " + super
			case u.rbSTI(super):
				u.keep = append(u.keep, fq)
				u.rbEntry = "STI subclass (loaded from the type column)"
			}
		}
		body := x.field(n, "body")
		if x.typ(n) == "module" && strings.HasSuffix("::"+fq, "::ClassMethods") && len(levels) > 0 {
			u.keep = append(u.keep, fq) // extended by ActiveSupport::Concern
		}
		if x.rbRegistersSelf(body) { // a class or module that registers or prepends itself
			u.keep = append(u.keep, fq)
		}
		u.rbVisitChildren(body, append([]string{fq}, levels...))
		return
	case "assignment":
		if l := x.field(n, "left"); l != nil && x.typ(l) == "constant" {
			u.rbVisitChildren(x.field(n, "right"), levels)
			return
		}
	case "scope_resolution":
		if c, ok := x.rbConstPath(n); ok {
			u.rbConst(levels, c)
			return
		}
		if s := x.field(n, "scope"); s != nil {
			u.rbVisit(s, levels)
		}
		return
	case "constant":
		u.rbConst(levels, x.text(n))
		return
	case "bare_string": // %w[CsvImporter JsonImporter], delegated_type types:
		literal := true
		for i := 0; i < n.NamedChildCount(); i++ {
			literal = literal && x.typ(n.NamedChild(i)) == "string_content"
		}
		if literal {
			if s := x.text(n); rbConstRe.MatchString(s) {
				u.rbConst(nil, s)
			}
			return
		}
	case "simple_symbol": // class_name: :Note, const_get(:Thing)
		if s := strings.TrimPrefix(x.text(n), ":"); rbConstRe.MatchString(s) {
			u.rbConst(nil, s)
		}
		return
	case "string":
		if s, ok := x.stringContent(n); ok {
			if rbConstRe.MatchString(s) && len(s) < 256 {
				u.rbConst(nil, s)
			}
			u.rbGlob(s)
			return
		}
		if p := x.staticPrefix(n); rbConstPrefixRe.MatchString(p) {
			u.rbConst(nil, p+"*")
		}
		// "#{module_parent_name}::#{kind}".constantize: a constant of the
		// enclosing namespace
		if rbParentConstRe.MatchString(x.text(n)) && len(levels) > 0 {
			if i := strings.LastIndex(levels[0], "::"); i > 0 {
				u.rbConst(nil, levels[0][:i]+"::*")
			}
		}
		u.rbGlob(x.rbStringTail(n))  // "#{Rails.root}/lib/patches/*.rb"
		u.rbVisitChildren(n, levels) // interpolations
		return
	case "call":
		if u.rbCall(n, levels) {
			return
		}
	}
	u.rbVisitChildren(n, levels)
}

func (u *usageCollector) rbVisitChildren(n *gts.Node, levels []string) {
	if n == nil {
		return
	}
	for i := 0; i < n.NamedChildCount(); i++ {
		u.rbVisit(n.NamedChild(i), levels)
	}
}

// rbRegistersSelf reports whether a class or module body hands itself to a
// call (HTTP::Options.register_feature(:x, self), Object.prepend(self)):
// a registry or the patched class uses it.
func (x *extractor) rbRegistersSelf(body *gts.Node) bool {
	if body == nil {
		return false
	}
	for i := 0; i < body.NamedChildCount(); i++ {
		c := body.NamedChild(i)
		if x.typ(c) != "call" {
			continue
		}
		for _, a := range x.rbArgs(c) {
			if x.typ(a) == "self" {
				return true
			}
		}
	}
	return false
}

// rbStringTail is the literal text after the last interpolation of a string.
func (x *extractor) rbStringTail(n *gts.Node) string {
	var b strings.Builder
	for i := 0; i < n.NamedChildCount(); i++ {
		switch c := n.NamedChild(i); x.typ(c) {
		case "string_content":
			b.WriteString(x.text(c))
		case "interpolation":
			b.Reset()
		}
	}
	return b.String()
}

// rbGlob records the directory of a file glob (Dir["lib/patches/*.rb"],
// Dir.glob("#{__dir__}/handlers/*.rb")): every file under it is loaded.
// The directory is taken from the repository root and from the file's
// directory.
func (u *usageCollector) rbGlob(s string) {
	i := strings.IndexByte(s, '*')
	// only a glob of Ruby files is loaded: "components/*" in a path
	// configuration names directories, not code
	if i < 0 || len(s) > 256 || !strings.HasSuffix(s, ".rb") {
		return
	}
	dir := path.Dir(strings.TrimPrefix(s[:i], "/") + "x")
	if dir == "." {
		return // the whole repository or directory: too broad to mean anything
	}
	for _, d := range []string{path.Clean(dir), path.Join(path.Dir(u.rel), dir)} {
		if d != "." && d != ".." && !strings.HasPrefix(d, "../") {
			u.tsPrefixes = append(u.tsPrefixes, d)
		}
	}
}

// rbConstPath is the text of a constant path made of constants only
// ("A::B", "::A"); ok is false for self.class::X and the like.
func (x *extractor) rbConstPath(n *gts.Node) (string, bool) {
	switch x.typ(n) {
	case "constant":
		return x.text(n), true
	case "scope_resolution":
		name := x.field(n, "name")
		if name == nil || x.typ(name) != "constant" {
			return "", false
		}
		s := x.field(n, "scope")
		if s == nil {
			return "::" + x.text(name), true
		}
		p, ok := x.rbConstPath(s)
		if !ok {
			return "", false
		}
		return p + "::" + x.text(name), true
	}
	return "", false
}

// rbArgs are the arguments of call n.
func (x *extractor) rbArgs(n *gts.Node) []*gts.Node {
	a := x.field(n, "arguments")
	if a == nil {
		return nil
	}
	out := make([]*gts.Node, 0, a.NamedChildCount())
	for i := 0; i < a.NamedChildCount(); i++ {
		out = append(out, a.NamedChild(i))
	}
	return out
}

// rbSymbolName is the name of a :symbol ("" for anything else).
func (x *extractor) rbSymbolName(n *gts.Node) string {
	switch x.typ(n) {
	case "simple_symbol":
		return strings.TrimPrefix(x.text(n), ":")
	case "hash_key_symbol":
		return x.text(n)
	}
	return ""
}

// rbCall handles the calls that name code; it returns whether n is done.
func (u *usageCollector) rbCall(n *gts.Node, levels []string) bool {
	x := u.x
	if x.field(n, "receiver") != nil {
		return false
	}
	m := x.fieldText(n, "method")
	args := x.rbArgs(n)
	switch m {
	case "require", "require_relative", "load", "require_dependency":
		if len(args) == 0 {
			return false
		}
		s, ok := x.stringContent(args[0])
		if !ok {
			return false
		}
		if m == "require_relative" && !strings.HasPrefix(s, "./") && !strings.HasPrefix(s, "../") {
			s = "./" + s
		}
		if s == "bundler/inline" {
			u.rbEntry = "script (bundler/inline)" // brings its own gems: run by hand
		}
		u.rbRequire(s)
		return true
	case "autoload":
		if len(args) > 1 {
			if s, ok := x.stringContent(args[1]); ok {
				u.rbRequire(s)
			}
		}
		return false
	case "validates", "validates_each":
		for _, a := range args {
			if x.typ(a) != "pair" {
				continue
			}
			k := x.rbSymbolName(x.field(a, "key"))
			if k != "" && !rbBuiltinValidators[k] {
				u.rbConst(levels, rbCamelize(k)+"Validator")
			}
		}
		return false
	}
	plural, ok := rbAssociations[m]
	if !ok || len(args) == 0 {
		return false
	}
	name := x.rbSymbolName(args[0])
	if name == "" {
		// has_one :"#{name.underscore}_search_data": every class whose name
		// ends with SearchData
		if x.typ(args[0]) == "delimited_symbol" {
			if tail := strings.Trim(x.rbStringTail(args[0]), "_"); tail != "" && !strings.ContainsAny(tail, "\"' ") {
				if plural {
					tail = rbSingularize(tail)
				}
				u.rbConst(levels, "*"+rbCamelize(tail))
			}
		}
		return false
	}
	named := false
	for _, a := range args[1:] {
		if x.typ(a) != "pair" {
			continue
		}
		switch x.rbSymbolName(x.field(a, "key")) {
		case "class_name", "polymorphic", "as_class":
			named = true
		}
	}
	if !named {
		if plural {
			name = rbSingularize(name)
		}
		u.rbConst(levels, rbCamelize(name))
	}
	return false
}

// rbSTI reports whether a class of a models/ directory inheriting from
// super may be a single-table-inheritance subclass, which Rails loads by
// the name stored in the type column.
func (u *usageCollector) rbSTI(super string) bool {
	if !hasSeg(strings.Split(path.Dir(u.rel), "/"), map[string]bool{"models": true}) {
		return false
	}
	if !rbConstRe.MatchString(super) {
		return false
	}
	last := super[strings.LastIndex(super, ":")+1:]
	return last != "ApplicationRecord" && last != "Base" && !strings.HasSuffix(last, "Record")
}

// ---- templates -------------------------------------------------------------

var (
	erbCodeRe     = regexp.MustCompile(`(?s)<%(?:[=-]{0,2})(.*?)-?%>`)
	rbInterpRe    = regexp.MustCompile(`#\{([^}]*)\}`)
	hamlCodeRe    = regexp.MustCompile(`^\s*(?:[=~-]|!=|&=|==)(.*)$`)
	hamlTagCodeRe = regexp.MustCompile(`^\s*[%.#]?[\w.#:-]+(?:\([^)]*\)|\{[^}]*\}|\[[^\]]*\])*\s*(?:==|!=|&=|=|~)(.*)$`)
	// a Haml / Slim tag line (%a{...}, .x(...), a href=...) and what follows the tag
	tagLineRe    = regexp.MustCompile(`^\s*(?:[%.#][\w.#-]*|[a-z][\w-]*(?:[.#][\w-]+)*)((?:\{[^}]*\}|\([^)]*\)|\[[^\]]*\]|\s+[\w-]+=\S+)+)`)
	attrGroupRe  = regexp.MustCompile(`\{[^}]*\}|\([^)]*\)|\[[^\]]*\]`)
	slimAttrRe   = regexp.MustCompile(`(?:^|\s)[\w-]+=(\S+)`)
	rbStringRe   = regexp.MustCompile(`"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*'`)
	rbTemplConst = regexp.MustCompile(`(?:^|[^\w:.@$])((?:::)?[A-Z]\w*(?:::[A-Z]\w*)*)`)
)

// rbTemplateCode is the Ruby code of an ERB / Haml / Slim template.
func rbTemplateCode(rel string, src []byte) []string {
	var code []string
	if strings.HasSuffix(strings.ToLower(rel), ".erb") {
		for _, m := range erbCodeRe.FindAllSubmatch(src, -1) {
			if c := string(m[1]); !strings.HasPrefix(c, "#") {
				code = append(code, c)
			}
		}
		return code
	}
	for line := range strings.SplitSeq(string(src), "\n") {
		if m := hamlCodeRe.FindStringSubmatch(line); m != nil {
			code = append(code, m[1])
		} else if m := hamlTagCodeRe.FindStringSubmatch(line); m != nil {
			code = append(code, m[1])
		}
		if m := tagLineRe.FindStringSubmatch(line); m != nil {
			// attributes are code: %a{href: X.url}, %a(href=X.url), a href=X.url
			code = append(code, attrGroupRe.FindAllString(m[1], -1)...)
			for _, a := range slimAttrRe.FindAllStringSubmatch(m[1], -1) {
				code = append(code, a[1])
			}
		}
		for _, m := range rbInterpRe.FindAllStringSubmatch(line, -1) {
			code = append(code, m[1])
		}
	}
	return code
}

// resolveRubyTemplate is ResolveFile for a view template: the constants its
// code names are its uses; it declares nothing and is an entry (rendered by
// the controllers).
func (r *Resolver) resolveRubyTemplate(ctx context.Context, root, rel string) (*graph.FileGraph, error) {
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, err
	}
	fg := &graph.FileGraph{
		Path: filepath.ToSlash(rel), Package: graph.PackageKey(rel), Language: graph.Ruby,
		Lines: strings.Count(string(src), "\n") + 1,
	}
	x := &extractor{src: src, fg: fg}
	u := &usageCollector{x: x, rel: fg.Path, lang: graph.Ruby, template: true}
	for _, c := range rbTemplateCode(rel, src) {
		c = rbStringRe.ReplaceAllString(c, `""`)
		for _, m := range rbTemplConst.FindAllStringSubmatch(c, -1) {
			u.rbConst(nil, m[1])
		}
	}
	reqs := u.requests()
	r.finishUsage(ctx, root, fg, u, reqs, r.resolveImports(ctx, root, fg, reqs))
	return fg, nil
}

// ---- imports of constants --------------------------------------------------

// rbRequests are the constant lookups to resolve (absolute "::A::B" specs).
func (u *usageCollector) rbRequests(add func(string)) {
	for _, ref := range u.rbRefs {
		for _, c := range ref.candidates() {
			add("::" + c)
		}
	}
}

// rbConstImports are the dependency edges of the constant references: for
// each, the first candidate the resolver places in another file of the
// repository. A constant the file declares itself is no edge.
func (u *usageCollector) rbConstImports(bySpec map[string]imports.Result) []graph.Import {
	own := map[string]bool{}
	for _, s := range u.x.fg.Symbols {
		own[s.Name] = true
	}
	seen := map[string]bool{}
	var out []graph.Import
	for _, ref := range u.rbRefs {
		for _, c := range ref.candidates() {
			if own[c] {
				break
			}
			res, ok := bySpec["::"+c]
			if !ok || res.Kind != imports.InRepo {
				continue
			}
			if res.File != u.rel && !seen[ref.written+"\x00"+res.Target] {
				seen[ref.written+"\x00"+res.Target] = true
				out = append(out, graph.Import{Spec: ref.written, Target: res.Target})
			}
			break
		}
	}
	return out
}

// ---- inflection ------------------------------------------------------------

// rbCamelize is ActiveSupport's camelize ("user_profile" -> "UserProfile",
// "admin/user" -> "Admin::User").
func rbCamelize(s string) string {
	var b strings.Builder
	for i, part := range strings.Split(s, "/") {
		if i > 0 {
			b.WriteString("::")
		}
		for w := range strings.SplitSeq(part, "_") {
			if w != "" {
				b.WriteString(strings.ToUpper(w[:1]) + w[1:])
			}
		}
	}
	return b.String()
}

var rbIrregular = map[string]string{
	"people": "person", "men": "man", "women": "woman", "children": "child", "sexes": "sex",
	"moves": "move", "zombies": "zombie", "mice": "mouse", "geese": "goose", "teeth": "tooth", "feet": "foot",
}

var rbUncountable = map[string]bool{
	"equipment": true, "information": true, "rice": true, "money": true, "species": true,
	"series": true, "fish": true, "sheep": true, "jeans": true, "police": true, "news": true, "data": true,
}

var rbSingularRules = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`(database)s$`), "$1"},
	{regexp.MustCompile(`(quiz)zes$`), "$1"},
	{regexp.MustCompile(`(matr)ices$`), "${1}ix"},
	{regexp.MustCompile(`(vert|ind)ices$`), "${1}ex"},
	{regexp.MustCompile(`^(ox)en`), "$1"},
	{regexp.MustCompile(`(alias|status)(es)?$`), "$1"},
	{regexp.MustCompile(`(octop|vir)(us|i)$`), "${1}us"},
	{regexp.MustCompile(`^(a)x[ie]s$`), "${1}xis"},
	{regexp.MustCompile(`(cris|test)(is|es)$`), "${1}is"},
	{regexp.MustCompile(`(shoe)s$`), "$1"},
	{regexp.MustCompile(`(o)es$`), "$1"},
	{regexp.MustCompile(`(bus)(es)?$`), "$1"},
	{regexp.MustCompile(`(m|l)ice$`), "${1}ouse"},
	{regexp.MustCompile(`(x|ch|ss|sh)es$`), "$1"},
	{regexp.MustCompile(`(m)ovies$`), "${1}ovie"},
	{regexp.MustCompile(`(s)eries$`), "${1}eries"},
	{regexp.MustCompile(`([^aeiouy]|qu)ies$`), "${1}y"},
	{regexp.MustCompile(`([lr])ves$`), "${1}f"},
	{regexp.MustCompile(`(tive)s$`), "$1"},
	{regexp.MustCompile(`(hive)s$`), "$1"},
	{regexp.MustCompile(`([^f])ves$`), "${1}fe"},
	{regexp.MustCompile(`(^analy)(sis|ses)$`), "${1}sis"},
	{regexp.MustCompile(`((a)naly|(b)a|(d)iagno|(p)arenthe|(p)rogno|(s)ynop|(t)he)(sis|ses)$`), "${1}sis"},
	{regexp.MustCompile(`([ti])a$`), "${1}um"},
	{regexp.MustCompile(`(n)ews$`), "${1}ews"},
	{regexp.MustCompile(`(ss)$`), "$1"},
	{regexp.MustCompile(`s$`), ""},
}

// rbSingularize is ActiveSupport's singularize of the last word of s
// (default English rules).
func rbSingularize(s string) string {
	i := strings.LastIndex(s, "_")
	head, word := s[:i+1], s[i+1:]
	if rbUncountable[word] {
		return s
	}
	if w, ok := rbIrregular[word]; ok {
		return head + w
	}
	for _, r := range rbSingularRules {
		if r.re.MatchString(word) {
			return head + r.re.ReplaceAllString(word, r.repl)
		}
	}
	return s
}
