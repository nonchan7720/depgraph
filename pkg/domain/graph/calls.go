package graph

import "strings"

// FuncCalls is a function, method or class of a file with the functions it
// calls: the function-level dependency graph, joined across the project by
// the rules (function-cycle).
//
// Name is the caller: a function ("Run"), a Go method with its receiver
// ("T.Run"), a top-level declaration of a TypeScript / Python file (a class
// stands for its methods).
type FuncCalls struct {
	Name  string `json:"name"`
	Calls []Call `json:"calls,omitempty"`
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
	parts = append(parts, f.Name)
	for _, c := range f.Calls {
		parts = append(parts, c.Target+usesSep+c.Name)
	}
	return strings.Join(parts, callSep)
}

// DecodeFuncCalls is the inverse of FuncCalls.Encode.
func DecodeFuncCalls(s string) (FuncCalls, bool) {
	parts := strings.Split(s, callSep)
	if len(parts) < 2 || parts[0] == "" {
		return FuncCalls{}, false
	}
	f := FuncCalls{Name: parts[0]}
	for _, p := range parts[1:] {
		target, name, ok := strings.Cut(p, usesSep)
		if !ok || name == "" {
			return FuncCalls{}, false
		}
		f.Calls = append(f.Calls, Call{Target: target, Name: name})
	}
	return f, true
}
