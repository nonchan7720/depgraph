package treesitter

import (
	"encoding/json"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver/imports"
)

// Ruby entry points. Rails loads much of an application by convention, not
// through constants other code names: controllers (routes), jobs, mailers,
// channels, helpers (every view), mailboxes, views, and the classes tools
// infer from a model's name (policies, serializers, decorators, dashboards,
// form inputs, ActiveAdmin resources). Concerns are not entries: they are
// used by including them, wherever they live. config/, db/, bin/, lib/tasks,
// tests, Rakefile, config.ru and scripts are run.
//
// A gem's lib/ is its public API. A packwerk pack's public path (package.yml
// public_path, app/public by default) is the API of the pack: reachable,
// but packs are used by the application only, so what nothing uses of it
// is still reported.

// rbPackPublic is the entry reason of a packwerk pack's public API.
const rbPackPublic = "packwerk public API"

// rbEntryAppDirs: directories under app/ Rails or a tool loads by convention.
var rbEntryAppDirs = map[string]bool{
	"controllers": true, "jobs": true, "mailers": true, "channels": true, "helpers": true,
	"mailboxes": true, "views": true, "workers": true, "sidekiq": true, "policies": true,
	"serializers": true, "decorators": true, "admin": true, "dashboards": true, "inputs": true,
	"previews": true, "avo": true, "madmin": true,
}

// rbEntryDirs: top-level directories of an application / gem whose files are run.
var rbEntryDirs = map[string]string{
	"config": "Rails convention (config/)", "db": "Rails convention (db/)", "bin": "script / tool directory",
	"exe": "script / tool directory", "script": "script / tool directory", "scripts": "script / tool directory",
	"tools": "script / tool directory", "hack": "script / tool directory", ".github": "script / tool directory",
	"spec": "test file", "test": "test file", "features": "test file",
	"sorbet": "Sorbet / Tapioca configuration",
}

var rbTestDirs = map[string]bool{"spec": true, "test": true, "features": true}

// packPublicPath is the public path of the packwerk pack whose package.yml is rel.
func packPublicPath(root, rel string) string {
	pub := "app/public"
	if b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
		var y struct {
			PublicPath string `yaml:"public_path"`
		}
		if yaml.Unmarshal(b, &y) == nil && strings.Trim(y.PublicPath, "./") != "" {
			pub = path.Clean(strings.TrimPrefix(y.PublicPath, "./"))
		}
	}
	return pub
}

// rbBase is the directory of the application, gem, engine or pack rel
// belongs to (the deepest with a Gemfile, *.gemspec or package.yml, or
// whose app/ has Rails' layout).
func (m *manifests) rbBase(rel string) string {
	for d := path.Dir(rel); ; d = path.Dir(d) {
		if _, ok := m.rbPacks[d]; ok || m.rbScopes[d] || m.rbApps[d] {
			return d
		}
		if d == "." || d == "/" || d == "" {
			return "."
		}
	}
}

func (m *manifests) rbEntry(rel string, u *usageCollector) string {
	base := path.Base(rel)
	switch {
	case u.template:
		return "view template"
	case base == "Rakefile" || base == "config.ru" || strings.HasSuffix(base, ".rake"):
		return "Rake / Rack file"
	case strings.HasSuffix(base, "_spec.rb") || strings.HasSuffix(base, "_test.rb"):
		return "test file"
	case u.mainGuard:
		return "if __FILE__ == $0"
	case u.shebang:
		return "script (#!)"
	case strings.HasPrefix(base, "."):
		return "tool configuration (" + base + ")" // .active_record_doctor.rb, .pryrc.rb
	case strings.HasPrefix(rel, ".") || strings.Contains(rel, "/."):
		return "tool directory" // .skills/, .devcontainer/, .github/
	case base == "plugin.rb" && m.rbApps[path.Dir(rel)]:
		return "Discourse plugin"
	}
	if u.rbEntry != "" {
		return u.rbEntry
	}
	if why := m.rbLibrary(u); why != "" {
		return why
	}
	for _, s := range u.x.fg.Symbols {
		if s.Kind == "class" || s.Kind == "module" {
			if f, ok := m.rbYAML[s.Name]; ok {
				return "named in " + f
			}
		}
	}
	b := m.rbBase(rel)
	in := rel
	if b != "." {
		in = strings.TrimPrefix(rel, b+"/")
	}
	if pub, ok := m.rbPacks[b]; ok && strings.HasPrefix(in, pub+"/") {
		return rbPackPublic
	}
	segs := strings.Split(path.Dir(in), "/")
	if why, ok := rbEntryDirs[segs[0]]; ok {
		return why
	}
	if hasSeg(strings.Split(path.Dir(rel), "/"), rbTestDirs) {
		return "test file"
	}
	switch segs[0] {
	case "lib":
		if m.rbGems[b] {
			return "gem library"
		}
		if len(segs) > 1 && (segs[1] == "tasks" || segs[1] == "generators" || segs[1] == "templates") {
			return "Rails convention (lib/" + segs[1] + ")"
		}
	case "app":
		if len(segs) > 1 && rbEntryAppDirs[segs[1]] && !hasSeg(segs, map[string]bool{"concerns": true}) {
			return "Rails convention (app/" + segs[1] + ")"
		}
	}
	return ""
}

// ---- libraries -------------------------------------------------------------

// rbCoreRoots are the top-level constants of Ruby itself, its standard
// library and Rails: a class or module of the project under one of them
// reopens the library's (a core extension, a monkey patch).
func rbCoreRoots() map[string]bool {
	m := map[string]bool{}
	for n := range strings.FieldsSeq(`Object BasicObject Kernel Module Class Comparable Enumerable
		String Symbol Integer Float Numeric Rational Complex Array Hash Range Set Struct Proc Method
		NilClass TrueClass FalseClass Time Date DateTime IO File Dir Exception StandardError Regexp
		Process Thread Fiber Marshal Math ObjectSpace Signal Warning Data Encoding Random
		URI Net JSON YAML Psych CSV ERB Logger OpenSSL Digest Base64 SecureRandom Pathname Tempfile
		OpenStruct Forwardable Singleton Benchmark Timeout Zlib StringIO Socket Ripper Coverage Gem Bundler
		Rails ActiveRecord ActiveSupport ActiveModel ActiveJob ActiveStorage ActionController ActionView
		ActionDispatch ActionMailer ActionCable ActionText ActionMailbox ActiveResource Arel Rack Rake
		Railties Sprockets Zeitwerk Concurrent Minitest RSpec Capybara FactoryBot`) {
		m[rbLibKey(n)] = true
	}
	return m
}

// rbLibKey compares a root constant with a gem name whatever their case and
// separators: the omniauth gem is OmniAuth, rubocop RuboCop, jsonapi JSONAPI.
func rbLibKey(s string) string {
	return strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(s))
}

var (
	gemLineRe     = regexp.MustCompile(`(?m)^\s*gem\s+["']([\w.-]+)["']([^\n]*)$`)
	gemLockSpecRe = regexp.MustCompile(`^    ([\w.-]+) \(`)
)

// loadGemNames records the root constants of the gems a Gemfile or its lock
// file names: the whole name (omniauth -> OmniAuth, kt-paperclip ->
// KtPaperclip) and the name after a short fork prefix (Paperclip) for sure,
// the first part of a hyphenated name (twitter-text -> Twitter) as a guess.
// Gems of the repository itself (path:) are not libraries.
func (m *manifests) loadGemNames(root, rel string) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return
	}
	var names []string
	if strings.HasSuffix(rel, "Gemfile") {
		for _, sm := range gemLineRe.FindAllStringSubmatch(string(b), -1) {
			if !strings.Contains(sm[2], "path:") && !strings.Contains(sm[2], ":path") {
				names = append(names, sm[1])
			}
		}
	} else {
		section := ""
		for line := range strings.SplitSeq(string(b), "\n") {
			if line != "" && line[0] != ' ' {
				section = strings.TrimSpace(line)
			}
			if sm := gemLockSpecRe.FindStringSubmatch(line); sm != nil && section != "PATH" {
				names = append(names, sm[1])
			}
		}
	}
	for _, n := range names {
		m.rbLibs[rbLibKey(n)] = true
		parts := strings.Split(n, "-")
		if len(parts) == 1 {
			continue
		}
		// a fork under a short prefix: kt-paperclip is Paperclip
		if len(parts[0]) <= 3 {
			m.rbLibs[rbLibKey(parts[1])] = true
		}
		// twitter-text is Twitter, but api-pagination is not every Api: only
		// a guess, which the application's own namespace overrides
		if k := rbLibKey(parts[0]); !m.rbLibs[k] {
			m.rbLibs[k] = false
		}
	}
}

// rbLibrary is why a Ruby file is loaded by a library ("" when it is not):
// every class and module it declares reopens a library's namespace
// (Paperclip processors, Chewy strategies, core extensions), which the
// library looks up by name. Such declarations in other files are kept.
func (m *manifests) rbLibrary(u *usageCollector) string {
	all, lib := 0, ""
	for _, s := range u.x.fg.Symbols {
		if s.Kind != "class" && s.Kind != "module" && s.Kind != "constant" {
			continue
		}
		all++
		r, _, _ := strings.Cut(s.Name, "::")
		if m.rbLibRoot(r) {
			u.keep = append(u.keep, s.Name)
			if lib == "" {
				lib = r
			}
		} else {
			all = -1 << 30
		}
	}
	if all > 0 && lib != "" {
		return "extends " + lib + " (a library's namespace)"
	}
	return ""
}

// rbLibRoot reports whether the root constant r is a library's: a guessed
// root (twitter-text -> Twitter) is not when the application has a directory
// of that name (app/services/api/ for Api).
func (m *manifests) rbLibRoot(r string) bool {
	k := rbLibKey(r)
	certain, ok := m.rbLibs[k]
	if !ok {
		return false
	}
	if certain {
		return true
	}
	m.rbOwnOnce.Do(func() {
		m.rbOwn = map[string]bool{}
		for _, f := range m.sources {
			if !strings.HasSuffix(f, ".rb") {
				continue
			}
			segs := strings.Split(path.Dir(f), "/")
			for _, s := range segs[min(len(segs), 2):] { // below app/<kind>/, lib/<x>/
				m.rbOwn[rbLibKey(s)] = true
			}
		}
	})
	return !m.rbOwn[k]
}

// ---- tool configuration ----------------------------------------------------

// rbToolConfig: configuration of Ruby tools that load project files by path
// (RuboCop, Standard, haml-lint, erb_lint, Reek).
func rbToolConfig(rel string) bool {
	base := path.Base(rel)
	if !strings.HasSuffix(base, ".yml") && !strings.HasSuffix(base, ".yaml") {
		return false
	}
	// a dotfile: .rubocop.yml, .haml-lint.yml, .standard.yml and the files
	// they extend (.custom_cops.yml), ...
	return strings.HasPrefix(base, ".") || strings.HasPrefix(rel, ".rubocop/") || strings.Contains(rel, "/.rubocop/")
}

// a list item ("- ./lib/cop.rb") or a scalar ("require: ./lib/cop.rb")
var yamlListPathRe = regexp.MustCompile(`(?m)^\s*(?:-|[\w-]+:)\s*["']?(\.{1,2}/[\w./-]+)`)

// loadRubyRequires: the files a tool configuration requires
// ("require: [./lib/linter/cop.rb]", relative to the configuration file).
func (m *manifests) loadRubyRequires(root, rel string) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return
	}
	for _, sm := range yamlListPathRe.FindAllStringSubmatch(string(b), -1) {
		p := path.Clean(path.Join(path.Dir(rel), sm[1]))
		if p == ".." || strings.HasPrefix(p, "../") {
			continue // outside the repository
		}
		for _, f := range []string{p, p + ".rb"} {
			if strings.HasSuffix(f, ".rb") && isFile(root, f) {
				m.addFile(f, "required by "+rel)
			}
		}
	}
}

// ---- JavaScript of a Rails application -------------------------------------

var (
	importmapPinRe    = regexp.MustCompile(`(?m)^\s*pin\s+["']([^"']+)["']([^\n]*)`)
	importmapToRe     = regexp.MustCompile(`to:\s*["']([^"']+)["']`)
	importmapPinAllRe = regexp.MustCompile(`(?m)^\s*pin_all_from\s+["']([^"']+)["']`)
)

// loadRailsJS: where the JavaScript of the Rails application at dir starts.
//
//   - Sprockets: everything under app/assets/ (//= require, not imports)
//   - jsbundling-rails / importmap: the files directly in app/javascript/
//   - importmap: config/importmap.rb pins and pin_all_from directories
//   - Vite Ruby: config/vite.json sourceCodeDir/entrypointsDir; without it
//     app/javascript/entrypoints and app/frontend/entrypoints
//   - Shakapacker / Webpacker: config/shakapacker.yml / webpacker.yml
//     source_path/source_entry_path; without it app/javascript/packs
func (m *manifests) loadRailsJS(root, dir string) {
	at := func(p string) string { return path.Clean(path.Join(dir, p)) }
	prefix := func(p, why string) {
		// a directory of the application, never the repository itself or
		// outside it ("../.." in a configuration file)
		d := at(p)
		if d == "." || d == dir || d == ".." || strings.HasPrefix(d, "../") || !isDir(root, d) {
			return
		}
		m.prefixes[d+"/"] = why
	}
	prefix("app/assets", "Sprockets asset")
	if es, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(at("app/javascript")))); err == nil {
		for _, e := range es {
			if f := at("app/javascript/" + e.Name()); !e.IsDir() && graphLanguage(f) != "" {
				m.addFile(f, "Rails JavaScript entry (app/javascript)")
			}
		}
	}
	if b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(at("config/importmap.rb")))); err == nil {
		for _, sm := range importmapPinRe.FindAllStringSubmatch(string(b), -1) {
			file := sm[1]
			if to := importmapToRe.FindStringSubmatch(sm[2]); to != nil {
				file = to[1]
			}
			if strings.Contains(file, "://") {
				continue
			}
			for _, f := range tsCandidates(root, at("app/javascript"), file, nil) {
				m.addFile(f, "pinned by config/importmap.rb")
			}
		}
		for _, sm := range importmapPinAllRe.FindAllStringSubmatch(string(b), -1) {
			prefix(sm[1], "pinned by config/importmap.rb")
		}
	}
	if b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(at("config/vite.json")))); err == nil {
		var cfg map[string]struct {
			SourceCodeDir  string `json:"sourceCodeDir"`
			EntrypointsDir string `json:"entrypointsDir"`
		}
		src, entries := "app/frontend", "entrypoints"
		if json.Unmarshal(b, &cfg) == nil {
			for _, env := range []string{"all", "development", "production"} {
				if c, ok := cfg[env]; ok {
					if c.SourceCodeDir != "" {
						src = c.SourceCodeDir
					}
					if c.EntrypointsDir != "" {
						entries = c.EntrypointsDir
					}
				}
			}
		}
		prefix(path.Join(src, entries), "Vite Ruby entrypoint")
	} else {
		prefix("app/javascript/entrypoints", "Vite Ruby entrypoint")
		prefix("app/frontend/entrypoints", "Vite Ruby entrypoint")
	}
	packer := false
	for _, f := range []string{"config/shakapacker.yml", "config/webpacker.yml"} {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(at(f))))
		if err != nil {
			continue
		}
		packer = true
		var cfg map[string]struct {
			SourcePath      string `yaml:"source_path"`
			SourceEntryPath string `yaml:"source_entry_path"`
		}
		src, entries := "app/javascript", "packs"
		if yaml.Unmarshal(b, &cfg) == nil {
			for _, env := range []string{"default", "development", "production"} {
				if c, ok := cfg[env]; ok {
					if c.SourcePath != "" {
						src = c.SourcePath
					}
					if c.SourceEntryPath != "" {
						entries = c.SourceEntryPath
					}
				}
			}
		}
		prefix(path.Join(src, strings.Trim(entries, "/")), "Shakapacker / Webpacker entry")
	}
	if !packer {
		prefix("app/javascript/packs", "Shakapacker / Webpacker entry")
	}
}

// ---- Rails layouts outside a Gemfile ---------------------------------------

// findRailsApps records the directories whose app/ has Rails' layout
// (an engine or a Discourse plugin without a Gemfile of its own).
func (m *manifests) findRailsApps() {
	for _, f := range m.sources {
		segs := strings.Split(f, "/")
		for i := 0; i+2 < len(segs); i++ {
			if segs[i] == "app" && imports.RailsAppDirs[segs[i+1]] {
				d := path.Join(segs[:i]...)
				if d == "" {
					d = "."
				}
				m.rbApps[d] = true
			}
		}
	}
}

// loadDiscoursePlugin: a Discourse plugin (plugin.rb) is loaded by
// Discourse, and the Ember resolver loads its JavaScript by name.
func (m *manifests) loadDiscoursePlugin(root, dir string) {
	if !m.rbApps[dir] && !isDir(root, path.Join(dir, "assets")) && !isDir(root, path.Join(dir, "lib")) {
		return // a plugin.rb that is not a Discourse plugin's
	}
	m.rbApps[dir] = true
	for _, d := range []string{"assets/javascripts", "admin/assets/javascripts", "test/javascripts"} {
		if p := path.Join(dir, d); isDir(root, p) {
			m.prefixes[p+"/"] = "Discourse plugin asset (Ember resolver)"
		}
	}
}

// ---- constants named by configuration --------------------------------------

// rbConfigYAML: YAML configuration of a Rails application (config/**/*.yml,
// not its translations), which names classes by string (site settings
// validators, schedules).
func rbConfigYAML(rel string) bool {
	if !strings.HasSuffix(rel, ".yml") && !strings.HasSuffix(rel, ".yaml") {
		return false
	}
	segs := strings.Split(path.Dir(rel), "/")
	return hasSeg(segs, map[string]bool{"config": true}) && !hasSeg(segs, map[string]bool{"locales": true, "locale": true})
}

// a YAML value or list item that is a constant name and nothing else
var yamlConstRe = regexp.MustCompile(`(?m)(?:^\s*-|:)\s*["']?((?:::)?[A-Z][A-Za-z0-9]*(?:::[A-Z][A-Za-z0-9]*)*)["']?\s*$`)

func (m *manifests) loadConfigConstants(root, rel string) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil || len(b) > 4<<20 {
		return
	}
	for _, sm := range yamlConstRe.FindAllSubmatch(b, -1) {
		if c := strings.TrimPrefix(string(sm[1]), "::"); m.rbYAML[c] == "" {
			m.rbYAML[c] = rel
		}
	}
}

var rubyShebangRe = regexp.MustCompile(`^#!\S*(?:/env\s+(?:-S\s+)?)?\S*\bruby\b`)

// loadRubyScript: an extensionless Ruby script (bin/setup, a #!/usr/bin/env
// ruby tool) is not parsed, but the constants its code names are used.
func (m *manifests) loadRubyScript(root, rel string) {
	f, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, 128)
	n, _ := io.ReadFull(f, head)
	if !rubyShebangRe.Match(head[:n]) {
		return // only the first line is read of files that are not Ruby
	}
	rest, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		return
	}
	b := append(head[:n], rest...)
	for _, sm := range rbTemplConst.FindAllSubmatch(rbStringRe.ReplaceAll(b, []byte(`""`)), -1) {
		if c := strings.TrimPrefix(string(sm[1]), "::"); m.rbYAML[c] == "" {
			m.rbYAML[c] = rel
		}
	}
}
