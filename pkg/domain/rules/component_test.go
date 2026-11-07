package rules

import (
	"slices"
	"strings"
	"testing"
)

func TestPrefixGlob(t *testing.T) {
	for _, tc := range []struct{ dir, glob, want string }{
		{"backend", "internal/domain/**", "backend/internal/domain/**"},
		{"backend", "./internal/", "backend/internal"},
		{"backend", "**/db/**", "backend/**/db/**"},
		{"backend", "**", "backend/**"},
		{"backend", ".", "backend"},
		{"apps/web", "/src/**", "apps/web/src/**"},
		{".", "pkg/**", "pkg/**"},
	} {
		if got := PrefixGlob(tc.dir, tc.glob); got != tc.want {
			t.Errorf("PrefixGlob(%q, %q) = %q, want %q", tc.dir, tc.glob, got, tc.want)
		}
	}
}

func violations(t *testing.T, c Config, from, to string) []string {
	t.Helper()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	pairs, err := c.LayerPairs()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, p := range pairs {
		if p.Reports(PackageRef{Key: from}, PackageRef{Key: to}) {
			out = append(out, strings.TrimPrefix(p.Component+":", ":")+p.From+"->"+p.To)
		}
	}
	slices.Sort(out)
	return out
}

// A monorepo: a Go backend and a TypeScript frontend, each with its own
// layers (relative to its directory), under the default root layers.
func monorepo() Config {
	c := DefaultConfig()
	c.Components = []Component{
		{
			Dir: "backend",
			Layers: []Layer{
				{Name: "domain", Packages: []string{"internal/domain/**"}},
				{Name: "usecase", Packages: []string{"internal/usecase/**"}},
				{Name: "handler", Packages: []string{"internal/handler/**"}},
			},
			ForbiddenLayers: [][2]string{{"domain", "usecase"}, {"domain", "handler"}, {"usecase", "handler"}},
		},
		{
			Dir: "frontend",
			Layers: []Layer{
				{Name: "ui", Packages: []string{"src/components/**"}},
				{Name: "api", Packages: []string{"src/api/**"}},
			},
			ForbiddenLayers: [][2]string{{"api", "ui"}},
		},
	}
	return c
}

func TestLayerPairsComponents(t *testing.T) {
	c := monorepo()
	for _, tc := range []struct {
		from, to string
		want     []string
	}{
		// each component's layers apply inside it
		{"backend/internal/domain/user", "backend/internal/usecase", []string{"backend:domain->usecase"}},
		{"frontend/src/api", "frontend/src/components/button", []string{"frontend:api->ui"}},
		{"backend/internal/handler", "backend/internal/usecase", nil},
		// a component's globs never match another directory with the same layout
		{"frontend/internal/domain", "frontend/internal/usecase", nil},
		// the default layers are a guess: not applied inside a component that
		// declares its own (backend has a handler and a repository segment here)
		{"backend/handler", "backend/repository", nil},
		// ... nor to an import into one
		{"tools/handler", "backend/repository", nil},
		// outside every component the defaults still apply
		{"tools/handler", "tools/repository", []string{"handler->repository"}},
	} {
		if got := violations(t, c, tc.from, tc.to); !slices.Equal(got, tc.want) {
			t.Errorf("%s -> %s: %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
}

// Layers the root declares are the repository's own architecture: they
// apply across and inside components (apps must not import each other).
func TestLayerPairsExplicitRootLayersApplyEverywhere(t *testing.T) {
	c := monorepo()
	c.DefaultLayers = false
	c.Layers = []Layer{
		{Name: "backend", Packages: []string{"backend/**"}},
		{Name: "frontend", Packages: []string{"frontend/**"}},
	}
	c.ForbiddenLayers = [][2]string{{"frontend", "backend"}}
	c.AbstractLayers = nil // the default pairs name the default layers
	if got := violations(t, c, "frontend/src/api", "backend/internal/domain"); !slices.Equal(got, []string{"frontend->backend"}) {
		t.Errorf("cross-component import: %v", got)
	}
	if got := violations(t, c, "backend/internal/domain/user", "backend/internal/usecase"); !slices.Equal(got, []string{"backend:domain->usecase"}) {
		t.Errorf("inside a component: %v", got)
	}
}

// A component that sets only forbidden uses the default layers, scoped to
// its directory; a component nested in it with its own layers is left out
// of them.
func TestLayerPairsNestedComponentWithDefaultLayers(t *testing.T) {
	c := DefaultConfig()
	def := DefaultConfig()
	c.Components = []Component{
		{Dir: "services/api", Layers: def.Layers, DefaultLayers: true, ForbiddenLayers: [][2]string{{"usecase", "handler"}}},
		{Dir: "services/api/plugins/x", Layers: []Layer{{Name: "core", Packages: []string{"core/**"}}, {Name: "ext", Packages: []string{"ext/**"}}},
			ForbiddenLayers: [][2]string{{"core", "ext"}}},
	}
	for _, tc := range []struct {
		from, to string
		want     []string
	}{
		{"services/api/usecase", "services/api/handler", []string{"services/api:usecase->handler"}},
		// the component replaced the default forbidden pairs
		{"services/api/handler", "services/api/repository", nil},
		{"services/api/plugins/x/usecase", "services/api/plugins/x/handler", nil},
		{"services/api/plugins/x/core", "services/api/plugins/x/ext", []string{"services/api/plugins/x:core->ext"}},
		{"cmd/handler", "cmd/repository", []string{"handler->repository"}},
	} {
		if got := violations(t, c, tc.from, tc.to); !slices.Equal(got, tc.want) {
			t.Errorf("%s -> %s: %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
}

func TestValidateComponents(t *testing.T) {
	for name, tc := range map[string]struct {
		comp Component
		want string
	}{
		"empty dir":      {Component{Layers: []Layer{{Name: "a", Packages: []string{"a/**"}}}}, "component without a directory"},
		"root dir":       {Component{Dir: ".", Layers: []Layer{{Name: "a", Packages: []string{"a/**"}}}}, `component "."`},
		"** in dir":      {Component{Dir: "apps/**"}, `component "apps/**"`},
		"unknown layer":  {Component{Dir: "web", Layers: []Layer{{Name: "a", Packages: []string{"a/**"}}}, ForbiddenLayers: [][2]string{{"a", "b"}}}, `component web: forbidden a -> b: unknown layer "b"`},
		"bad layer glob": {Component{Dir: "web", Layers: []Layer{{Name: "a", Packages: []string{"a**"}}}}, "component web: layer \"a\""},
	} {
		c := DefaultConfig()
		c.Components = []Component{tc.comp}
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to contain %q", name, err, tc.want)
		}
	}
	c := DefaultConfig()
	c.Components = []Component{{Dir: "web"}, {Dir: "web"}}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("duplicate component: %v", err)
	}
}

// A component whose directory is a pattern (packages/*) is one scope per
// directory it matches: the layers of packages/ui never meet those of
// packages/api, and a nested file for one of them replaces the pattern.
func TestExpandComponents(t *testing.T) {
	c := DefaultConfig()
	layered := []Layer{{Name: "ui", Packages: []string{"src/components/**"}}, {Name: "api", Packages: []string{"src/api/**"}}}
	c.Components = []Component{
		{Dir: "packages/*", Layers: layered, ForbiddenLayers: [][2]string{{"api", "ui"}}},
		{Dir: "packages/legacy", Layers: []Layer{{Name: "x", Packages: []string{"x/**"}}, {Name: "y", Packages: []string{"y/**"}}}, ForbiddenLayers: [][2]string{{"x", "y"}}},
	}
	keys := []string{
		".", "pkg/handler", "packages", "packages/ui/src/api", "packages/ui/src/components/button",
		"packages/web/src/api", "packages/legacy/src/api", "packages/legacy/src/components",
	}
	e := c.ExpandComponents(keys)
	var dirs []string
	for _, comp := range e.Components {
		dirs = append(dirs, comp.Dir)
	}
	if want := []string{"packages/legacy", "packages/ui", "packages/web"}; !slices.Equal(dirs, want) {
		t.Fatalf("expanded components = %v, want %v", dirs, want)
	}
	for _, tc := range []struct {
		from, to string
		want     []string
	}{
		{"packages/ui/src/api", "packages/ui/src/components/button", []string{"packages/ui:api->ui"}},
		// two packages matched by one pattern are two scopes
		{"packages/web/src/api", "packages/ui/src/components/button", nil},
		// the nested file of packages/legacy replaced the pattern there
		{"packages/legacy/src/api", "packages/legacy/src/components", nil},
	} {
		if got := violations(t, e, tc.from, tc.to); !slices.Equal(got, tc.want) {
			t.Errorf("%s -> %s: %v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
	// a configuration without patterns is returned as is
	if got := monorepo().ExpandComponents(keys); len(got.Components) != 2 {
		t.Errorf("concrete components changed: %+v", got.Components)
	}
}
