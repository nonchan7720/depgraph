package rules

import (
	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// functionCycles reports the cycles of calls across the files of the Go
// packages of the call graph.
func functionCycles(g *callGraph, cfg rules.Config) []rules.Finding {
	var edges [][2]rules.FuncNode
	for _, c := range g.callers {
		if c.lang != string(graph.Go) {
			continue
		}
		for _, call := range c.calls.Calls {
			for _, to := range g.callees(c, call) {
				edges = append(edges, [2]rules.FuncNode{c.node(), to})
			}
		}
	}
	var out []rules.Finding
	for _, cyc := range rules.FunctionCycles(edges) {
		f := rules.FunctionCycle(cyc)
		f.Severity = string(cfg.Severity(rules.RuleFunctionCycle))
		out = append(out, f)
	}
	return out
}

// passThrough reports the chains of functions that only forward. A
// forward whose target is not exactly one function of the project ends no
// chain and starts none.
func passThrough(g *callGraph, cfg rules.Config) []rules.Finding {
	var forwards [][2]rules.FuncNode
	for _, c := range g.callers {
		if !c.calls.Forwards || len(c.calls.Calls) != 1 {
			continue
		}
		if to := g.callees(c, c.calls.Calls[0]); len(to) == 1 {
			forwards = append(forwards, [2]rules.FuncNode{c.node(), to[0]})
		}
	}
	var out []rules.Finding
	for _, chain := range rules.PassThroughChains(forwards) {
		f := rules.PassThrough(chain)
		f.Severity = string(cfg.Severity(rules.RulePassThrough))
		out = append(out, f)
	}
	return out
}
