package graph

import (
	"strconv"
	"strings"
)

// TypeDep is a dependency a struct or class holds on a type declared in
// another package of the repository: the type of a field, or of a
// constructor parameter (a Go New* function, a TypeScript constructor,
// Python's __init__). It is what dependency injection hands in, so it says
// whether the holder depends on an abstraction or on an implementation.
type TypeDep struct {
	// Holder is the struct / class holding the dependency (for a Go
	// constructor, the function).
	Holder string `json:"holder"`
	// Member is the field or parameter.
	Member string `json:"member"`
	Line   int    `json:"line"`
	// Target is where the type is declared, repo-relative: a Go package
	// directory, or a TypeScript / Python file (or package directory).
	Target string `json:"target"`
	// Type is the type's name in Target.
	Type string `json:"type"`
}

// Encode returns d as one string (for list properties of the store).
func (d TypeDep) Encode() string {
	return strings.Join([]string{d.Holder, d.Member, strconv.Itoa(d.Line), d.Target, d.Type}, usesSep)
}

// DecodeTypeDep is the inverse of TypeDep.Encode.
func DecodeTypeDep(s string) (TypeDep, bool) {
	f := strings.Split(s, usesSep)
	if len(f) != 5 || f[3] == "" || f[4] == "" {
		return TypeDep{}, false
	}
	line, err := strconv.Atoi(f[2])
	if err != nil {
		return TypeDep{}, false
	}
	return TypeDep{Holder: f[0], Member: f[1], Line: line, Target: f[3], Type: f[4]}, true
}
