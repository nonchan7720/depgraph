// Package config reads .depgraph.yml, the per-repository settings file at
// the scanned root: the project key and the architecture rules. Decoding is
// strict: unknown keys, rules, options and layer names are errors.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
	"github.com/nonchan7720/depgraph/pkg/infrastructure/resolver"
	"github.com/nonchan7720/depgraph/pkg/internal/safefile"
)

// FileName is the settings file, looked up directly under the root.
const FileName = ".depgraph.yml"

// File is the content of .depgraph.yml with defaults applied.
type File struct {
	// Project is the project key ("" when unset).
	Project string
	// Rules is rules.DefaultConfig() with the file's settings applied.
	Rules rules.Config
	// Store is how Neo4j is provided (the store: section).
	Store StoreSettings
	// Warnings: settings of nested files that are not applied (they apply
	// only where that directory is scanned as the root).
	Warnings []string
}

// StoreSettings is the store: section. Every field is optional; flags and
// environment variables take precedence over it.
type StoreSettings struct {
	// Provider names the Neo4j provider ("external", "container"; "" lets
	// depgraph choose).
	Provider string `yaml:"provider"`
	// Persist keeps a container provider's data in a named volume across
	// runs (nil: not set here).
	Persist *bool `yaml:"persist"`
	// ContainerCLI is the container CLI the container provider runs
	// (docker, podman, nerdctl or a path; "" detects one).
	ContainerCLI string `yaml:"containerCli"`
}

// fileYAML is the on-disk schema.
type fileYAML struct {
	Project string        `yaml:"project"`
	Store   StoreSettings `yaml:"store"`
	// Layers: layer name -> package globs. Replaces the default layers (and
	// their forbidden pairs) when present.
	Layers map[string][]string `yaml:"layers"`
	// Forbidden: from layer -> layers it must not import.
	Forbidden map[string][]string `yaml:"forbidden"`
	// Abstract: from layer -> layers it may use only through interfaces.
	Abstract map[string][]string  `yaml:"abstract"`
	Rules    map[string]yaml.Node `yaml:"rules"`
	// Ignore: package globs excluded from every rule.
	Ignore []string `yaml:"ignore"`
	// Components: directory (or pattern such as packages/*) -> its layers.
	Components map[string]componentYAML `yaml:"components"`
	// Languages: language -> the version it runs (rules.LanguageSetting).
	Languages map[string]languageYAML `yaml:"languages"`
}

// languageYAML is one entry under languages:; paths are relative to the
// directory of the file (or of the component).
type languageYAML struct {
	VersionFile string `yaml:"versionFile"`
	Version     string `yaml:"version"`
	Stdlib      string `yaml:"stdlib"`
}

// languagesOf turns languages: of directory (or pattern) dir into settings.
func languagesOf(dir string, m map[string]languageYAML) []rules.LanguageSetting {
	out := make([]rules.LanguageSetting, 0, len(m))
	for _, lang := range sortedKeys(m) {
		y := m[lang]
		out = append(out, rules.LanguageSetting{Dir: dir, Lang: lang, VersionFile: y.VersionFile, Version: y.Version, Stdlib: y.Stdlib})
	}
	return out
}

// componentYAML is one entry under components:; its globs are relative to
// the component's directory.
type componentYAML struct {
	Layers    map[string][]string     `yaml:"layers"`
	Forbidden map[string][]string     `yaml:"forbidden"`
	Abstract  map[string][]string     `yaml:"abstract"`
	Languages map[string]languageYAML `yaml:"languages"`
}

// ruleYAML is one entry under rules:. Which of the rule-specific options a
// rule accepts is rules.Rule.Options.
type ruleYAML struct {
	Enabled  *bool     `yaml:"enabled"`
	Severity string    `yaml:"severity"`
	FanIn    *int      `yaml:"fanIn"`
	FanOut   *int      `yaml:"fanOut"`
	Ignore   *[]string `yaml:"ignore"`
	Entries  *[]string `yaml:"entries"`
	// duplicate-code
	MinTokens  *int     `yaml:"minTokens"`
	Similarity *float64 `yaml:"similarity"`
	// file-cycle
	Deferred *bool `yaml:"deferred"`
	// wide-function
	MaxPackages *int `yaml:"maxPackages"`
	// misplaced-function
	MinNames *int `yaml:"minNames"`
}

// Load reads root/.depgraph.yml and the .depgraph.yml files below it (a
// monorepo's packages, see loadNested). A missing file is the defaults.
func Load(root string) (File, error) {
	p := filepath.Join(root, FileName)
	b, err := safefile.ReadManifest(root, FileName)
	var f File
	switch {
	case errors.Is(err, os.ErrNotExist):
		f = File{Rules: rules.DefaultConfig()}
	case err != nil:
		return File{}, err
	default:
		if f, err = Parse(b); err != nil {
			return File{}, fmt.Errorf("%s: %w", p, err)
		}
	}
	if err := loadNested(root, &f); err != nil {
		return File{}, err
	}
	return f, nil
}

// Parse decodes the content of a .depgraph.yml and applies it to the defaults.
func Parse(b []byte) (File, error) {
	y, err := decode(b)
	if err != nil {
		return File{}, err
	}
	c := rules.DefaultConfig()
	if y.Layers != nil || y.Forbidden != nil {
		c.Layers, c.ForbiddenLayers, c.DefaultLayers = layersOf(y.Layers, y.Forbidden)
		if y.Layers != nil {
			c.AbstractLayers = nil // the default pairs name the default layers
		}
	}
	if y.Abstract != nil {
		c.AbstractLayers = pairsOf(y.Abstract)
	}
	if y.Ignore != nil {
		c.Ignore = y.Ignore
	}
	for _, name := range sortedKeys(y.Rules) {
		if err := applyRule(&c, name, y.Rules[name]); err != nil {
			return File{}, err
		}
	}
	addComponents(&c, ".", y.Components)
	c.Languages = append(c.Languages, languagesOf(".", y.Languages)...)
	if err := c.Validate(); err != nil {
		return File{}, err
	}
	return File{Project: y.Project, Rules: c, Store: y.Store}, nil
}

// decode reads one strict YAML document.
func decode(b []byte) (fileYAML, error) {
	var y fileYAML
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&y); err != nil && !errors.Is(err, io.EOF) {
		return fileYAML{}, fmt.Errorf("yaml: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return fileYAML{}, errors.New("want one YAML document")
	}
	return y, nil
}

// layersOf returns the layers and forbidden pairs a file declares. layers
// replaces the default layers and their forbidden pairs; forbidden alone
// refers to the default layers (defaults is then true).
func layersOf(layers, forbidden map[string][]string) (ls []rules.Layer, pairs [][2]string, defaults bool) {
	def := rules.DefaultConfig()
	ls, pairs, defaults = def.Layers, def.ForbiddenLayers, true
	if layers != nil {
		ls = make([]rules.Layer, 0, len(layers))
		for _, name := range sortedKeys(layers) {
			ls = append(ls, rules.Layer{Name: name, Packages: layers[name]})
		}
		pairs, defaults = nil, false // the default pairs name the default layers
	}
	if forbidden != nil {
		pairs = nil
		seen := map[[2]string]bool{}
		for _, from := range sortedKeys(forbidden) {
			for _, to := range forbidden[from] {
				p := [2]string{from, to}
				if !seen[p] {
					seen[p] = true
					pairs = append(pairs, p)
				}
			}
		}
	}
	return ls, pairs, defaults
}

// pairsOf flattens from layer -> layers into distinct [from, to] pairs.
func pairsOf(m map[string][]string) [][2]string {
	pairs := [][2]string{}
	seen := map[[2]string]bool{}
	for _, from := range sortedKeys(m) {
		for _, to := range m[from] {
			p := [2]string{from, to}
			if !seen[p] {
				seen[p] = true
				pairs = append(pairs, p)
			}
		}
	}
	return pairs
}

// componentAbstract: the abstract pairs of a component: its own, else the
// default ones over the default layers (none with layers of its own).
func componentAbstract(layers, abstract map[string][]string) [][2]string {
	switch {
	case abstract != nil:
		return pairsOf(abstract)
	case layers != nil:
		return nil
	}
	return rules.DefaultConfig().AbstractLayers
}

// addComponents adds the components: of the file in directory dir.
func addComponents(c *rules.Config, dir string, comps map[string]componentYAML) {
	for _, key := range sortedKeys(comps) {
		y := comps[key]
		at := rules.PrefixGlob(dir, key)
		c.Languages = append(c.Languages, languagesOf(at, y.Languages)...)
		if y.Languages != nil && y.Layers == nil && y.Forbidden == nil && y.Abstract == nil {
			continue // languages only: not a part with an architecture of its own
		}
		comp := rules.Component{Dir: at}
		comp.Layers, comp.ForbiddenLayers, comp.DefaultLayers = layersOf(y.Layers, y.Forbidden)
		comp.AbstractLayers = componentAbstract(y.Layers, y.Abstract)
		c.Components = append(c.Components, comp)
	}
}

// loadNested applies the .depgraph.yml files below root to f and records
// the directories of its modules (IsModuleManifest) in f.Rules.Modules. Each one is
// the configuration of its directory as a component of a monorepo: its
// layers, forbidden pairs, components and ignore are relative to it, and so
// are the paths of the unused-code rules. What only the root decides (rule
// switches, severities, thresholds) is not applied and reported in
// f.Warnings; project and store apply where the directory is the root.
// Directories the scan skips (node_modules, .git, .claude, --exclude, ...)
// are not read.
func loadNested(root string, f *File) error {
	var dirs []string
	modules := map[string]bool{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() && p != root {
				return filepath.SkipDir // unreadable: the scan skips it too
			}
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel != "." && resolver.ShouldSkipDir(rel, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if dir := path.Dir(rel); dir != "." {
			switch {
			case d.Name() == FileName:
				dirs = append(dirs, dir)
			case IsModuleManifest(d.Name()):
				modules[dir] = true
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(modules) > 0 {
		f.Rules.Modules = sortedKeys(modules)
	}
	if len(dirs) == 0 {
		return nil
	}
	c := &f.Rules
	for _, dir := range dirs {
		p := filepath.Join(root, filepath.FromSlash(dir), FileName)
		b, err := safefile.ReadManifest(root, path.Join(dir, FileName))
		if err != nil {
			return err
		}
		warnings, err := applyNested(c, dir, b)
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		f.Warnings = append(f.Warnings, warnings...)
	}
	if err := c.Validate(); err != nil {
		return fmt.Errorf("%s: %w", filepath.Join(root, FileName), err)
	}
	return nil
}

// IsModuleManifest reports whether a file name makes its directory a module
// of the repository (the units of module-cycle).
func IsModuleManifest(name string) bool {
	switch name {
	case "go.mod", "package.json", "pyproject.toml", "setup.py", "setup.cfg",
		"Gemfile", "package.yml": // package.yml: a packwerk pack
		return true
	}
	return strings.HasSuffix(name, ".gemspec")
}

// applyNested applies the .depgraph.yml of directory dir (below the root).
func applyNested(c *rules.Config, dir string, b []byte) ([]string, error) {
	y, err := decode(b)
	if err != nil {
		return nil, err
	}
	// validated as a root file first: the same file must work as --root
	own, err := Parse(b)
	if err != nil {
		return nil, err
	}
	if y.Layers != nil || y.Forbidden != nil || y.Abstract != nil {
		comp := rules.Component{Dir: dir}
		comp.Layers, comp.ForbiddenLayers, comp.DefaultLayers = layersOf(y.Layers, y.Forbidden)
		comp.AbstractLayers = componentAbstract(y.Layers, y.Abstract)
		c.Components = append(c.Components, comp)
	}
	addComponents(c, dir, y.Components)
	c.Languages = append(c.Languages, languagesOf(dir, y.Languages)...)
	for _, g := range y.Ignore {
		c.Ignore = append(c.Ignore, rules.PrefixGlob(dir, g))
	}
	var warnings []string
	for _, name := range sortedKeys(y.Rules) {
		n := y.Rules[name]
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i].Value
			if rules.IsUnusedRule(name) && (key == "ignore" || key == "entries") {
				continue
			}
			warnings = append(warnings, fmt.Sprintf("%s/%s: rules.%s.%s applies only where %s is the root (--root %s); the root's setting is used",
				dir, FileName, name, key, dir, dir))
		}
	}
	for _, g := range own.Rules.UnusedIgnore {
		c.UnusedIgnore = append(c.UnusedIgnore, rules.PrefixGlob(dir, g))
	}
	for _, g := range own.Rules.UnusedEntries {
		c.UnusedEntries = append(c.UnusedEntries, rules.PrefixGlob(dir, g))
	}
	return warnings, nil
}

func applyRule(c *rules.Config, name string, n yaml.Node) error {
	r, ok := rules.Lookup(name)
	if !ok {
		var known []string
		for _, b := range rules.Builtin() {
			known = append(known, b.Name)
		}
		return fmt.Errorf("rules: unknown rule %q (known: %s)", name, strings.Join(known, ", "))
	}
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("rules.%s: want a mapping such as {severity: warn}", name)
	}
	allowed := append([]string{"enabled", "severity"}, r.Options...)
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i].Value
		if !contains(allowed, key) {
			return fmt.Errorf("rules.%s: unknown option %q (allowed: %s)", name, key, strings.Join(allowed, ", "))
		}
	}
	var ry ruleYAML
	if err := n.Decode(&ry); err != nil {
		return fmt.Errorf("rules.%s: %w", name, err)
	}
	s := rules.RuleSetting{Severity: rules.Severity(ry.Severity)}
	if ry.Enabled != nil {
		s.Disabled = !*ry.Enabled
	}
	if c.Settings == nil {
		c.Settings = map[string]rules.RuleSetting{}
	}
	c.Settings[name] = s
	if ry.FanIn != nil {
		c.GodPackageFanIn = *ry.FanIn
	}
	if ry.FanOut != nil {
		if *ry.FanOut < 1 {
			return fmt.Errorf("rules.%s: fanOut %d (want >= 1)", name, *ry.FanOut)
		}
		c.GodPackageFanOut = *ry.FanOut
	}
	switch {
	case rules.IsUnusedRule(name):
		// shared by the three unused-code rules: the lists add up
		if ry.Ignore != nil {
			c.UnusedIgnore = append(c.UnusedIgnore, *ry.Ignore...)
		}
		if ry.Entries != nil {
			c.UnusedEntries = append(c.UnusedEntries, *ry.Entries...)
		}
	case ry.Ignore != nil:
		c.IgnoreDuplicateNames = *ry.Ignore
	}
	if ry.MinTokens != nil {
		if *ry.MinTokens <= 0 {
			return fmt.Errorf("rules.%s: minTokens %d (want > 0)", name, *ry.MinTokens)
		}
		c.Clones.MinTokens = *ry.MinTokens
	}
	if ry.MaxPackages != nil {
		if *ry.MaxPackages < 1 {
			return fmt.Errorf("rules.%s: maxPackages %d (want >= 1)", name, *ry.MaxPackages)
		}
		c.MaxCalledPackages = *ry.MaxPackages
	}
	if ry.MinNames != nil {
		if *ry.MinNames < 1 {
			return fmt.Errorf("rules.%s: minNames %d (want >= 1)", name, *ry.MinNames)
		}
		c.MinMisplacedNames = *ry.MinNames
	}
	if ry.Deferred != nil {
		c.FileCycleDeferred = *ry.Deferred
	}
	if ry.Similarity != nil {
		if *ry.Similarity <= 0 || *ry.Similarity > 1 {
			return fmt.Errorf("rules.%s: similarity %g (want 0 < similarity <= 1)", name, *ry.Similarity)
		}
		c.Clones.MinSimilarity = *ry.Similarity
	}
	return nil
}

func contains(s []string, v string) bool {
	return slices.Contains(s, v)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
