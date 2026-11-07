package graph

import (
	"strconv"
	"strings"
)

// The members model: what the types and functions of a file hold, take,
// return and construct, by type. It is what the rules about the code
// inside the layers read (dependency inversion, types leaking through an
// API): the import graph says a package depends on another, the members
// say on which of its types and how.

// Roles of a TypeUse.
const (
	RoleField  = "field"  // a field of a struct / a property of a class
	RoleCtor   = "ctor"   // a parameter of a constructor (Go New*, constructor, __init__)
	RoleParam  = "param"  // a parameter of another function or method
	RoleResult = "result" // a result of a function or method
)

// TypeRef is a type declared in the repository: Target is a Go package
// directory or a TypeScript / Python file (or package directory), Type its
// name there ("default" for what a TypeScript file exports as default).
type TypeRef struct {
	Target string `json:"target"`
	Type   string `json:"type"`
}

// String is target#Type.
func (r TypeRef) String() string { return r.Target + "#" + r.Type }

// ParseTypeRef is the inverse of TypeRef.String.
func ParseTypeRef(s string) (TypeRef, bool) {
	i := strings.LastIndexByte(s, '#')
	if i <= 0 || i == len(s)-1 {
		return TypeRef{}, false
	}
	return TypeRef{Target: s[:i], Type: s[i+1:]}, true
}

// TypeUse is one place a type of the repository is named by a member: a
// field, a constructor parameter, a parameter or a result.
type TypeUse struct {
	// Owner is the struct / class of a field or constructor, else the
	// function, or the method as Type.Method.
	Owner  string `json:"owner"`
	Member string `json:"member"` // the field or parameter; results are numbered from 0
	Role   string `json:"role"`
	// Exported: the member is part of the package's API (an exported owner
	// and member, a public method of an exported class).
	Exported bool `json:"exported,omitempty"`
	// Collection: the type is the element of a slice / array / map / list.
	Collection bool `json:"collection,omitempty"`
	Line       int  `json:"line"`
	// Calls: the methods the owner calls on the member (a field through
	// its receiver / this / self, a constructor parameter in the
	// constructor), sorted.
	Calls []string `json:"calls,omitempty"`
	TypeRef
}

func boolField(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// Encode returns u as one string (for list properties of the store).
func (u TypeUse) Encode() string {
	return strings.Join([]string{u.Owner, u.Member, u.Role, strconv.Itoa(u.Line), boolField(u.Exported), boolField(u.Collection), u.Target, u.Type,
		strings.Join(u.Calls, ",")}, usesSep)
}

// DecodeTypeUse is the inverse of TypeUse.Encode.
func DecodeTypeUse(s string) (TypeUse, bool) {
	f := strings.Split(s, usesSep)
	if (len(f) != 8 && len(f) != 9) || f[6] == "" || f[7] == "" { // 8: stored before Calls
		return TypeUse{}, false
	}
	switch f[2] {
	case RoleField, RoleCtor, RoleParam, RoleResult:
	default:
		return TypeUse{}, false
	}
	line, err := strconv.Atoi(f[3])
	if err != nil {
		return TypeUse{}, false
	}
	u := TypeUse{Owner: f[0], Member: f[1], Role: f[2], Line: line, Exported: f[4] == "1", Collection: f[5] == "1",
		Target: f[6], Type: f[7]}
	if len(f) == 9 && f[8] != "" {
		u.Calls = strings.Split(f[8], ",")
	}
	return u, true
}

// Construct is a place the file instantiates a type of the repository
// (&q.T{}, new T(), T()), inside Owner (the top-level function, method
// as Type.Method, or class).
type Construct struct {
	Owner string `json:"owner"`
	Line  int    `json:"line"`
	TypeRef
}

// Encode returns c as one string.
func (c Construct) Encode() string {
	return strings.Join([]string{c.Owner, strconv.Itoa(c.Line), c.Target, c.Type}, usesSep)
}

// DecodeConstruct is the inverse of Construct.Encode.
func DecodeConstruct(s string) (Construct, bool) {
	f := strings.Split(s, usesSep)
	if len(f) != 4 || f[2] == "" || f[3] == "" {
		return Construct{}, false
	}
	line, err := strconv.Atoi(f[1])
	if err != nil {
		return Construct{}, false
	}
	return Construct{Owner: f[0], Line: line, Target: f[2], Type: f[3]}, true
}
