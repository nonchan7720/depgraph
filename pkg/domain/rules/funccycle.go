package rules

import "strings"

// RuleFunctionCycle: functions (and methods) declared in different files
// that call each other in a cycle: one piece of logic spread over files
// that cannot be read, tested or changed apart. Recursion inside one file
// (a recursive descent parser, a tree walk) is how such code is written and
// is not reported, and neither is a cycle among the methods of one type,
// whichever files declare them.
//
// Reported for Go, whose packages span files. In TypeScript / JavaScript
// and Python a call across files needs an import, so such a cycle is a
// file-cycle or an import-cycle already.
const RuleFunctionCycle = "function-cycle"

func init() {
	builtin = append(builtin, Rule{Name: RuleFunctionCycle, Severity: SeverityInfo,
		Description: "functions in different files of a Go package that call each other in a cycle"})
}

// FuncNode is a function in the function-level dependency graph.
type FuncNode struct {
	File string // the file that declares it
	Name string // "Run", or "T.Run" for a method
}

const funcNodeSep = "\x00"

func (n FuncNode) id() string { return n.File + funcNodeSep + n.Name }

// FunctionCycles returns, for every call between two files on a cycle of
// calls, the shortest cycle through it (see shortestCycles). A cycle whose
// functions are all in one file has no such call.
func FunctionCycles(calls [][2]FuncNode) [][]FuncNode {
	edges := make([][2]string, len(calls))
	for i, c := range calls {
		edges[i] = [2]string{c[0].id(), c[1].id()}
	}
	fileOf := func(id string) string { f, _, _ := strings.Cut(id, funcNodeSep); return f }
	var out [][]FuncNode
	for _, cyc := range shortestCycles(edges, func(from, to string) bool { return fileOf(from) != fileOf(to) }) {
		nodes := make([]FuncNode, len(cyc))
		for i, id := range cyc {
			nodes[i].File, nodes[i].Name, _ = strings.Cut(id, funcNodeSep)
		}
		if !oneReceiver(nodes) {
			out = append(out, nodes)
		}
	}
	return out
}

// oneReceiver reports whether every function of the cycle is a method of
// the same type: a type declared over several files (a parser and its
// TypeScript part) is one unit, and its methods recurse as they would in
// one file.
func oneReceiver(cycle []FuncNode) bool {
	recv, _, method := strings.Cut(cycle[0].Name, ".")
	if !method {
		return false
	}
	for _, n := range cycle[1:] {
		if r, _, ok := strings.Cut(n.Name, "."); !ok || r != recv {
			return false
		}
	}
	return true
}

// FunctionCycle reports the cycle (the first function repeated at the end).
func FunctionCycle(cycle []FuncNode) Finding {
	parts := make([]string, len(cycle))
	names := make([]string, len(cycle))
	for i, n := range cycle {
		parts[i] = n.File + " " + n.Name
		names[i] = n.File + "#" + n.Name
	}
	return Finding{
		Rule: RuleFunctionCycle, Severity: string(SeverityInfo),
		Message: strings.Join(parts, " -> "),
		Data:    map[string]any{"functions": names},
	}
}
