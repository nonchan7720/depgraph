// Package config reads .depgraph.yml, the per-repository settings file at
// the scanned root: the project key and the architecture rules. Decoding is
// strict: unknown keys, rules, options and layer names are errors.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// FileName is the settings file, looked up directly under the root.
const FileName = ".depgraph.yml"

// File is the content of .depgraph.yml with defaults applied.
type File struct {
	// Project is the project key ("" when unset).
	Project string
	// Rules is rules.DefaultConfig() with the file's settings applied.
	Rules rules.Config
}

// fileYAML is the on-disk schema.
type fileYAML struct {
	Project string `yaml:"project"`
	// Layers: layer name -> package globs. Replaces the default layers (and
	// their forbidden pairs) when present.
	Layers map[string][]string `yaml:"layers"`
	// Forbidden: from layer -> layers it must not import.
	Forbidden map[string][]string  `yaml:"forbidden"`
	Rules     map[string]yaml.Node `yaml:"rules"`
	// Ignore: package globs excluded from every rule.
	Ignore []string `yaml:"ignore"`
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
}

// Load reads root/.depgraph.yml. A missing file is the defaults.
func Load(root string) (File, error) {
	p := filepath.Join(root, FileName)
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return File{Rules: rules.DefaultConfig()}, nil
	}
	if err != nil {
		return File{}, err
	}
	f, err := Parse(b)
	if err != nil {
		return File{}, fmt.Errorf("%s: %w", p, err)
	}
	return f, nil
}

// Parse decodes the content of a .depgraph.yml and applies it to the defaults.
func Parse(b []byte) (File, error) {
	var y fileYAML
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(&y); err != nil && !errors.Is(err, io.EOF) {
		return File{}, fmt.Errorf("yaml: %w", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return File{}, errors.New("want one YAML document")
	}

	c := rules.DefaultConfig()
	if y.Layers != nil {
		c.Layers = make([]rules.Layer, 0, len(y.Layers))
		for _, name := range sortedKeys(y.Layers) {
			c.Layers = append(c.Layers, rules.Layer{Name: name, Packages: y.Layers[name]})
		}
		c.ForbiddenLayers = nil // the default pairs name the default layers
	}
	if y.Forbidden != nil {
		c.ForbiddenLayers = nil
		seen := map[[2]string]bool{}
		for _, from := range sortedKeys(y.Forbidden) {
			for _, to := range y.Forbidden[from] {
				p := [2]string{from, to}
				if !seen[p] {
					seen[p] = true
					c.ForbiddenLayers = append(c.ForbiddenLayers, p)
				}
			}
		}
	}
	if y.Ignore != nil {
		c.Ignore = y.Ignore
	}
	for _, name := range sortedKeys(y.Rules) {
		if err := applyRule(&c, name, y.Rules[name]); err != nil {
			return File{}, err
		}
	}
	if err := c.Validate(); err != nil {
		return File{}, err
	}
	return File{Project: y.Project, Rules: c}, nil
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
	if ry.Similarity != nil {
		if *ry.Similarity <= 0 || *ry.Similarity > 1 {
			return fmt.Errorf("rules.%s: similarity %g (want 0 < similarity <= 1)", name, *ry.Similarity)
		}
		c.Clones.MinSimilarity = *ry.Similarity
	}
	return nil
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
