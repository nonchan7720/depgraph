package rules

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Component is a part of a monorepo with its own architecture: a directory
// (a Go module, an npm package, a Python project, ...) whose layers are
// declared relative to it. Its forbidden pairs apply to imports between two
// packages of the same component directory only.
type Component struct {
	// Dir is the package key of the component's directory, or a pattern
	// over directories with * and ? (not **): "packages/*" is one component
	// per directory it matches (packages/ui, packages/api, ...).
	Dir string
	// Layers: package globs relative to Dir ("src/domain/**").
	Layers []Layer
	// ForbiddenLayers: [from, to] layer names of Layers.
	ForbiddenLayers [][2]string
	// DefaultLayers: Layers are the built-in defaults (the component only
	// set forbidden pairs).
	DefaultLayers bool
}

// LayerPair is one forbidden pair, compiled for the store: a package matched
// by FromRx must not import one matched by ToRx, unless the import does not
// cross the pair or either end is matched by ExceptRx.
type LayerPair struct {
	// Component is the directory of the component the pair belongs to
	// ("" for the root's layers).
	Component    string
	From, To     string
	FromRx, ToRx []string
	// ExceptRx: packages outside these layers, the components with layers
	// of their own when these layers are the built-in defaults.
	ExceptRx []string
}

// PrefixGlob makes glob, relative to directory dir, relative to the root.
func PrefixGlob(dir, glob string) string {
	g := strings.TrimPrefix(strings.TrimSuffix(strings.TrimPrefix(glob, "./"), "/"), "/")
	switch {
	case dir == "." || dir == "":
		return g
	case g == "" || g == ".":
		return dir
	}
	return dir + "/" + g
}

// isPattern reports whether a component directory has wildcards.
func isPattern(dir string) bool { return strings.ContainsAny(dir, "*?") }

// ExpandComponents returns c with every component whose directory is a
// pattern replaced by one component per directory of keys (package keys)
// it matches; a component declared for a directory itself (its own
// .depgraph.yml) takes precedence over a pattern matching it.
func (c Config) ExpandComponents(keys []string) Config {
	concrete := map[string]bool{}
	hasPattern := false
	for _, comp := range c.Components {
		if isPattern(comp.Dir) {
			hasPattern = true
		} else {
			concrete[comp.Dir] = true
		}
	}
	if !hasPattern {
		return c
	}
	out := c
	out.Components = nil
	seen := map[string]bool{}
	for _, comp := range c.Components {
		if !isPattern(comp.Dir) {
			out.Components = append(out.Components, comp)
			continue
		}
		g, err := CompileGlob(comp.Dir)
		if err != nil {
			continue // Validate reports it
		}
		depth := strings.Count(comp.Dir, "/") + 1
		for _, k := range keys {
			segs := strings.Split(k, "/")
			if len(segs) < depth {
				continue
			}
			dir := strings.Join(segs[:depth], "/")
			if concrete[dir] || seen[dir] || !g.Match(dir) {
				continue
			}
			seen[dir] = true
			inst := comp
			inst.Dir = dir
			out.Components = append(out.Components, inst)
		}
	}
	sort.SliceStable(out.Components, func(i, j int) bool { return out.Components[i].Dir < out.Components[j].Dir })
	return out
}

// LayerPairs compiles the forbidden pairs of the root and of every
// component. The root's layers apply to every package, except that the
// built-in defaults (a guess from directory names) are not applied to the
// packages of a component with layers of its own; a component's default
// layers likewise leave out the components nested in it. Components whose
// directory is a pattern are skipped: expand them first (ExpandComponents).
func (c Config) LayerPairs() ([]LayerPair, error) {
	var out []LayerPair
	add := func(scope string, layers []Layer, forbidden [][2]string, defaults bool) error {
		if len(forbidden) == 0 {
			return nil
		}
		if scope != "" {
			scoped := make([]Layer, len(layers))
			for i, l := range layers {
				scoped[i] = Layer{Name: l.Name, Packages: make([]string, len(l.Packages))}
				for j, p := range l.Packages {
					scoped[i].Packages[j] = PrefixGlob(scope, p)
				}
			}
			layers = scoped
		}
		globs, err := (Config{Layers: layers}).LayerGlobs()
		if err != nil {
			return err
		}
		var except []string
		if defaults {
			for _, comp := range c.Components {
				if isPattern(comp.Dir) || comp.Dir == scope || (scope != "" && !strings.HasPrefix(comp.Dir, scope+"/")) {
					continue
				}
				g, err := CompileGlob(comp.Dir + "/**")
				if err != nil {
					return err
				}
				except = append(except, g.Regex())
			}
		}
		for _, p := range forbidden {
			out = append(out, LayerPair{
				Component: scope, From: p[0], To: p[1],
				FromRx: Regexes(globs[p[0]]), ToRx: Regexes(globs[p[1]]), ExceptRx: except,
			})
		}
		return nil
	}
	if err := add("", c.Layers, c.ForbiddenLayers, c.DefaultLayers); err != nil {
		return nil, err
	}
	for _, comp := range c.Components {
		if isPattern(comp.Dir) {
			continue
		}
		if err := add(comp.Dir, comp.Layers, comp.ForbiddenLayers, comp.DefaultLayers); err != nil {
			return nil, fmt.Errorf("component %s: %w", comp.Dir, err)
		}
	}
	return out, nil
}

// validateLayers checks layers and the forbidden pairs over them.
func validateLayers(layers []Layer, forbidden [][2]string) error {
	globs, err := (Config{Layers: layers}).LayerGlobs()
	if err != nil {
		return err
	}
	for _, p := range forbidden {
		for _, l := range p {
			if _, ok := globs[l]; !ok {
				return fmt.Errorf("forbidden %s -> %s: unknown layer %q (defined: %s)", p[0], p[1], l, layerNames(layers))
			}
		}
		if p[0] == p[1] {
			return fmt.Errorf("forbidden: layer %q cannot forbid itself", p[0])
		}
	}
	return nil
}

// validateComponents checks the component directories and their layers.
func (c Config) validateComponents() error {
	seen := map[string]bool{}
	for _, comp := range c.Components {
		dir := comp.Dir
		switch {
		case dir == "":
			return errors.New("component without a directory")
		case dir == "." || strings.Contains(dir, "**") || strings.HasPrefix(dir, "/") || strings.HasSuffix(dir, "/") ||
			strings.HasPrefix(dir, "./") || strings.Contains(dir, "//") || strings.Contains(dir, "\\"):
			return fmt.Errorf("component %q: want a directory below the root such as backend or packages/* (no **)", dir)
		case seen[dir]:
			return fmt.Errorf("component %s defined twice", dir)
		}
		for seg := range strings.SplitSeq(dir, "/") {
			if seg == "." || seg == ".." {
				return fmt.Errorf("component %q: want a directory below the root such as backend or packages/* (no **)", dir)
			}
		}
		seen[dir] = true
		if _, err := CompileGlob(dir); err != nil {
			return fmt.Errorf("component %q: %w", dir, err)
		}
		if err := validateLayers(comp.Layers, comp.ForbiddenLayers); err != nil {
			return fmt.Errorf("component %s: %w", dir, err)
		}
	}
	return nil
}
