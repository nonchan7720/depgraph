package rules

import (
	"slices"
	"strings"
	"testing"
)

func TestModuleOf(t *testing.T) {
	units := []string{"lib/auth", "packages/web", "packages/web/e2e", "."}
	for key, want := range map[string]string{
		"packages/web/src/api": "packages/web",
		"packages/web/e2e/x":   "packages/web/e2e",
		"packages/webkit/src":  ".", // whole segments only
		"lib/auth":             "lib/auth",
		"pkg/handler":          ".",
		".":                    ".",
	} {
		if got := ModuleOf(units, key); got != want {
			t.Errorf("ModuleOf(%q) = %q, want %q", key, got, want)
		}
	}
}

func cycleStrings(cs []ModuleCycle) []string {
	var out []string
	for _, c := range cs {
		out = append(out, strings.Join(c.Modules, " -> ")+" via "+strings.Join(c.Imports, ", "))
	}
	return out
}

// Two npm packages that import each other through different directories:
// no directory cycle, so import-cycle cannot see it.
func TestModuleCycles(t *testing.T) {
	units := []string{"packages/web", "packages/kit", "packages/ui", "lib/auth"}
	edges := [][2]string{
		{"packages/web/src/a", "packages/kit/src"},
		{"packages/kit/src", "packages/web/src/b"},
		{"packages/web/src/a", "packages/web/src/b"}, // inside one module: no hop
		{"packages/ui/src", "packages/kit/src"},      // one way: no cycle
		{"pkg/handler", "lib/auth/token"},            // the root module "." and lib/auth
		{"lib/auth/token", "pkg/usecase"},
		{"lib/auth/token", "pkg/usecase/sub"},
	}
	got := cycleStrings(ModuleCycles(edges, units, nil))
	want := []string{
		". -> lib/auth -> . via pkg/handler -> lib/auth/token, lib/auth/token -> pkg/usecase",
		"packages/kit -> packages/web -> packages/kit via packages/kit/src -> packages/web/src/b, packages/web/src/a -> packages/kit/src",
	}
	if !slices.Equal(got, want) {
		t.Errorf("cycles =\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// A module cycle that is the same as a directory cycle import-cycle already
// reports (the same modules) is not reported twice.
func TestModuleCyclesSkipsDirectoryCycles(t *testing.T) {
	units := []string{"packages/web", "packages/kit"}
	edges := [][2]string{
		{"packages/web/src", "packages/kit/src"},
		{"packages/kit/src", "packages/web/src"},
	}
	dirCycles := [][]string{{"packages/kit/src", "packages/web/src", "packages/kit/src"}}
	if got := ModuleCycles(edges, units, dirCycles); len(got) != 0 {
		t.Errorf("reported again: %v", cycleStrings(got))
	}
	if got := ModuleCycles(edges, units, nil); len(got) != 1 {
		t.Errorf("without the directory cycle: %v", cycleStrings(got))
	}
}

// Three modules in a ring, plus a shortcut: the shortest cycle through every
// module import is reported once each.
func TestModuleCyclesRing(t *testing.T) {
	units := []string{"a", "b", "c"}
	edges := [][2]string{{"a/x", "b/x"}, {"b/x", "c/x"}, {"c/x", "a/y"}}
	got := cycleStrings(ModuleCycles(edges, units, nil))
	if want := []string{"a -> b -> c -> a via a/x -> b/x, b/x -> c/x, c/x -> a/y"}; !slices.Equal(got, want) {
		t.Errorf("ring = %v", got)
	}
	// without modules everything is the root module: nothing to report
	if got := ModuleCycles(edges, nil, nil); len(got) != 0 {
		t.Errorf("one module: %v", cycleStrings(got))
	}
}
