package rules

import (
	"strings"
	"testing"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
)

func archConfig() Config {
	c := DefaultConfig()
	c.DefaultLayers = false
	c.Layers = []Layer{
		{Name: "usecase", Packages: []string{"app/usecase/**"}},
		{Name: "infra", Packages: []string{"app/infra/**"}},
		{Name: "domain", Packages: []string{"app/domain/**"}},
	}
	c.AbstractLayers = nil
	c.ForbiddenLayers = nil
	c.AbstractLayers = [][2]string{{"usecase", "infra"}}
	return c
}

type types map[string]TypeInfo // TypeRef.String -> info

func (t types) Type(r graph.TypeRef) (TypeInfo, bool) { v, ok := t[r.String()]; return v, ok }
func (t types) Interfaces(pkg string) []TypeInfo {
	var out []TypeInfo
	for _, v := range t {
		if v.Package == pkg && (v.Kind == "interface" || v.Abstract) {
			out = append(out, v)
		}
	}
	return out
}

func ref(target, typ string) graph.TypeRef { return graph.TypeRef{Target: target, Type: typ} }

func use(owner, member, role string, line int, r graph.TypeRef) MemberUse {
	return MemberUse{File: "app/usecase/user.go", Package: "app/usecase", Owner: owner, Member: member, Role: role, Line: line, Target: r.Target, Type: r.Type}
}

func goTypes() types {
	return types{
		"app/infra#MySQLRepo": {Ref: ref("app/infra", "MySQLRepo"), Package: "app/infra", Kind: "struct", Methods: []string{"Find", "Save", "String"}},
		"app/infra#Lonely":    {Ref: ref("app/infra", "Lonely"), Package: "app/infra", Kind: "struct", Methods: []string{"Run"}},
		"app/infra#Row":       {Ref: ref("app/infra", "Row"), Package: "app/infra", Kind: "struct", Methods: []string{"String", "MarshalJSON"}},
		"app/infra#Closer":    {Ref: ref("app/infra", "Closer"), Package: "app/infra", Kind: "interface", Methods: []string{"Close"}},
		// the port, declared by the use case
		"app/usecase#UserRepository": {Ref: ref("app/usecase", "UserRepository"), Package: "app/usecase", Kind: "interface", Methods: []string{"Find", "Save"}},
		// an interface of another layer the type also satisfies: not where a port is
		"app/domain#Finder": {Ref: ref("app/domain", "Finder"), Package: "app/domain", Kind: "interface", Methods: []string{"Find"}},
	}
}

// A use case holding (or building) an implementation of the infrastructure
// although an interface it implements is at hand is reported; without
// such an interface, or for data, a collection or the interface itself,
// nothing is.
func TestConcreteDependencies(t *testing.T) {
	c := archConfig()
	pairs, err := c.AbstractPairs()
	if err != nil {
		t.Fatal(err)
	}
	ty := goTypes()
	uses := []MemberUse{
		use("UserUsecase", "repo", graph.RoleField, 8, ref("app/infra", "MySQLRepo")),
		use("NewUserUsecase", "r", graph.RoleCtor, 12, ref("app/infra", "MySQLRepo")),
		{File: "app/usecase/user.go", Package: "app/usecase", Construct: true, Owner: "NewUserUsecase", Line: 13, Target: "app/infra", Type: "MySQLRepo"},
		use("UserUsecase", "l", graph.RoleField, 9, ref("app/infra", "Lonely")),                // no interface for it
		use("UserUsecase", "row", graph.RoleField, 10, ref("app/infra", "Row")),                // data
		use("UserUsecase", "users", graph.RoleField, 11, ref("app/usecase", "UserRepository")), // the port
		use("Get", "0", graph.RoleResult, 20, ref("app/infra", "MySQLRepo")),                   // not held
	}
	all := uses
	coll := use("UserUsecase", "all", graph.RoleField, 14, ref("app/infra", "MySQLRepo"))
	coll.Collection = true
	all = append(all, coll)
	got := ConcreteDependencies(all, ty, pairs)
	if len(got) != 1 {
		t.Fatalf("findings = %+v", got)
	}
	f := got[0]
	for _, want := range []string{
		"app/usecase depends on the concrete type app/infra.MySQLRepo",
		"app/usecase/user.go:8 UserUsecase.repo, and 2 more",
		"although it implements app/usecase.UserRepository",
		"usecase must depend on infra through an interface",
	} {
		if !strings.Contains(f.Message, want) {
			t.Errorf("message %q lacks %q", f.Message, want)
		}
	}
	if f.ID != "app/usecase -> app/infra.MySQLRepo" || f.Rule != RuleConcreteDependency {
		t.Errorf("finding = %+v", f)
	}
}

// TypeScript and Python classes say what they implement.
func TestConcreteDependenciesExplicitImplements(t *testing.T) {
	c := archConfig()
	pairs, _ := c.AbstractPairs()
	ty := types{
		"app/infra/http.ts#HttpRepo": {Ref: ref("app/infra/http.ts", "HttpRepo"), Package: "app/infra", Kind: "class",
			Methods: []string{"find"}, Implements: []graph.TypeRef{ref("app/usecase/port.ts", "Repo")}},
		"app/usecase/port.ts#Repo": {Ref: ref("app/usecase/port.ts", "Repo"), Package: "app/usecase", Kind: "interface", Methods: []string{"find", "save"}},
		"app/infra/other.ts#Other": {Ref: ref("app/infra/other.ts", "Other"), Package: "app/infra", Kind: "class", Methods: []string{"find", "save"}},
	}
	got := ConcreteDependencies([]MemberUse{
		use("S", "a", graph.RoleCtor, 1, ref("app/infra/http.ts", "HttpRepo")),
		// a class satisfies an interface only by saying so (no structural match)
		use("S", "b", graph.RoleCtor, 2, ref("app/infra/other.ts", "Other")),
	}, ty, pairs)
	if len(got) != 1 || !strings.Contains(got[0].Message, "HttpRepo") {
		t.Errorf("findings = %+v", got)
	}
}

// An inner layer exposing an outer layer's concrete type (or data) in its
// API leaks it; interfaces, unexported members and other layers do not.
func TestTypeLeaks(t *testing.T) {
	c := archConfig()
	pairs, _ := c.AbstractPairs()
	ty := goTypes()
	api := func(owner, member, role string, line int, r graph.TypeRef) MemberUse {
		u := use(owner, member, role, line, r)
		u.Exported = true
		return u
	}
	got := TypeLeaks([]MemberUse{
		api("UserUsecase.Get", "0", graph.RoleResult, 20, ref("app/infra", "Row")),
		api("UserUsecase.List", "0", graph.RoleResult, 21, ref("app/infra", "Row")),
		api("UserUsecase.Save", "r", graph.RoleParam, 22, ref("app/infra", "MySQLRepo")),
		api("UserUsecase.Close", "c", graph.RoleParam, 23, ref("app/infra", "Closer")), // an interface
		use("UserUsecase.load", "0", graph.RoleResult, 24, ref("app/infra", "Row")),    // not the API
		api("UserUsecase.Find", "0", graph.RoleResult, 25, ref("app/domain", "Finder")),
	}, ty, pairs)
	var ids []string
	for _, f := range got {
		ids = append(ids, f.ID)
	}
	if strings.Join(ids, ",") != "app/usecase -> app/infra.MySQLRepo,app/usecase -> app/infra.Row" {
		t.Fatalf("leaks = %v", ids)
	}
	if !strings.Contains(got[1].Message, "app/usecase exposes app/infra.Row in its API (app/usecase/user.go:20 UserUsecase.Get result 0, and 1 more)") {
		t.Errorf("message = %q", got[1].Message)
	}
}

func TestAbstractPairsDefaultsAndValidate(t *testing.T) {
	c := DefaultConfig()
	pairs, err := c.AbstractPairs()
	if err != nil || len(pairs) != 2 || pairs[0].From != "usecase" {
		t.Errorf("default abstract pairs = %+v, %v", pairs, err)
	}
	c.AbstractLayers = [][2]string{{"nope", "db"}}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), `abstract nope -> db: unknown layer "nope"`) {
		t.Errorf("validate: %v", err)
	}
}

// The interface to hold must cover what the holder calls on the member:
// an unrelated interface the implementation happens to satisfy (a Logger
// of the infrastructure) is not advice, and without calls only a port of
// the holder's own package is.
func TestConcreteDependenciesPortCoversTheCalls(t *testing.T) {
	c := archConfig()
	pairs, _ := c.AbstractPairs()
	ty := types{
		"app/infra#Orders":   {Ref: ref("app/infra", "Orders"), Package: "app/infra", Kind: "struct", Methods: []string{"Find", "Save", "Printf"}},
		"app/infra#Logger":   {Ref: ref("app/infra", "Logger"), Package: "app/infra", Kind: "interface", Methods: []string{"Printf"}},
		"app/infra#Embeds":   {Ref: ref("app/infra", "Embeds"), Package: "app/infra", Kind: "interface", Methods: []string{"Find"}, Embeds: true},
		"app/usecase#Finder": {Ref: ref("app/usecase", "Finder"), Package: "app/usecase", Kind: "interface", Methods: []string{"Find"}},
		"app/usecase#Repo":   {Ref: ref("app/usecase", "Repo"), Package: "app/usecase", Kind: "interface", Methods: []string{"Find", "Save"}},
	}
	held := func(calls ...string) MemberUse {
		u := use("UC", "orders", graph.RoleField, 3, ref("app/infra", "Orders"))
		u.Calls = calls
		return u
	}
	for _, tc := range []struct {
		name  string
		calls []string
		want  string // the interface named, "" for no finding
	}{
		{"the smallest port covering the calls", []string{"Find"}, "app/usecase.Finder"},
		{"both calls", []string{"Find", "Save"}, "app/usecase.Repo"},
		{"a call no port has", []string{"Find", "Close"}, ""},
		{"no calls: a port of the holder's package", nil, "app/usecase.Repo"},
	} {
		got := ConcreteDependencies([]MemberUse{held(tc.calls...)}, ty, pairs)
		switch {
		case tc.want == "" && len(got) != 0:
			t.Errorf("%s: %+v", tc.name, got)
		case tc.want != "" && (len(got) != 1 || !strings.Contains(got[0].Message, "although it implements "+tc.want+":")):
			t.Errorf("%s: %+v, want %s", tc.name, got, tc.want)
		}
	}
	// only the infrastructure's Logger: no advice, no finding
	only := types{"app/infra#Orders": ty["app/infra#Orders"], "app/infra#Logger": ty["app/infra#Logger"]}
	if got := ConcreteDependencies([]MemberUse{held("Find")}, only, pairs); len(got) != 0 {
		t.Errorf("an unrelated interface was named: %+v", got)
	}
	if got := ConcreteDependencies([]MemberUse{held()}, only, pairs); len(got) != 0 {
		t.Errorf("no calls and no port of the holder's package: %+v", got)
	}
}
