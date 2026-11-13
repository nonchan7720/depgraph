package rules

import (
	"fmt"
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

// MinCohesionNames: a unit with fewer names used from outside is too small
// to be worth splitting.
const MinCohesionNames = 3

// CohesionGroup is a group of parts of a unit that refer to each other,
// with its names used from outside and who uses them (all sorted).
type CohesionGroup struct {
	Parts, Names, Users []string
}

// LowCohesion reports the unit key (what: "package", "module") whose parts
// fall into groups that nothing inside connects, each used from outside by
// different code. Severity is left to the caller.
func LowCohesion(key, what string, groups []CohesionGroup) Finding {
	parts := make([]string, len(groups))
	data := make([]map[string]any, len(groups))
	for i, g := range groups {
		parts[i] = fmt.Sprintf("%s (%s) used by %s", strings.Join(g.Parts, ", "), strings.Join(g.Names, ", "), strings.Join(g.Users, ", "))
		data[i] = map[string]any{"parts": g.Parts, "names": g.Names, "users": g.Users}
	}
	return Finding{
		Rule: RuleLowCohesion, ID: key,
		Message: fmt.Sprintf("%s %s has %d parts nothing inside connects, used by different code: %s; split it along them",
			what, key, len(groups), strings.Join(parts, "; ")),
		Data: map[string]any{"pkg": key, "groups": data},
	}
}
