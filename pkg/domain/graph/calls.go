package graph

import (
	"strconv"
	"strings"
)

// FuncCalls is a function, method or class of a file with the functions it
// calls: the function-level dependency graph, joined across the project by
// the rules (function-cycle).
//
// Name is the caller: a function ("Run"), a method with its receiver or
// class ("T.Run"), a function bound to a constant of a TypeScript /
// JavaScript file. Line is the 1-based line it is declared at.
type FuncCalls struct {
	Name  string `json:"name"`
	Line  int    `json:"line,omitempty"`
	Calls []Call `json:"calls,omitempty"`
	// Forwards: the function only passes its parameters on: its body is one
	// call (the only one of Calls) with exactly its parameters, in order,
	// whose result it returns. Never a method (it may be there to satisfy an
	// interface) or a class.
	Forwards bool `json:"forwards,omitempty"`
	// Ignore are the function-level rules a `depgraph:ignore <rule>` comment
	// of the function exempts it from.
	Ignore []string `json:"ignore,omitempty"`
}

// Call is one function called.
//
// Target is where it is declared: "" for the caller's own file (TypeScript,
// Python) or package (Go), else a file (TypeScript, Python), a package
// directory (Go) or a Python package directory (Name is then resolved
// against its modules, like Use). Name is the function ("default" for a
// default import, "T.M" for a method of the caller's own receiver type, dotted
// for a Python name reached through a module: "sub.f"). A name that is no
// function of Target (a variable holding a function, a conversion) is
// dropped by the rules: only they know every declaration of the project.
type Call struct {
	Target string `json:"target,omitempty"`
	Name   string `json:"name"`
	// Via: the call is a method (Name) called on a member whose type is
	// declared: "Owner|member", Owner and member as in TypeUse (a field of
	// the receiver's type or class: "Svc|repo"; a parameter:
	// "Svc.Run|r", "handle|r"; a constructor's parameter belongs to the
	// class). Target is empty: the rules find the member's type and, for an
	// interface, the types that implement it.
	Via string `json:"via,omitempty"`
}

// ViaSep separates the owner and the member of Call.Via.
const ViaSep = "|"

// OwnMember is a field or parameter (Owner, Member as in TypeUse) whose
// type is declared in the file's own package (Go): the members model
// (TypeUse) only holds the types of other packages, and a Go interface is
// usually declared where it is used.
type OwnMember struct {
	Owner  string `json:"owner"`
	Member string `json:"member"`
	Type   string `json:"type"`
}

// Encode returns m as one string.
func (m OwnMember) Encode() string { return strings.Join([]string{m.Owner, m.Member, m.Type}, usesSep) }

// DecodeOwnMember is the inverse of OwnMember.Encode.
func DecodeOwnMember(s string) (OwnMember, bool) {
	f := strings.Split(s, usesSep)
	if len(f) != 3 || f[0] == "" || f[1] == "" || f[2] == "" {
		return OwnMember{}, false
	}
	return OwnMember{Owner: f[0], Member: f[1], Type: f[2]}, true
}

const callSep = "\x1e"

// Encode returns f as one string (for list properties of the store).
func (f FuncCalls) Encode() string {
	parts := make([]string, 0, 1+len(f.Calls))
	flag := ""
	if f.Forwards {
		flag = "f"
	}
	head := strings.Join([]string{f.Name, flag, strings.Join(f.Ignore, ","), strconv.Itoa(f.Line)}, usesSep)
	parts = append(parts, head)
	for _, c := range f.Calls {
		if c.Via != "" {
			parts = append(parts, c.Target+usesSep+c.Name+usesSep+"v"+c.Via)
			continue
		}
		parts = append(parts, c.Target+usesSep+c.Name)
	}
	return strings.Join(parts, callSep)
}

// DecodeFuncCalls is the inverse of FuncCalls.Encode.
func DecodeFuncCalls(s string) (FuncCalls, bool) {
	parts := strings.Split(s, callSep)
	if len(parts) < 2 {
		return FuncCalls{}, false
	}
	head := strings.Split(parts[0], usesSep) // name, flag, ignored rules, line
	if len(head) != 4 || head[0] == "" {
		return FuncCalls{}, false
	}
	f := FuncCalls{Name: head[0], Forwards: head[1] == "f"}
	if head[2] != "" {
		f.Ignore = strings.Split(head[2], ",")
	}
	f.Line, _ = strconv.Atoi(head[3])
	for _, p := range parts[1:] {
		fields := strings.Split(p, usesSep) // target, name [, "v" + via]
		if len(fields) < 2 || fields[1] == "" {
			return FuncCalls{}, false
		}
		c := Call{Target: fields[0], Name: fields[1]}
		if len(fields) > 2 {
			c.Via = strings.TrimPrefix(fields[2], "v")
		}
		f.Calls = append(f.Calls, c)
	}
	return f, true
}
