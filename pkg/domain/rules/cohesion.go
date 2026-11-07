package rules

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// RuleLowCohesion: a package (Go) or module (TypeScript / Python file)
// made of parts that nothing inside connects and that different code uses:
// more than one responsibility in one place. Linters measure a file's or a
// class's size; only the whole project shows who uses which part.
const RuleLowCohesion = "low-cohesion"

func init() {
	builtin = append(builtin, Rule{Name: RuleLowCohesion, Severity: SeverityInfo,
		Description: "a package / module whose parts nothing inside connects are used by different code: more than one responsibility"})
}

// CohesionUnit is a package or module: its parts (Go files, top-level
// declarations), the links between them inside the unit, and who outside
// uses its names.
type CohesionUnit struct {
	Key   string // package key, or file
	What  string // "package", "module"
	Parts []CohesionPart
	Links [][2]int // indexes of Parts that refer to each other
	// Users: name -> the packages / files outside the unit that use it.
	Users map[string][]string
}

// CohesionPart is a part of a unit and the names it declares.
type CohesionPart struct {
	ID    string
	Names []string
	// Data: the part declares data only (types without behaviour,
	// constants, constructors): a unit of data parts (entities, DTOs) is
	// not reported.
	Data bool
}

// minCohesionNames: a unit with fewer names used from outside is too small
// to be worth splitting.
const minCohesionNames = 3

// LowCohesion reports the units whose parts fall into two or more groups
// that nothing inside connects, each used from outside, where no user uses
// two of the groups. Severity is left to the caller.
func LowCohesion(units []CohesionUnit) []Finding {
	var out []Finding
	for _, u := range units {
		groups := cohesionGroups(u)
		if len(groups) < 2 || !slices.ContainsFunc(groups, func(g cohesionGroup) bool { return !g.data }) {
			continue
		}
		used := 0
		seen := map[string]int{}
		disjoint := true
		for gi, g := range groups {
			used += len(g.names)
			for _, user := range g.users {
				if other, ok := seen[user]; ok && other != gi {
					disjoint = false
				}
				seen[user] = gi
			}
		}
		if !disjoint || used < minCohesionNames {
			continue
		}
		parts := make([]string, len(groups))
		data := make([]map[string]any, len(groups))
		for i, g := range groups {
			parts[i] = fmt.Sprintf("%s (%s) used by %s", strings.Join(g.parts, ", "), strings.Join(g.names, ", "), strings.Join(g.users, ", "))
			data[i] = map[string]any{"parts": g.parts, "names": g.names, "users": g.users}
		}
		out = append(out, Finding{
			Rule: RuleLowCohesion, ID: u.Key,
			Message: fmt.Sprintf("%s %s has %d parts nothing inside connects, used by different code: %s; split it along them",
				u.What, u.Key, len(groups), strings.Join(parts, "; ")),
			Data: map[string]any{"pkg": u.Key, "groups": data},
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

type cohesionGroup struct {
	parts, names, users []string
	data                bool // every part is data
}

// cohesionGroups are the connected groups of parts of u that are used from
// outside, sorted by their first part.
func cohesionGroups(u CohesionUnit) []cohesionGroup {
	parent := make([]int, len(u.Parts))
	for i := range parent {
		parent[i] = i
	}
	find := func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	for _, l := range u.Links {
		if l[0] < 0 || l[1] < 0 || l[0] >= len(parent) || l[1] >= len(parent) {
			continue
		}
		parent[find(l[0])] = find(l[1])
	}
	byRoot := map[int]*cohesionGroup{}
	for i, p := range u.Parts {
		r := find(i)
		g := byRoot[r]
		if g == nil {
			g = &cohesionGroup{data: true}
			byRoot[r] = g
		}
		g.data = g.data && p.Data
		g.parts = append(g.parts, p.ID)
		for _, n := range p.Names {
			if users := u.Users[n]; len(users) > 0 {
				g.names = append(g.names, n)
				g.users = append(g.users, users...)
			}
		}
	}
	var out []cohesionGroup
	for _, g := range byRoot {
		if len(g.names) == 0 {
			continue
		}
		sort.Strings(g.parts)
		sort.Strings(g.names)
		sort.Strings(g.users)
		g.users = slices.Compact(g.users)
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].parts[0] < out[j].parts[0] })
	return out
}
