package rules

import (
	"maps"
	"slices"
	"sort"
	"strings"
)

// RuleFileCycle: files of one directory that load each other (TypeScript /
// JavaScript modules, Python modules). import-cycle sees directories, so a
// cycle inside one is invisible to it; no compiler reports either. Only
// imports that load a module count: one erased with the types (import type,
// `if TYPE_CHECKING:`) or made on demand (import()) closes no cycle.
const RuleFileCycle = "file-cycle"

func init() {
	builtin = append(builtin, Rule{Name: RuleFileCycle, Severity: SeverityWarn,
		Description: "import cycle between the files of one directory (TypeScript / JavaScript, Python)"})
}

const (
	// fileCycleMax is the longest cycle searched for, in files (as
	// import-cycle's, in packages).
	fileCycleMax = 8
	// fileCycleLimit caps the cycles reported.
	fileCycleLimit = 50
)

// FileCycles returns, for every edge (from loads to) on a cycle of at most
// fileCycleMax files, the shortest cycle through it: each cycle once,
// starting (and ending) at its smallest file, shortest first, at most
// fileCycleLimit of them.
func FileCycles(edges [][2]string) [][]string {
	return shortestCycles(edges, func(_, _ string) bool { return true })
}

// shortestCycles returns, for every edge of the graph that through accepts
// and that lies on a cycle of at most fileCycleMax nodes, the shortest cycle
// through it: each cycle once, starting (and ending) at its smallest node,
// shortest first, at most fileCycleLimit of them.
func shortestCycles(edges [][2]string, through func(from, to string) bool) [][]string {
	next := map[string][]string{}
	for _, e := range edges {
		if e[0] != e[1] && !slices.Contains(next[e[0]], e[1]) {
			next[e[0]] = append(next[e[0]], e[1])
		}
	}
	for _, to := range next {
		sort.Strings(to) // the shortest path chosen does not depend on the input order
	}
	seen := map[string]bool{}
	var out [][]string
	for _, from := range slices.Sorted(maps.Keys(next)) {
		for _, to := range next[from] {
			if !through(from, to) {
				continue
			}
			back := shortestPath(next, to, from, fileCycleMax-1)
			if back == nil {
				continue
			}
			ring := rotateToSmallest(back) // to ... from: every node of the cycle once
			if id := strings.Join(ring, "\x00"); !seen[id] {
				seen[id] = true
				out = append(out, append(ring, ring[0]))
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i]) != len(out[j]) {
			return len(out[i]) < len(out[j])
		}
		return slices.Compare(out[i], out[j]) < 0
	})
	if len(out) > fileCycleLimit {
		out = out[:fileCycleLimit]
	}
	return out
}

// shortestPath is the shortest path from -> ... -> to over next with at
// most maxEdges edges (breadth first), nil when there is none.
func shortestPath(next map[string][]string, from, to string, maxEdges int) []string {
	prev := map[string]string{from: ""} // the node each visited node was reached from
	level := []string{from}
	for depth := 0; depth < maxEdges && len(level) > 0; depth++ {
		var following []string
		for _, n := range level {
			for _, m := range next[n] {
				if _, visited := prev[m]; visited {
					continue
				}
				prev[m] = n
				following = append(following, m)
			}
		}
		if _, reached := prev[to]; reached && to != from {
			return pathTo(prev, to)
		}
		level = following
	}
	return nil
}

// pathTo follows prev back from to and returns the path in walking order.
func pathTo(prev map[string]string, to string) []string {
	var path []string
	for at := to; at != ""; at = prev[at] {
		path = append(path, at)
	}
	slices.Reverse(path)
	return path
}

// rotateToSmallest returns ring starting at its smallest element.
func rotateToSmallest(ring []string) []string {
	s := 0
	for i, x := range ring {
		if x < ring[s] {
			s = i
		}
	}
	return append(append([]string(nil), ring[s:]...), ring[:s]...)
}

// FileCycle reports the cycle files (the first file repeated at the end).
func FileCycle(files []string) Finding {
	return Finding{
		Rule: RuleFileCycle, Severity: string(SeverityWarn),
		Message: strings.Join(files, " -> "),
		Data:    map[string]any{"files": files},
	}
}
