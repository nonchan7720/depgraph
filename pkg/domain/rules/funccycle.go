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
