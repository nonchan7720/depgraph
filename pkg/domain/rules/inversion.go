package rules

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
)

// Rules about the code inside the layers, over the members model
// (graph.TypeUse, graph.Construct). Their pairs are Config.AbstractLayers:
// a layer may use another one, but only through abstractions.
const (
	// RuleConcreteDependency: a struct / class of a layer holds or builds an
	// implementation of a layer it must use through an interface, although
	// an interface that implementation satisfies is at hand.
	RuleConcreteDependency = "concrete-dependency"
	// RuleTypeLeak: the API of a layer exposes a concrete type (or data) of
	// a layer it must use only through an interface.
	RuleTypeLeak = "type-leak"
)

func init() {
	builtin = append(builtin,
		Rule{Name: RuleConcreteDependency, Severity: SeverityWarn,
			Description: "a struct / class holds or builds an implementation of a layer it must use through an interface (abstract:), although an interface it implements exists"},
		Rule{Name: RuleTypeLeak, Severity: SeverityWarn,
			Description: "the API of a layer exposes a concrete type or data of a layer it must use only through an interface (abstract:)"},
	)
}

// AbstractPairs compiles the abstract pairs (Config.AbstractLayers) of the
// root and of every component, scoped like LayerPairs.
func (c Config) AbstractPairs() ([]LayerPair, error) {
	return c.pairs(c.AbstractLayers, func(comp Component) [][2]string { return comp.AbstractLayers })
}

// TypeInfo is what the graph knows of a type declared in the repository.
type TypeInfo struct {
	Ref     graph.TypeRef
	Package string // package key of its file
	Kind    string // struct, class, interface, type, ...
	// Abstract: an abstract class (an interface is Kind interface).
	Abstract bool
	// Data: declared as a record (a Python dataclass, ...).
	Data bool
	// Methods: the methods it declares (Go: of the type or its pointer, in
	// any non-test file of its package).
	Methods []string
	// Implements: the abstractions it says it implements (TypeScript,
	// Python).
	Implements []graph.TypeRef
	// Embeds: a Go interface embedding others (Methods is not its whole
	// method set).
	Embeds bool
}

// abstraction reports whether the type is an interface / abstract class.
func (t TypeInfo) abstraction() bool { return t.Kind == "interface" || t.Abstract }

// goDataMethods are the methods values implement for the standard library
// (fmt.Stringer, error, the encoders, database/sql, sort, ...): a struct with
// only these is data, not a service.
var goDataMethods = map[string]bool{
	"String": true, "GoString": true, "Format": true, "Error": true, "Unwrap": true, "Is": true,
	"MarshalJSON": true, "UnmarshalJSON": true, "MarshalText": true, "UnmarshalText": true,
	"MarshalYAML": true, "UnmarshalYAML": true, "MarshalBinary": true, "UnmarshalBinary": true,
	"MarshalXML": true, "UnmarshalXML": true, "Scan": true, "Value": true, "TableName": true,
	"Len": true, "Less": true, "Swap": true, "Equal": true, "Clone": true, "Validate": true,
}

// IsDataMethod reports whether a method is one values implement for the
// standard library (String, MarshalJSON, Scan, ...), not behaviour.
func IsDataMethod(name string) bool { return goDataMethods[name] }

// behaviour are the methods of a type that are not data methods.
func (t TypeInfo) behaviour() []string {
	var out []string
	for _, m := range t.Methods {
		if !goDataMethods[m] {
			out = append(out, m)
		}
	}
	return out
}

// concrete reports whether the type is an implementation: a struct or class
// with behaviour that is neither abstract nor data.
func (t TypeInfo) concrete() bool {
	return (t.Kind == "struct" || t.Kind == "class") && !t.Abstract && !t.Data && len(t.behaviour()) > 0
}

// implements reports whether t implements the abstraction i: it says so
// (TypeScript, Python), or, for a Go struct, it has every method of the
// interface (Go's structural rule, by name).
func (t TypeInfo) implements(i TypeInfo) bool {
	if slices.Contains(t.Implements, i.Ref) {
		return true
	}
	if t.Kind != "struct" || i.Kind != "interface" || len(i.Methods) == 0 {
		return false
	}
	for _, m := range i.Methods {
		if !slices.Contains(t.Methods, m) {
			return false
		}
	}
	return true
}

// Abstraction reports whether the type is an interface / abstract class.
func (t TypeInfo) Abstraction() bool { return t.abstraction() }

// ImplementsType reports whether t implements the abstraction i (see implements).
func (t TypeInfo) ImplementsType(i TypeInfo) bool { return t.implements(i) }

// TypeIndex looks up the types of the repository.
type TypeIndex interface {
	Type(graph.TypeRef) (TypeInfo, bool)
	// Interfaces are the abstractions declared in package key pkg.
	Interfaces(pkg string) []TypeInfo
}

// MemberUse is a graph.TypeUse (or a graph.Construct) with where it is.
type MemberUse struct {
	File, Package string
	graph.TypeUse
	Construct bool
}

func (u MemberUse) where() string {
	if u.Construct {
		return fmt.Sprintf("%s:%d builds it in %s", u.File, u.Line, u.Owner)
	}
	if u.Role == graph.RoleResult {
		return fmt.Sprintf("%s:%d %s result %s", u.File, u.Line, u.Owner, u.Member)
	}
	return fmt.Sprintf("%s:%d %s.%s", u.File, u.Line, u.Owner, u.Member)
}

type usePair struct {
	from, to, typ string
}

// group collects uses by (package, type package, type) with the first pair
// that applies.
type group struct {
	pair  LayerPair
	info  TypeInfo
	uses  []MemberUse
	extra string
}

func findings(rule string, groups map[usePair]*group, msg func(k usePair, g *group, at string) string) []Finding {
	out := make([]Finding, 0, len(groups))
	for k, g := range groups {
		sort.Slice(g.uses, func(i, j int) bool {
			a, b := g.uses[i], g.uses[j]
			if a.File != b.File {
				return a.File < b.File
			}
			return a.Line < b.Line
		})
		members := make([]string, len(g.uses))
		for i, u := range g.uses {
			members[i] = u.where()
		}
		members = slices.Compact(members)
		at := members[0]
		if n := len(members) - 1; n > 0 {
			at += fmt.Sprintf(", and %d more", n)
		}
		data := map[string]any{"from": k.from, "to": k.to, "type": k.typ, "fromLayer": g.pair.From, "toLayer": g.pair.To, "members": members}
		if g.pair.Component != "" {
			data["component"] = g.pair.Component
		}
		if g.extra != "" {
			data["abstraction"] = g.extra
		}
		out = append(out, Finding{Rule: rule, ID: fmt.Sprintf("%s -> %s.%s", k.from, k.to, k.typ), Message: msg(k, g, at), Data: data})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func pairRule(p LayerPair, what string) string {
	r := fmt.Sprintf("%s must %s %s through an interface", p.From, what, p.To)
	if p.Component != "" {
		r = p.Component + ": " + r
	}
	return r
}

// pairFor is the first abstract pair applying to a use of package from on
// a type of package to.
func pairFor(pairs []LayerPair, from, to string) (LayerPair, bool) {
	if from == to {
		return LayerPair{}, false
	}
	for _, p := range pairs {
		if p.Reports(PackageRef{Key: from}, PackageRef{Key: to}) {
			return p, true
		}
	}
	return LayerPair{}, false
}

// ConcreteDependencies reports, per package and concrete type, the fields,
// constructor parameters and constructions that an abstract pair applies
// to, when the type implements an abstraction the holder could hold
// instead: one that has every method the holder calls on it (the smallest
// such, one of the holder's package first), declared in the holder's
// package, the type's package or the pair's from layer; without recorded
// calls, the fullest one of the holder's package. Without such an abstraction
// nothing is reported (that the code should have one is a design call, not
// a fact). Severity is left to the caller.
func ConcreteDependencies(uses []MemberUse, types TypeIndex, pairs []LayerPair) []Finding {
	type pending struct {
		pair LayerPair
		info TypeInfo
		uses []MemberUse
	}
	byKey := map[usePair]*pending{}
	var keys []usePair
	for _, u := range uses {
		if !u.Construct && (u.Collection || (u.Role != graph.RoleField && u.Role != graph.RoleCtor)) {
			continue
		}
		t, ok := types.Type(u.TypeRef)
		if !ok || !t.concrete() {
			continue
		}
		k := usePair{u.Package, t.Package, t.Ref.Type}
		if g := byKey[k]; g != nil {
			g.uses = append(g.uses, u)
			continue
		}
		p, ok := pairFor(pairs, u.Package, t.Package)
		if !ok {
			continue
		}
		byKey[k] = &pending{pair: p, info: t, uses: []MemberUse{u}}
		keys = append(keys, k)
	}
	groups := map[usePair]*group{}
	for _, k := range keys {
		g := byKey[k]
		var calls []string
		for _, u := range g.uses {
			calls = append(calls, u.Calls...)
		}
		slices.Sort(calls)
		calls = slices.Compact(calls)
		if port, ok := portOf(g.info, types, k.from, g.pair, calls); ok {
			groups[k] = &group{pair: g.pair, info: g.info, uses: g.uses, extra: port}
		}
	}
	return findings(RuleConcreteDependency, groups, func(k usePair, g *group, at string) string {
		return fmt.Sprintf("%s depends on the concrete type %s.%s (%s) although it implements %s: hold the interface (%s)",
			k.from, k.to, k.typ, at, g.extra, pairRule(g.pair, "depend on"))
	})
}

// portOf finds the abstraction the holder could hold instead of t (see
// ConcreteDependencies), as package.Name.
func portOf(t TypeInfo, types TypeIndex, holder string, p LayerPair, calls []string) (string, bool) {
	best, bestN, bestOwn := "", -1, false
	seen := map[string]bool{}
	consider := func(pkg string) {
		if seen[pkg] || (len(calls) == 0 && pkg != holder) {
			return
		}
		seen[pkg] = true
		for _, i := range types.Interfaces(pkg) {
			if i.Ref == t.Ref || i.Embeds || !t.implements(i) {
				continue
			}
			if !slices.ContainsFunc(calls, func(c string) bool { return !slices.Contains(i.Methods, c) }) {
				name, n, own := i.Package+"."+i.Ref.Type, len(i.Methods), i.Package == holder
				// the smallest port covering the calls; without calls the
				// fullest port of the holder's package
				closer := n < bestN
				if len(calls) == 0 {
					closer = n > bestN
				}
				better := bestN < 0 || closer || (n == bestN && own && !bestOwn) || (n == bestN && own == bestOwn && name < best)
				if better {
					best, bestN, bestOwn = name, n, own
				}
			}
		}
	}
	consider(holder)
	consider(t.Package)
	for _, ref := range t.Implements {
		if i, ok := types.Type(ref); ok && p.FromSet.Contains(PackageRef{Key: i.Package}) {
			consider(i.Package)
		}
	}
	return best, bestN >= 0
}

// TypeLeaks reports, per package and type, the members of the API of a
// package (parameters and results of exported functions and methods,
// exported fields holding data) that name a struct or class of a layer an
// abstract pair says the package must use only through interfaces.
// Constructor parameters and held implementations are left to
// ConcreteDependencies. Severity is left to the caller.
func TypeLeaks(uses []MemberUse, types TypeIndex, pairs []LayerPair) []Finding {
	groups := map[usePair]*group{}
	for _, u := range uses {
		if u.Construct || !u.Exported || u.Role == graph.RoleCtor {
			continue // a constructor's parameters are what it is handed: concrete-dependency's
		}
		t, ok := types.Type(u.TypeRef)
		if !ok || t.abstraction() || (t.Kind != "struct" && t.Kind != "class") {
			continue
		}
		if u.Role == graph.RoleField && t.concrete() {
			continue // a held implementation: concrete-dependency's
		}
		k := usePair{u.Package, t.Package, t.Ref.Type}
		if g := groups[k]; g != nil {
			g.uses = append(g.uses, u)
			continue
		}
		if p, ok := pairFor(pairs, u.Package, t.Package); ok {
			groups[k] = &group{pair: p, info: t, uses: []MemberUse{u}}
		}
	}
	return findings(RuleTypeLeak, groups, func(k usePair, g *group, at string) string {
		return fmt.Sprintf("%s exposes %s.%s in its API (%s): use a type of its own layer, or an interface (%s)",
			k.from, k.to, k.typ, at, strings.TrimSuffix(pairRule(g.pair, "use"), " through an interface")+" only through an interface")
	})
}
