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
		target, name, ok := strings.Cut(p, usesSep)
		if !ok || name == "" {
			return FuncCalls{}, false
		}
		f.Calls = append(f.Calls, Call{Target: target, Name: name})
	}
	return f, true
}
