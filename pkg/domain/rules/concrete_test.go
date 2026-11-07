package rules

import (
	"strings"
	"testing"
)

func cleanArch() Config {
	c := DefaultConfig()
	c.DefaultLayers = false
	c.Layers = []Layer{
		{Name: "domain", Packages: []string{"app/domain/**"}},
		{Name: "usecase", Packages: []string{"app/usecase/**"}},
		{Name: "repository", Packages: []string{"app/repository/**"}},
	}
	c.ForbiddenLayers = nil
	c.AbstractLayers = [][2]string{{"usecase", "repository"}}
	return c
}

func held(member, target, typ string, shape TypeShape) HeldDependency {
	return HeldDependency{File: "app/usecase/user.go", Package: "app/usecase", Holder: "UserUsecase", Member: member, Line: 7,
		TargetPackage: target, Type: typ, Shape: shape}
}

var (
	service = TypeShape{Kind: "struct", Methods: 2}
	data    = TypeShape{Kind: "struct"}
	port    = TypeShape{Kind: "interface"}
)

func TestConcreteDependencies(t *testing.T) {
	c := cleanArch()
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	pairs, err := c.AbstractPairs()
	if err != nil {
		t.Fatal(err)
	}
	deps := []HeldDependency{
		held("repo", "app/repository", "MySQLUserRepo", service),
		// an interface of the same package is what the rule asks for
		held("users", "app/repository", "UserRepository", port),
		// a struct without methods is data (a row, an option set), not a service
		held("opts", "app/repository", "Options", data),
		// an abstract class is an abstraction
		held("base", "app/repository", "BaseRepo", TypeShape{Kind: "class", Methods: 3, Abstract: true}),
		// a layer the pair does not name
		held("user", "app/domain", "User", service),
		// the same concrete type through a second member: one finding
		{File: "app/usecase/user.go", Package: "app/usecase", Holder: "NewUserUsecase", Member: "repo", Line: 12,
			TargetPackage: "app/repository", Type: "MySQLUserRepo", Shape: service},
	}
	got := ConcreteDependencies(deps, pairs)
	if len(got) != 1 {
		t.Fatalf("findings = %+v", got)
	}
	f := got[0]
	if f.Rule != RuleConcreteDependency || f.ID != "app/usecase -> app/repository.MySQLUserRepo" {
		t.Errorf("finding = %+v", f)
	}
	for _, want := range []string{"app/usecase depends on the concrete type app/repository.MySQLUserRepo",
		"app/usecase/user.go:7 UserUsecase.repo", "and 1 more", "usecase must depend on repository through an interface"} {
		if !strings.Contains(f.Message, want) {
			t.Errorf("message %q lacks %q", f.Message, want)
		}
	}
	if members, _ := f.Data["members"].([]string); len(members) != 2 {
		t.Errorf("members = %v", f.Data["members"])
	}
	if f.Data["from"] != "app/usecase" || f.Data["to"] != "app/repository" {
		t.Errorf("data = %v", f.Data)
	}
}

// A type of the holder's own layer (another use case) is not reported, and
// neither is a type in both layers of the pair (the import does not cross it).
func TestConcreteDependenciesSameLayer(t *testing.T) {
	c := cleanArch()
	c.Layers = append(c.Layers, Layer{Name: "shared", Packages: []string{"app/usecase/shared/**", "app/repository/shared/**"}})
	pairs, err := c.AbstractPairs()
	if err != nil {
		t.Fatal(err)
	}
	if got := ConcreteDependencies([]HeldDependency{held("x", "app/usecase/order", "OrderUsecase", service)}, pairs); len(got) != 0 {
		t.Errorf("same layer: %+v", got)
	}
}

func TestDefaultAbstractPairs(t *testing.T) {
	c := DefaultConfig()
	pairs, err := c.AbstractPairs()
	if err != nil {
		t.Fatal(err)
	}
	got := ConcreteDependencies([]HeldDependency{
		{File: "internal/usecase/user.go", Package: "internal/usecase", Holder: "U", Member: "r", Line: 1,
			TargetPackage: "internal/infra/mysql", Type: "Repo", Shape: service},
		{File: "internal/usecase/user.go", Package: "internal/usecase", Holder: "U", Member: "d", Line: 2,
			TargetPackage: "internal/domain", Type: "Users", Shape: service},
	}, pairs)
	if len(got) != 1 || got[0].Data["to"] != "internal/infra/mysql" {
		t.Errorf("default pairs: %+v", got)
	}
}

func TestLargePackages(t *testing.T) {
	c := DefaultConfig()
	c.DefaultLayers = false
	c.Layers = []Layer{{Name: "interfaces", Packages: []string{"pkg/interfaces/**"}}, {Name: "domain", Packages: []string{"pkg/domain/**"}}}
	c.ForbiddenLayers, c.AbstractLayers = nil, nil
	c.LargePackage.MaxLines = 1000
	c.LargePackage.Layers = map[string]int{"interfaces": 300}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	got, err := LargePackages([]PackageSize{
		{Key: "pkg/interfaces/cmd", Lines: 400, Files: 3, Go: true},  // over the layer's limit
		{Key: "pkg/interfaces/ui", Lines: 200, Files: 2, Go: true},   // under it
		{Key: "pkg/domain/rules", Lines: 1200, Files: 9, Go: true},   // over the default
		{Key: "pkg/domain/small", Lines: 999, Files: 9, Go: true},    // under it
		{Key: "web/src/lib", Lines: 5000, Files: 30},                 // a directory, not a Go package
		{Key: "pkg/interfaces/web", Lines: 301, Files: 4, Go: false}, // a layer limit applies to every language
	}, c)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, f := range got {
		ids = append(ids, f.ID)
	}
	if strings.Join(ids, ",") != "pkg/domain/rules,pkg/interfaces/cmd,pkg/interfaces/web" {
		t.Fatalf("large packages = %v", ids)
	}
	if !strings.Contains(got[1].Message, "pkg/interfaces/cmd has 400 lines in 3 files (limit 300 for layer interfaces)") {
		t.Errorf("message = %q", got[1].Message)
	}
	if !strings.Contains(got[0].Message, "(limit 1000)") {
		t.Errorf("message = %q", got[0].Message)
	}
}

func TestLargePackageValidate(t *testing.T) {
	for _, tc := range []struct {
		edit func(*Config)
		want string
	}{
		{func(c *Config) { c.LargePackage.MaxLines = -1 }, "maxLines"},
		{func(c *Config) { c.LargePackage.Layers = map[string]int{"nope": 10} }, `unknown layer "nope"`},
		{func(c *Config) { c.LargePackage.Layers = map[string]int{"domain": 0} }, "want > 0"},
	} {
		c := DefaultConfig()
		tc.edit(&c)
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("err = %v, want %q", err, tc.want)
		}
	}
}
