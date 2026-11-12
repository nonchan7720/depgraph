package rules

import (
	"fmt"
	"sort"
	"strings"
)

// RulePassThrough: a chain of at least two functions that only pass their
// parameters on to the next (`api.Get -> service.Get -> load`): layers that
// add a name and nothing else, which is how wrappers pile up when code is
// added in front of code instead of changed. One wrapper is an API choice
// and is not reported; methods are never counted (they may be there to
// satisfy an interface).
const RulePassThrough = "pass-through"

func init() {
	builtin = append(builtin, Rule{Name: RulePassThrough, Severity: SeverityWarn,
		Description: "a chain of two or more functions that only pass their parameters on to the next"})
}

// passThroughLimit caps the chains reported.
const passThroughLimit = 50

// PassThroughChains returns the chains of forwards ([from, to]: from only
// passes its parameters on to to) with at least two forwarding functions:
// from a function nothing forwards to, through the functions that only
// forward, to the first one that does something else (the last element).
// A ring of forwarders has no start and no end and is left out.
func PassThroughChains(forwards [][2]FuncNode) [][]FuncNode {
	next := map[FuncNode]FuncNode{}
	target := map[FuncNode]bool{}
	for _, f := range forwards {
		if f[0] != f[1] {
			next[f[0]] = f[1]
			target[f[1]] = true
		}
	}
	var out [][]FuncNode
	for head := range next {
		if target[head] {
			continue // inside a chain
		}
		chain := []FuncNode{head}
		seen := map[FuncNode]bool{head: true}
		for at, ok := next[head]; ok && !seen[at]; at, ok = next[at] {
			chain = append(chain, at)
			seen[at] = true
		}
		// the last element does the work, unless the walk ran into a ring
		if _, forwardsOn := next[chain[len(chain)-1]]; !forwardsOn && len(chain) >= 3 {
			out = append(out, chain)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i][0], out[j][0]
		if a.File != b.File {
			return a.File < b.File
		}
		return a.Name < b.Name
	})
	if len(out) > passThroughLimit {
		out = out[:passThroughLimit]
	}
	return out
}

// PassThrough reports chain: its functions but the last only pass their
// arguments on. Its identity is the first function.
func PassThrough(chain []FuncNode) Finding {
	steps := make([]string, len(chain))
	ids := make([]string, len(chain))
	for i, n := range chain {
		steps[i] = n.File + " " + n.Name
		ids[i] = n.File + "#" + n.Name
	}
	names := make([]string, len(chain)-1)
	for i, n := range chain[:len(chain)-1] {
		names[i] = n.Name
	}
	who := strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	return Finding{
		Rule: RulePassThrough, Severity: string(SeverityWarn),
		ID:      ids[0],
		Message: fmt.Sprintf("%s: %s only pass their arguments on", strings.Join(steps, " -> "), who),
		Data:    map[string]any{"functions": ids},
	}
}
