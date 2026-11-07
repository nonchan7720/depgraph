package rules

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// The members of the stored files are joined with the types they name:
// a use case holding and building an implementation of the infrastructure
// that satisfies a port is a concrete-dependency (the methods of a Go type
// may be in another file of its package, not in its tests), and its API
// exposing the infrastructure's types is a type-leak.
func TestEvaluateMembers(t *testing.T) {
	ctx, st := connect(t)
	key := fmt.Sprintf("zz_test_%d_members", time.Now().UnixNano())
	tu := func(owner, member, role string, line int, exported bool, target, typ string) graph.TypeUse {
		return graph.TypeUse{Owner: owner, Member: member, Role: role, Line: line, Exported: exported, Target: target, Type: typ}
	}
	upsertAll(ctx, t, st, key, []*graph.FileGraph{
		{Path: "app/usecase/user.go", Package: "app/usecase", Language: graph.Go,
			Imports: []graph.Import{{Spec: "m/app/infra", Target: "app/infra"}},
			Symbols: []graph.Symbol{{Name: "UserRepository", Kind: "interface", Exported: true, Methods: []string{"Find", "Save"}}},
			TypeUses: []graph.TypeUse{
				tu("UserUsecase", "repo", graph.RoleField, 8, false, "app/infra", "MySQLRepo"),
				tu("UserUsecase.Get", "0", graph.RoleResult, 20, true, "app/infra", "Row"),
				tu("UserUsecase.Opts", "o", graph.RoleParam, 21, true, "app/infra", "Options"),
			},
			Constructs: []graph.Construct{{Owner: "NewUserUsecase", Line: 12, Target: "app/infra", Type: "MySQLRepo"}}},
		{Path: "app/infra/mysql.go", Package: "app/infra", Language: graph.Go, Symbols: []graph.Symbol{
			{Name: "MySQLRepo", Kind: "struct", Exported: true},
			{Name: "Row", Kind: "struct", Exported: true},
			{Name: "Options", Kind: "struct", Exported: true},
		}},
		{Path: "app/infra/mysql_find.go", Package: "app/infra", Language: graph.Go, Symbols: []graph.Symbol{
			{Name: "Find", Kind: "method", Exported: true, Receiver: "MySQLRepo"},
			{Name: "Save", Kind: "method", Exported: true, Receiver: "MySQLRepo"},
			{Name: "String", Kind: "method", Exported: true, Receiver: "Row"},
		}},
		// a test's method does not make a type satisfy a port
		{Path: "app/infra/opts_test.go", Package: "app/infra", Language: graph.Go, Symbols: []graph.Symbol{
			{Name: "Find", Kind: "method", Receiver: "Options"}, {Name: "Save", Kind: "method", Receiver: "Options"},
		}},
		// TypeScript: a default-exported class that says it implements the port
		{Path: "web/usecase/order.ts", Package: "web/usecase", Language: graph.TypeScript,
			Symbols:  []graph.Symbol{{Name: "OrderRepo", Kind: "interface", Exported: true, Methods: []string{"find"}}},
			TypeUses: []graph.TypeUse{tu("OrderService", "repo", graph.RoleCtor, 3, true, "web/infra/sql.ts", "default")}},
		{Path: "web/infra/sql.ts", Package: "web/infra", Language: graph.TypeScript,
			Symbols: []graph.Symbol{{Name: "SqlOrderRepo", Kind: "class", Exported: true, Methods: []string{"find"},
				Implements: []string{"web/usecase/order.ts#OrderRepo"}}},
			Usage: &graph.Usage{Exports: []graph.ExportName{{Name: "default", Local: "SqlOrderRepo"}}}},
	})
	c := rules.DefaultConfig()
	c.DefaultLayers = false
	c.Layers = []rules.Layer{{Name: "usecase", Packages: []string{"**/usecase/**"}}, {Name: "infra", Packages: []string{"**/infra/**"}}}
	c.ForbiddenLayers = nil
	c.AbstractLayers = [][2]string{{"usecase", "infra"}}
	fs, err := New(st, key).Evaluate(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	ids := func(rule string) []string {
		var out []string
		for _, f := range byRule(fs, rule) {
			out = append(out, f.ID)
		}
		return out
	}
	if got, want := ids(rules.RuleConcreteDependency), []string{"app/usecase -> app/infra.MySQLRepo", "web/usecase -> web/infra.SqlOrderRepo"}; !slices.Equal(got, want) {
		t.Errorf("concrete dependencies = %q, want %q", got, want)
	}
	if got, want := ids(rules.RuleTypeLeak), []string{"app/usecase -> app/infra.Options", "app/usecase -> app/infra.Row"}; !slices.Equal(got, want) {
		t.Errorf("type leaks = %q, want %q", got, want)
	}
	for _, f := range byRule(fs, rules.RuleConcreteDependency) {
		if f.ID == "app/usecase -> app/infra.MySQLRepo" && !strings.Contains(f.Message, "app/usecase/user.go:8 UserUsecase.repo, and 1 more") {
			t.Errorf("message = %q", f.Message)
		}
	}
	c.Settings = map[string]rules.RuleSetting{rules.RuleConcreteDependency: {Disabled: true}, rules.RuleTypeLeak: {Disabled: true}}
	if fs, err := New(st, key).Evaluate(ctx, c); err != nil || len(byRule(fs, rules.RuleConcreteDependency))+len(byRule(fs, rules.RuleTypeLeak)) != 0 {
		t.Errorf("disabled: %v", err)
	}
}
