package rules

import (
	"sort"
	"strings"
)

// RuleModuleCycle: an import cycle between the modules of a monorepo (Go
// modules, npm packages, Python projects, Ruby applications / gems /
// packwerk packs, components), which import-cycle
// does not see when the directories involved do not form a cycle.
const RuleModuleCycle = "module-cycle"

func init() {
	builtin = append(builtin, Rule{Name: RuleModuleCycle, Severity: SeverityError,
		Description: "import cycle between the modules of a monorepo (Go modules, npm packages, Python projects, Ruby gems / packwerk packs, components)"})
}

// ModuleOf returns the deepest of units (module directories) that contains
// package key, by whole path segments; "." (the root module) when none does.
func ModuleOf(units []string, key string) string {
	best := ""
	for _, u := range units {
		if u == "." || u == "" {
			continue
		}
		if (key == u || strings.HasPrefix(key, u+"/")) && len(u) > len(best) {
			best = u
		}
	}
	if best == "" {
		return "."
	}
	return best
}

// ModuleCycle is a cycle between modules.
type ModuleCycle struct {
	// Modules: the ring, starting at its smallest module and repeating it
	// at the end.
	Modules []string
	// Imports: for every hop, one package import that makes it ("a -> b").
	Imports []string
}

// maxModuleCycle and moduleCycleLimit bound the search like import-cycle's.
const (
	maxModuleCycle   = 8
	moduleCycleLimit = 50
)

// ModuleCycles finds, for every import between two modules, the shortest
// cycle of modules through it (at most 8 modules, up to 50 cycles). edges are
// package imports (from, to keys), units the module directories. A cycle of
// the same modules as one of dirCycles (package key rings import-cycle
// reports) is left out: it is already reported.
func ModuleCycles(edges [][2]string, units []string, dirCycles [][]string) []ModuleCycle {
	type hop struct{ from, to string }
	example := map[hop][2]string{}
	adj := map[string]map[string]bool{}
	for _, e := range edges {
		a, b := ModuleOf(units, e[0]), ModuleOf(units, e[1])
		if a == b {
			continue
		}
		h := hop{a, b}
		if ex, ok := example[h]; !ok || e[0] < ex[0] || (e[0] == ex[0] && e[1] < ex[1]) {
			example[h] = e
		}
		if adj[a] == nil {
			adj[a] = map[string]bool{}
		}
		adj[a][b] = true
	}
	known := map[string]bool{}
	for _, c := range dirCycles {
		known[moduleSet(units, c)] = true
	}
	// shortest path from -> to, at most maxLen hops (BFS, deterministic)
	shortest := func(from, to string, maxLen int) []string {
		prev := map[string]string{from: ""}
		frontier := []string{from}
		for depth := 0; depth < maxLen && len(frontier) > 0; depth++ {
			var next []string
			for _, n := range frontier {
				outs := make([]string, 0, len(adj[n]))
				for m := range adj[n] {
					outs = append(outs, m)
				}
				sort.Strings(outs)
				for _, m := range outs {
					if _, seen := prev[m]; seen {
						continue
					}
					prev[m] = n
					if m == to {
						var path []string
						for x := m; x != ""; x = prev[x] {
							path = append([]string{x}, path...)
						}
						return path
					}
					next = append(next, m)
				}
			}
			frontier = next
		}
		return nil
	}
	hops := make([]hop, 0, len(example))
	for h := range example {
		hops = append(hops, h)
	}
	sort.Slice(hops, func(i, j int) bool {
		if hops[i].from != hops[j].from {
			return hops[i].from < hops[j].from
		}
		return hops[i].to < hops[j].to
	})
	seen := map[string]bool{}
	var out []ModuleCycle
	for _, h := range hops {
		back := shortest(h.to, h.from, maxModuleCycle-1)
		if back == nil {
			continue
		}
		ring := append([]string{h.from}, back[:len(back)-1]...)
		s := 0
		for i := range ring {
			if ring[i] < ring[s] {
				s = i
			}
		}
		ring = append(ring[s:], ring[:s]...)
		key := strings.Join(ring, "\x00")
		if seen[key] {
			continue
		}
		seen[key] = true
		set := append([]string(nil), ring...)
		sort.Strings(set)
		if known[strings.Join(set, "\x00")] {
			continue
		}
		c := ModuleCycle{Modules: append(append([]string(nil), ring...), ring[0])}
		for i := range ring {
			e := example[hop{ring[i], ring[(i+1)%len(ring)]}]
			c.Imports = append(c.Imports, e[0]+" -> "+e[1])
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].Modules) != len(out[j].Modules) {
			return len(out[i].Modules) < len(out[j].Modules)
		}
		return strings.Join(out[i].Modules, "\x00") < strings.Join(out[j].Modules, "\x00")
	})
	if len(out) > moduleCycleLimit {
		out = out[:moduleCycleLimit]
	}
	return out
}

// moduleSet is the sorted, distinct modules of a package key ring.
func moduleSet(units, keys []string) string {
	m := map[string]bool{}
	for _, k := range keys {
		m[ModuleOf(units, k)] = true
	}
	set := make([]string, 0, len(m))
	for k := range m {
		set = append(set, k)
	}
	sort.Strings(set)
	return strings.Join(set, "\x00")
}
