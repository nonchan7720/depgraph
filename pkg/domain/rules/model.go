// Package rules holds the architectural rule model: the rules, their
// configuration (layers, forbidden imports, per-rule overrides), findings and
// the diff between two evaluations.
package rules

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Finding is one rule violation.
type Finding struct {
	Rule     string         `json:"rule"`
	Severity string         `json:"severity"` // error | warn | info
	Message  string         `json:"message"`
	ID       string         `json:"id,omitempty"` // stable identity across runs; defaults to Message
	Data     map[string]any `json:"data,omitempty"`
}

// Key identifies a finding across runs.
func (f Finding) Key() string {
	if f.ID != "" {
		return f.Rule + "|" + f.ID
	}
	return f.Rule + "|" + f.Message
}

// Severity of a finding.
type Severity string

// Severities, most severe first.
const (
	SeverityError Severity = "error"
	SeverityWarn  Severity = "warn"
	SeverityInfo  Severity = "info"
)

// Valid reports whether s is one of the known severities.
func (s Severity) Valid() bool {
	return s == SeverityError || s == SeverityWarn || s == SeverityInfo
}

// Rule names.
const (
	RuleImportCycle     = "import-cycle"
	RuleLayerViolation  = "layer-violation"
	RuleGodPackage      = "god-package"
	RuleDuplicateSymbol = "duplicate-symbol"
	RuleDuplicateCode   = "duplicate-code"
)

// Rule describes one rule: its name, default severity and the rule-specific
// options it accepts in the configuration file (besides enabled / severity).
type Rule struct {
	Name        string
	Severity    Severity
	Description string
	Options     []string
}

// builtin is the rule registry. A rule added here is configurable by name
// (enabled / severity) without touching the configuration loader.
var builtin = []Rule{
	{Name: RuleImportCycle, Severity: SeverityError, Description: "import cycle between packages"},
	{Name: RuleLayerViolation, Severity: SeverityError, Description: "a package imports a layer it must not import"},
	{Name: RuleGodPackage, Severity: SeverityWarn, Description: "a hub: imported by many packages and importing many", Options: []string{"fanIn", "fanOut"}},
	{Name: RuleDuplicateSymbol, Severity: SeverityWarn, Description: "an exported function / class declared in several files", Options: []string{"ignore"}},
	{Name: RuleDuplicateCode, Severity: SeverityWarn, Description: "the same logic in several places, whatever the names", Options: []string{"minTokens", "similarity"}},
}

// Builtin returns the registered rules.
func Builtin() []Rule { return append([]Rule(nil), builtin...) }

// Lookup returns the registered rule called name.
func Lookup(name string) (Rule, bool) {
	for _, r := range builtin {
		if r.Name == name {
			return r, true
		}
	}
	return Rule{}, false
}

func ruleNames() string {
	names := make([]string, len(builtin))
	for i, r := range builtin {
		names[i] = r.Name
	}
	return strings.Join(names, ", ")
}

// Layer is a named set of packages, selected by package key globs (see Glob).
type Layer struct {
	Name     string
	Packages []string
}

// RuleSetting overrides one rule. The zero value keeps the rule's defaults.
type RuleSetting struct {
	Disabled bool
	Severity Severity // "" keeps the rule's default severity
}

// Config tunes the rules.
type Config struct {
	// Layers are the architecture layers. A package may belong to several.
	Layers []Layer
	// ForbiddenLayers: [from, to] layer names; a package in from must not
	// import a package in to. An import between two packages that are both
	// in from or both in to is not a violation (it does not cross the pair).
	ForbiddenLayers [][2]string
	// AbstractLayers: [from, to] layer names; from may use to, but only
	// through abstractions: holding or building an implementation of to
	// (concrete-dependency) or exposing its concrete types in from's API
	// (type-leak) is reported.
	AbstractLayers [][2]string
	// GodPackageFanIn / GodPackageFanOut: a god package is a hub, imported
	// by at least FanIn packages AND importing at least FanOut in-repo
	// packages. A package many depend on that depends on nothing in the
	// repository (a domain model) is stable, not a god package.
	GodPackageFanIn  int
	GodPackageFanOut int
	// IgnoreDuplicateNames: symbol name patterns (see NamePattern: an exact
	// name, a prefix such as Test*, or a glob with * and ?) excluded from
	// the duplicate check.
	IgnoreDuplicateNames []string
	// Ignore: package key globs excluded from every rule.
	Ignore []string
	// Settings: per-rule overrides by rule name.
	Settings map[string]RuleSetting
	// UnusedEntries: path globs of entry points nothing in the repository
	// shows (a plugin loaded by name, a file an external tool runs). They
	// are reachable and their exports are used. A `depgraph:entry` comment
	// in the file does the same.
	UnusedEntries []string
	// UnusedIgnore: path globs (files or Go package dirs) the unused-code
	// rules never report, e.g. a library's public API.
	UnusedIgnore []string
	// FileCycleDeferred: file-cycle also reports the cycles that only close
	// through an import inside a function.
	FileCycleDeferred bool
	// Clones tunes duplicate-code.
	Clones CloneOptions
	// DefaultLayers: Layers are the built-in defaults, a guess from
	// directory names that is not applied inside Components with layers of
	// their own. Layers the root declares apply to every package.
	DefaultLayers bool
	// Components: parts of a monorepo with layers of their own (a
	// .depgraph.yml in their directory, or components: of the root's).
	Components []Component
	// Languages: the version settings of the languages whose imports are
	// classified from a version (see LanguageSetting).
	Languages []LanguageSetting
	// Modules: the directories below the root of the repository's modules
	// (go.mod, package.json, pyproject.toml, ...), the units of module-cycle
	// together with the Components.
	Modules []string
}

// segmentLayer is a default layer: the name (and its other spellings) as a
// whole path segment anywhere in the package key.
func segmentLayer(name string, spellings ...string) Layer {
	l := Layer{Name: name}
	for _, s := range append([]string{name}, spellings...) {
		l.Packages = append(l.Packages, "**/"+s+"/**")
	}
	return l
}

// DefaultConfig returns the configuration used without .depgraph.yml.
func DefaultConfig() Config {
	return Config{
		Layers: []Layer{
			segmentLayer("controller", "controllers"),
			segmentLayer("db"),
			segmentLayer("domain"),
			segmentLayer("handler", "handlers"),
			segmentLayer("infra", "infrastructure"),
			segmentLayer("repository", "repositories"),
			segmentLayer("usecase", "usecases"),
		},
		ForbiddenLayers: [][2]string{
			{"handler", "repository"}, {"handler", "infra"}, {"handler", "db"},
			{"controller", "repository"}, {"controller", "db"},
			{"domain", "infra"}, {"domain", "handler"}, {"domain", "repository"},
			{"usecase", "handler"},
		},
		AbstractLayers:    [][2]string{{"usecase", "infra"}, {"usecase", "db"}},
		DefaultLayers:     true,
		GodPackageFanIn:   10,
		GodPackageFanOut:  3,
		FileCycleDeferred: true,
		Clones:            DefaultCloneOptions(),
		IgnoreDuplicateNames: []string{
			"main", "init", "TestMain", "New", "String", "Error", "Close", "Run", "Validate",
			"default", "index", "setup", "teardown", "handler", "render", "App",
		},
	}
}

// Enabled reports whether rule runs.
func (c Config) Enabled(rule string) bool { return !c.Settings[rule].Disabled }

// Severity returns the severity of rule's findings.
func (c Config) Severity(rule string) Severity {
	if s := c.Settings[rule].Severity; s != "" {
		return s
	}
	if r, ok := Lookup(rule); ok {
		return r.Severity
	}
	return SeverityWarn
}

// layerSets compiles the layers' patterns by layer name.
func (c Config) layerSets() (map[string]LayerSet, error) {
	out := make(map[string]LayerSet, len(c.Layers))
	for _, l := range c.Layers {
		if l.Name == "" {
			return nil, errors.New("layer without a name")
		}
		if _, dup := out[l.Name]; dup {
			return nil, fmt.Errorf("layer %q defined twice", l.Name)
		}
		s, err := compileLayerSet(l.Packages)
		if err != nil {
			return nil, fmt.Errorf("layer %q: %w", l.Name, err)
		}
		out[l.Name] = s
	}
	return out, nil
}

// IgnoreGlobs compiles Ignore.
func (c Config) IgnoreGlobs() ([]Glob, error) {
	gs, err := compileGlobs(c.Ignore)
	if err != nil {
		return nil, fmt.Errorf("ignore: %w", err)
	}
	return gs, nil
}

// LayersOf returns the names of the root layers in-repo package key
// belongs to, sorted.
func (c Config) LayersOf(key string) ([]string, error) {
	layers, err := c.layerSets()
	if err != nil {
		return nil, err
	}
	var out []string
	for name, s := range layers {
		if s.Contains(PackageRef{Key: key}) {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Ignored reports whether package key is excluded from every rule.
func (c Config) Ignored(key string) (bool, error) {
	gs, err := c.IgnoreGlobs()
	if err != nil {
		return false, err
	}
	return matchAny(gs, key), nil
}

// Validate reports the first problem in c: an unknown rule or severity, a
// bad pattern, or a forbidden pair naming an undefined layer.
func (c Config) Validate() error {
	names := make([]string, 0, len(c.Settings))
	for name := range c.Settings {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, ok := Lookup(name); !ok {
			return fmt.Errorf("unknown rule %q (known: %s)", name, ruleNames())
		}
		if s := c.Settings[name].Severity; s != "" && !s.Valid() {
			return fmt.Errorf("rule %s: severity %q (want error, warn or info)", name, s)
		}
	}
	if c.Clones.MinTokens < 0 {
		return fmt.Errorf("rule %s: minTokens %d (want > 0)", RuleDuplicateCode, c.Clones.MinTokens)
	}
	if c.Clones.MinSimilarity < 0 || c.Clones.MinSimilarity > 1 {
		return fmt.Errorf("rule %s: similarity %g (want 0 < similarity <= 1)", RuleDuplicateCode, c.Clones.MinSimilarity)
	}
	if err := validateLayers(c.Layers, c.ForbiddenLayers, c.AbstractLayers); err != nil {
		return err
	}
	if err := validateLanguages(c.Languages); err != nil {
		return err
	}
	if err := c.validateComponents(); err != nil {
		return err
	}
	if _, err := c.IgnoreGlobs(); err != nil {
		return err
	}
	if _, _, err := c.UnusedGlobs(); err != nil {
		return err
	}
	if _, err := c.DuplicateNameRegexes(); err != nil {
		return err
	}
	if c.GodPackageFanOut < 0 {
		return fmt.Errorf("rule %s: fanOut must be at least 1, got %d", RuleGodPackage, c.GodPackageFanOut)
	}
	if c.GodPackageFanIn < 1 {
		return fmt.Errorf("rule %s: fanIn must be at least 1, got %d", RuleGodPackage, c.GodPackageFanIn)
	}
	return nil
}

func layerNames(ls []Layer) string {
	if len(ls) == 0 {
		return "none"
	}
	names := make([]string, len(ls))
	for i, l := range ls {
		names[i] = l.Name
	}
	return strings.Join(names, ", ")
}

// Diff returns findings added and removed between prev and cur.
func Diff(prev, cur []Finding) (added, removed []Finding) {
	pm := map[string]Finding{}
	for _, f := range prev {
		pm[f.Key()] = f
	}
	cm := map[string]Finding{}
	for _, f := range cur {
		cm[f.Key()] = f
		if _, ok := pm[f.Key()]; !ok {
			added = append(added, f)
		}
	}
	for k, f := range pm {
		if _, ok := cm[k]; !ok {
			removed = append(removed, f)
		}
	}
	sort.Slice(removed, func(i, j int) bool { return removed[i].Key() < removed[j].Key() })
	return added, removed
}
