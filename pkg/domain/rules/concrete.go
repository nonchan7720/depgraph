package rules

import (
	"fmt"
	"sort"
	"strings"
)

// RuleConcreteDependency: a struct / class of a layer holds (as a field or
// a constructor parameter) an implementation of a layer it may use only
// through abstractions (Config.AbstractLayers): the import is allowed, the
// dependency is not inverted.
const RuleConcreteDependency = "concrete-dependency"

func init() {
	builtin = append(builtin, Rule{Name: RuleConcreteDependency, Severity: SeverityWarn,
		Description: "a struct / class holds an implementation of a layer it must use through an interface (abstract:)"})
}

// TypeShape is what the graph knows of a type a dependency names.
type TypeShape struct {
	Kind     string // struct, class, interface, type, ...
	Abstract bool
	// Methods: how many methods the type declares (Go: on the type or its
	// pointer, in its package).
	Methods int
}

// Concrete reports whether the type is an implementation one should not
// depend on directly: a struct or class with behaviour that is not
// abstract. A struct without methods is data (a row, an options struct),
// an interface or abstract class is the abstraction itself.
func (t TypeShape) Concrete() bool {
	return (t.Kind == "struct" || t.Kind == "class") && !t.Abstract && t.Methods > 0
}

// HeldDependency is a graph.TypeDep with where it is and what it names.
type HeldDependency struct {
	File, Package  string // the holder's file and package key
	Holder, Member string
	Line           int
	TargetPackage  string // package key of the type's declaration
	Type           string
	Shape          TypeShape
}

func (d HeldDependency) where() string {
	return fmt.Sprintf("%s:%d %s.%s", d.File, d.Line, d.Holder, d.Member)
}

// ConcreteDependencies reports, per package and concrete type, the
// dependencies that an abstract pair (AbstractPairs) applies to. Severity is
// left to the caller.
func ConcreteDependencies(deps []HeldDependency, pairs []LayerPair) []Finding {
	type key struct{ from, to, typ string }
	type group struct {
		pair    LayerPair
		members []HeldDependency
	}
	groups := map[key]*group{}
	for _, d := range deps {
		if !d.Shape.Concrete() || d.Package == d.TargetPackage {
			continue
		}
		k := key{d.Package, d.TargetPackage, d.Type}
		if g := groups[k]; g != nil {
			g.members = append(g.members, d)
			continue
		}
		for _, p := range pairs {
			if p.Reports(PackageRef{Key: d.Package}, PackageRef{Key: d.TargetPackage}) {
				groups[k] = &group{pair: p, members: []HeldDependency{d}}
				break
			}
		}
	}
	out := make([]Finding, 0, len(groups))
	for k, g := range groups {
		sort.Slice(g.members, func(i, j int) bool {
			a, b := g.members[i], g.members[j]
			if a.File != b.File {
				return a.File < b.File
			}
			return a.Line < b.Line
		})
		members := make([]string, len(g.members))
		for i, m := range g.members {
			members[i] = m.where()
		}
		at := members[0]
		if n := len(members) - 1; n > 0 {
			at += fmt.Sprintf(", and %d more", n)
		}
		rule := fmt.Sprintf("%s must depend on %s through an interface", g.pair.From, g.pair.To)
		if g.pair.Component != "" {
			rule = g.pair.Component + ": " + rule
		}
		data := map[string]any{"from": k.from, "to": k.to, "type": k.typ,
			"fromLayer": g.pair.From, "toLayer": g.pair.To, "members": members}
		if g.pair.Component != "" {
			data["component"] = g.pair.Component
		}
		out = append(out, Finding{
			Rule:    RuleConcreteDependency,
			ID:      fmt.Sprintf("%s -> %s.%s", k.from, k.to, k.typ),
			Message: fmt.Sprintf("%s depends on the concrete type %s.%s (%s): %s", k.from, k.to, k.typ, at, rule),
			Data:    data,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// RuleLargePackage: a package with more code than its limit, a sign that
// it holds more than one responsibility (or that logic leaked into an
// outer layer that should stay thin).
const RuleLargePackage = "large-package"

func init() {
	builtin = append(builtin, Rule{Name: RuleLargePackage, Severity: SeverityWarn,
		Description: "a package with more lines of code than its limit (maxLines for Go packages; layers: a limit per layer, for every language)",
		Options:     []string{"maxLines", "layers"}})
}

// LargePackageOptions tunes large-package.
type LargePackageOptions struct {
	// MaxLines: the limit of a Go package (a Go package is one namespace;
	// in other languages a directory is a set of modules). 0 turns it off.
	MaxLines int
	// Layers: layer name (of the root's layers) -> the limit of each of its
	// packages, in every language. The smallest limit of a package's layers
	// applies, before MaxLines.
	Layers map[string]int
}

// DefaultLargePackageOptions are the defaults of large-package.
func DefaultLargePackageOptions() LargePackageOptions { return LargePackageOptions{MaxLines: 5000} }

func (o LargePackageOptions) validate(layers []Layer) error {
	if o.MaxLines < 0 {
		return fmt.Errorf("rule %s: maxLines %d (want >= 0)", RuleLargePackage, o.MaxLines)
	}
	names := map[string]bool{}
	for _, l := range layers {
		names[l.Name] = true
	}
	for _, name := range sortedLayerNames(o.Layers) {
		if !names[name] {
			return fmt.Errorf("rule %s: layers: unknown layer %q (defined: %s)", RuleLargePackage, name, layerNames(layers))
		}
		if o.Layers[name] <= 0 {
			return fmt.Errorf("rule %s: layers.%s: %d (want > 0)", RuleLargePackage, name, o.Layers[name])
		}
	}
	return nil
}

func sortedLayerNames(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// PackageSize is the code of one in-repo package: lines of its source files
// (tests and generated files left out).
type PackageSize struct {
	Key          string
	Lines, Files int
	// Go: the package has Go files.
	Go bool
}

// LargePackages reports the packages over their limit. Severity is left to
// the caller.
func LargePackages(sizes []PackageSize, c Config) ([]Finding, error) {
	sets, err := c.layerSets()
	if err != nil {
		return nil, err
	}
	var out []Finding
	for _, s := range sizes {
		limit, layer := 0, ""
		for _, name := range sortedLayerNames(c.LargePackage.Layers) {
			n := c.LargePackage.Layers[name]
			if sets[name].Contains(PackageRef{Key: s.Key}) && (limit == 0 || n < limit) {
				limit, layer = n, name
			}
		}
		if limit == 0 && s.Go {
			limit = c.LargePackage.MaxLines
		}
		if limit == 0 || s.Lines <= limit {
			continue
		}
		bound := fmt.Sprintf("limit %d", limit)
		if layer != "" {
			bound += " for layer " + layer
		}
		data := map[string]any{"pkg": s.Key, "lines": s.Lines, "files": s.Files, "limit": limit}
		if layer != "" {
			data["layer"] = layer
		}
		out = append(out, Finding{
			Rule: RuleLargePackage, ID: s.Key, Data: data,
			Message: fmt.Sprintf("%s has %d lines in %d files (%s): split it by responsibility", s.Key, s.Lines, s.Files, bound),
		})
	}
	sort.Slice(out, func(i, j int) bool { return strings.Compare(out[i].ID, out[j].ID) < 0 })
	return out, nil
}
