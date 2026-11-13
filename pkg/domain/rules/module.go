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

// ModuleSet identifies the modules a ring of package keys goes through:
// the distinct modules, sorted, each followed by a NUL.
func ModuleSet(units, keys []string) string {
	m := map[string]bool{}
	for _, k := range keys {
		m[ModuleOf(units, k)] = true
	}
	set := make([]string, 0, len(m))
	for k := range m {
		set = append(set, k)
	}
	sort.Strings(set)
	return strings.Join(set, "\x00") + "\x00"
}
