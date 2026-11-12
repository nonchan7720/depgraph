package rules

import (
	"path"
	"slices"
	"sort"
	"strings"

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
		if !c.calls.Forwards || len(c.calls.Calls) != 1 || rules.IgnoresRule(c.calls.Ignore, rules.RulePassThrough) {
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

// wideFunctions reports the functions that call functions of more
// packages (directories) of the repository than the limit, their own not
// counted. A function with a `depgraph:ignore wide-function` comment is
// left out.
func wideFunctions(g *callGraph, cfg rules.Config) []rules.Finding {
	limit := cfg.CalledPackagesLimit()
	var out []rules.Finding
	for _, c := range g.callers {
		if len(c.calls.Calls) <= limit || rules.IgnoresRule(c.calls.Ignore, rules.RuleWideFunction) {
			continue
		}
		pkgs := map[string]bool{}
		for _, call := range c.calls.Calls {
			for _, to := range g.callees(c, call) {
				if dir := path.Dir(to.File); dir != path.Dir(c.file) {
					pkgs[dir] = true
				}
			}
		}
		if len(pkgs) <= limit {
			continue
		}
		list := make([]string, 0, len(pkgs))
		for p := range pkgs {
			list = append(list, p)
		}
		sort.Strings(list)
		f := rules.WideFunction(c.file, c.calls.Line, c.calls.Name, list, limit)
		f.Severity = string(cfg.Severity(rules.RuleWideFunction))
		out = append(out, f)
	}
	return out
}

// misplacedFunctions reports the functions that use nothing of their own
// package and several names of exactly one other package. Not a Go main
// or init function (run, not used).
func misplacedFunctions(g *callGraph, cfg rules.Config) []rules.Finding {
	minNames := cfg.MisplacedNamesLimit()
	var out []rules.Finding
	for _, c := range g.callers {
		if rules.IgnoresRule(c.calls.Ignore, rules.RuleMisplacedFunction) || c.calls.Name == "main" || c.calls.Name == "init" {
			continue
		}
		use := rules.FuncUse{Method: strings.Contains(c.calls.Name, "."), Other: map[string][]string{}}
		own := path.Dir(c.file)
		for _, ref := range slices.Concat(c.calls.Calls, c.calls.Refs) {
			pkg, name, ok := g.used(c, ref)
			switch {
			case !ok:
			case pkg == own:
				use.Own++
			case !slices.Contains(use.Other[pkg], name):
				use.Other[pkg] = append(use.Other[pkg], name)
			}
		}
		where := rules.Misplaced(use, minNames)
		if where == "" {
			continue
		}
		names := use.Other[where]
		sort.Strings(names)
		f := rules.MisplacedFunction(c.file, c.calls.Line, c.calls.Name, where, names)
		f.Severity = string(cfg.Severity(rules.RuleMisplacedFunction))
		out = append(out, f)
	}
	return out
}
