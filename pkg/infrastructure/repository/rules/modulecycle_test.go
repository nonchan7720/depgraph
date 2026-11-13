package rules

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// moduleCycles seeds the package imports (from -> to) and returns the
// messages of module-cycle with units as the modules.
func moduleCycles(t *testing.T, name string, units []string, edges [][2]string) []string {
	t.Helper()
	ctx, st := connect(t)
	key := fmt.Sprintf("zz_test_%d_%s", time.Now().UnixNano(), name)
	pkgs := map[string][]string{}
	for _, e := range edges {
		pkgs[e[0]] = append(pkgs[e[0]], e[1])
		if _, ok := pkgs[e[1]]; !ok {
			pkgs[e[1]] = nil
		}
	}
	seed(ctx, t, st, key, pkgs)
	cfg := rules.DefaultConfig()
	cfg.Modules = units
	fs, err := New(st, key).Evaluate(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range byRule(fs, rules.RuleModuleCycle) {
		out = append(out, f.Message)
	}
	return out
}

// Two npm packages that import each other through different directories:
// no directory cycle, so import-cycle cannot see it.
func TestModuleCycles(t *testing.T) {
	got := moduleCycles(t, "modules", []string{"packages/web", "packages/kit", "packages/ui", "lib/auth"}, [][2]string{
		{"packages/web/src/a", "packages/kit/src"},
		{"packages/kit/src", "packages/web/src/b"},
		{"packages/web/src/a", "packages/web/src/b"}, // inside one module: no hop
		{"packages/ui/src", "packages/kit/src"},      // one way: no cycle
		{"pkg/handler", "lib/auth/token"},            // the root module "." and lib/auth
		{"lib/auth/token", "pkg/usecase"},
		{"lib/auth/token", "pkg/usecase/sub"},
	})
	want := []string{
		". -> lib/auth -> . (pkg/handler -> lib/auth/token, lib/auth/token -> pkg/usecase)",
		"packages/kit -> packages/web -> packages/kit (packages/kit/src -> packages/web/src/b, packages/web/src/a -> packages/kit/src)",
	}
	if !slices.Equal(got, want) {
		t.Errorf("cycles =\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
}

// A module cycle that is the same as a directory cycle import-cycle already
// reports (the same modules) is not reported twice.
func TestModuleCyclesSkipsDirectoryCycles(t *testing.T) {
	got := moduleCycles(t, "modules_dir", []string{"packages/web", "packages/kit"}, [][2]string{
		{"packages/web/src", "packages/kit/src"},
		{"packages/kit/src", "packages/web/src"},
	})
	if len(got) != 0 {
		t.Errorf("reported again: %v", got)
	}
}

// Three modules in a ring: the shortest cycle through every module import
// is reported once. Without modules everything is the root module.
func TestModuleCyclesRing(t *testing.T) {
	edges := [][2]string{{"a/x", "b/x"}, {"b/x", "c/x"}, {"c/x", "a/y"}}
	got := moduleCycles(t, "modules_ring", []string{"a", "b", "c"}, edges)
	if want := []string{"a -> b -> c -> a (a/x -> b/x, b/x -> c/x, c/x -> a/y)"}; !slices.Equal(got, want) {
		t.Errorf("ring = %v", got)
	}
	if got := moduleCycles(t, "modules_none", nil, edges); len(got) != 0 {
		t.Errorf("one module: %v", got)
	}
}
