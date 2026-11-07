package treesitter

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"testing"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
)

func resolve(t *testing.T, root, rel string) *graph.FileGraph {
	t.Helper()
	r := New()
	t.Cleanup(r.Close)
	fg, err := r.ResolveFile(context.Background(), root, rel)
	if err != nil {
		t.Fatal(err)
	}
	return fg
}

// typeUses renders "Owner.Member role[ api][ coll]@line -> target#Type", sorted.
func typeUses(fg *graph.FileGraph) []string {
	out := make([]string, 0, len(fg.TypeUses))
	for _, u := range fg.TypeUses {
		s := fmt.Sprintf("%s.%s %s", u.Owner, u.Member, u.Role)
		if u.Exported {
			s += " api"
		}
		if u.Collection {
			s += " coll"
		}
		out = append(out, fmt.Sprintf("%s@%d -> %s", s, u.Line, u.String()))
	}
	sort.Strings(out)
	return out
}

func constructs(fg *graph.FileGraph) []string {
	out := make([]string, 0, len(fg.Constructs))
	for _, c := range fg.Constructs {
		out = append(out, fmt.Sprintf("%s@%d -> %s", c.Owner, c.Line, c.String()))
	}
	sort.Strings(out)
	return out
}

func symbol(t *testing.T, fg *graph.FileGraph, name string) graph.Symbol {
	t.Helper()
	for _, s := range fg.Symbols {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("%s: no symbol %s", fg.Path, name)
	return graph.Symbol{}
}

func wantList(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s:\n  got  %q\n  want %q", what, got, want)
	}
}

func TestMembersGo(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/app\n")
	writeFile(t, root, "repository/repo.go", "package repository\n")
	writeFile(t, root, "store/s.go", "package store\n")
	writeFile(t, root, "usecase/user.go", `package usecase

import (
	"database/sql"

	"example.com/app/repository"
	st "example.com/app/store"
)

type Repo interface {
	Find(id string) (*repository.Row, error)
	Save()
}

type UserUsecase struct {
	repo  *repository.MySQLRepo
	Users repository.UserRepository
	*repository.Base
	all  []*repository.Row
	byID map[string]repository.Row
	db   *sql.DB
	own  *Local
}

type Local struct{}

func NewUserUsecase(r *repository.MySQLRepo) *UserUsecase {
	return &UserUsecase{repo: &repository.MySQLRepo{}}
}

func (u *UserUsecase) Get(id string) (*repository.Row, error) { return nil, nil }

func (u *UserUsecase) list(x repository.Row) {}

func helper() {
	_ = st.Cache{}
	_ = repository.NewMySQL()
}
`)
	fg := resolve(t, root, "usecase/user.go")
	wantList(t, "type uses", typeUses(fg), []string{
		"NewUserUsecase.r ctor api@27 -> repository#MySQLRepo",
		"Repo.Find.0 result api@11 -> repository#Row",
		"UserUsecase.Base field api@18 -> repository#Base",
		"UserUsecase.Get.0 result api@31 -> repository#Row",
		"UserUsecase.Users field api@17 -> repository#UserRepository",
		"UserUsecase.all field coll@19 -> repository#Row",
		"UserUsecase.byID field coll@20 -> repository#Row",
		"UserUsecase.list.x param@33 -> repository#Row",
		"UserUsecase.repo field@16 -> repository#MySQLRepo",
	})
	wantList(t, "constructs", constructs(fg), []string{
		"NewUserUsecase@28 -> repository#MySQLRepo",
		"helper@36 -> store#Cache",
	})
	if s := symbol(t, fg, "Repo"); !slices.Equal(s.Methods, []string{"Find", "Save"}) {
		t.Errorf("interface methods = %v", s.Methods)
	}
	if s := symbol(t, fg, "Get"); s.Receiver != "UserUsecase" {
		t.Errorf("receiver = %q", s.Receiver)
	}
}

func TestMembersTypeScript(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"name": "app"}`)
	writeFile(t, root, "src/repository/user.ts", `export class HttpRepo { find() {} save() {} }
export interface UserRepository { find(): void }
export class Row {}
`)
	writeFile(t, root, "src/repository/sql.ts", "export default class SqlRepo { find() {} }\n")
	writeFile(t, root, "src/usecase/user.ts", `import { HttpRepo, UserRepository as Users, Row } from "../repository/user";
import * as repo from "../repository/user";
import Sql from "../repository/sql";

export interface Port { find(id: string): Row; save(r: Row): void }

export class UserService implements Users {
  private cache: HttpRepo;
  users!: Users;
  rows: Row[] = [];
  constructor(private readonly repo: HttpRepo, other: repo.HttpRepo, opt?: Sql | null) {}
  get(id: string): Row { return new Row(); }
  private build(): HttpRepo { return new repo.HttpRepo(); }
  static make(): void {}
  get size(): number { return 0; }
}

export function load(r: HttpRepo): Promise<Row> { return Promise.resolve(new Row()); }
function local(r: Row): void {}
`)
	fg := resolve(t, root, "src/usecase/user.ts")
	const u = "src/repository/user.ts#"
	wantList(t, "type uses", typeUses(fg), []string{
		"Port.find.0 result api@5 -> " + u + "Row",
		"Port.save.r param api@5 -> " + u + "Row",
		"UserService.build.0 result@13 -> " + u + "HttpRepo",
		"UserService.cache field@8 -> " + u + "HttpRepo",
		"UserService.get.0 result api@12 -> " + u + "Row",
		"UserService.opt ctor api@11 -> src/repository/sql.ts#default",
		"UserService.other ctor api@11 -> " + u + "HttpRepo",
		"UserService.repo ctor api@11 -> " + u + "HttpRepo",
		"UserService.rows field api coll@10 -> " + u + "Row",
		"UserService.users field api@9 -> " + u + "UserRepository",
		"load.0 result api coll@18 -> " + u + "Row",
		"load.r param api@18 -> " + u + "HttpRepo",
		"local.r param@19 -> " + u + "Row",
	})
	wantList(t, "constructs", constructs(fg), []string{
		"UserService.build@13 -> " + u + "HttpRepo",
		"UserService.get@12 -> " + u + "Row",
		"load@18 -> " + u + "Row",
	})
	s := symbol(t, fg, "UserService")
	wantList(t, "methods", s.Methods, []string{"get", "build"})
	wantList(t, "implements", s.Implements, []string{u + "UserRepository"})
	wantList(t, "interface methods", symbol(t, fg, "Port").Methods, []string{"find", "save"})
}

func TestMembersPython(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pyproject.toml", "[project]\nname = \"app\"\n")
	writeFile(t, root, "app/__init__.py", "")
	writeFile(t, root, "app/repository.py", `from abc import ABC, abstractmethod
from dataclasses import dataclass
from typing import Protocol

class SqlRepo:
    def find(self): ...

class UserRepository(Protocol):
    def find(self): ...

class Base(ABC):
    @abstractmethod
    def find(self): ...

@dataclass
class Row:
    id: int
    def key(self): ...

class Props:
    @property
    def dsn(self): ...
`)
	writeFile(t, root, "app/usecase.py", `from typing import Optional, Protocol
from app.repository import SqlRepo, UserRepository as Users, Row
from app import repository

class Port(Protocol):
    def find(self, id: str) -> Row: ...

class UserService(Users):
    repo: SqlRepo
    _cache: SqlRepo
    rows: list[Row]

    def __init__(self, repo: SqlRepo, mod: repository.SqlRepo, opt: Optional[SqlRepo] = None):
        self.repo = repo

    def get(self, id: str) -> Row:
        return Row()

    def _build(self) -> SqlRepo:
        return repository.SqlRepo()

def load(r: SqlRepo) -> list[Row]: ...

def _private(x: Row): ...
`)
	fg := resolve(t, root, "app/usecase.py")
	const r = "app/repository.py#"
	wantList(t, "type uses", typeUses(fg), []string{
		"Port.find.0 result api@6 -> " + r + "Row",
		"UserService._build.0 result@19 -> " + r + "SqlRepo",
		"UserService._cache field@10 -> " + r + "SqlRepo",
		"UserService.get.0 result api@16 -> " + r + "Row",
		"UserService.mod ctor api@13 -> " + r + "SqlRepo",
		"UserService.opt ctor api@13 -> " + r + "SqlRepo",
		"UserService.repo ctor api@13 -> " + r + "SqlRepo",
		"UserService.repo field api@9 -> " + r + "SqlRepo",
		"UserService.rows field api coll@11 -> " + r + "Row",
		"_private.x param@24 -> " + r + "Row",
		"load.0 result api coll@22 -> " + r + "Row",
		"load.r param api@22 -> " + r + "SqlRepo",
	})
	wantList(t, "constructs", constructs(fg), []string{
		"UserService._build@20 -> " + r + "SqlRepo",
		"UserService.get@17 -> " + r + "Row",
	})
	us := symbol(t, fg, "UserService")
	wantList(t, "methods", us.Methods, []string{"get", "_build"})
	wantList(t, "implements", us.Implements, []string{r + "UserRepository"})

	def := resolve(t, root, "app/repository.py")
	for name, want := range map[string]struct {
		abstract, data bool
		methods        []string
	}{
		"SqlRepo":        {methods: []string{"find"}},
		"UserRepository": {abstract: true, methods: []string{"find"}},
		"Base":           {abstract: true, methods: []string{"find"}},
		"Row":            {data: true, methods: []string{"key"}},
		"Props":          {}, // a property is not a method
	} {
		s := symbol(t, def, name)
		if s.Abstract != want.abstract || s.Data != want.data || !slices.Equal(s.Methods, want.methods) {
			t.Errorf("%s: abstract %v data %v methods %v, want %+v", name, s.Abstract, s.Data, s.Methods, want)
		}
	}
}

func TestMembersTypeScriptClassShape(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"name": "app"}`)
	writeFile(t, root, "a.ts", "export abstract class Base { abstract find(): void; run() {} }\n")
	s := symbol(t, resolve(t, root, "a.ts"), "Base")
	if !s.Abstract || !slices.Equal(s.Methods, []string{"find", "run"}) {
		t.Errorf("Base = %+v", s)
	}
}
