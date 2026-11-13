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

// DataMethods are the methods values implement for the standard library
// (see IsDataMethod), sorted.
func DataMethods() []string {
	out := make([]string, 0, len(goDataMethods))
	for m := range goDataMethods {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// MemberUse is a graph.TypeUse (or a graph.Construct) with where it is.
type MemberUse struct {
	File, Package string
	graph.TypeUse
	Construct bool
}

func pairRule(p LayerPair, what string) string {
	r := fmt.Sprintf("%s must %s %s through an interface", p.From, what, p.To)
	if p.Component != "" {
		r = p.Component + ": " + r
	}
	return r
}

// MemberFinding is the finding of concrete-dependency or type-leak for the
// members (where each is, sorted by file and line) of package from that
// name the type typ of package to, under the abstract pair p. port is the
// abstraction the type implements that from could hold instead
// (concrete-dependency). Its identity is the packages and the type.
func MemberFinding(rule, from, to, typ string, members []string, p LayerPair, port string) Finding {
	at := members[0]
	if n := len(members) - 1; n > 0 {
		at += fmt.Sprintf(", and %d more", n)
	}
	data := map[string]any{"from": from, "to": to, "type": typ, "fromLayer": p.From, "toLayer": p.To, "members": members}
	if p.Component != "" {
		data["component"] = p.Component
	}
	var msg string
	if rule == RuleConcreteDependency {
		data["abstraction"] = port
		msg = fmt.Sprintf("%s depends on the concrete type %s.%s (%s) although it implements %s: hold the interface (%s)",
			from, to, typ, at, port, pairRule(p, "depend on"))
	} else {
		msg = fmt.Sprintf("%s exposes %s.%s in its API (%s): use a type of its own layer, or an interface (%s)",
			from, to, typ, at, strings.TrimSuffix(pairRule(p, "use"), " through an interface")+" only through an interface")
	}
	return Finding{Rule: rule, ID: fmt.Sprintf("%s -> %s.%s", from, to, typ), Message: msg, Data: data}
}
