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

// typeDeps renders "Holder.Member@line -> target#Type", sorted.
func typeDeps(fg *graph.FileGraph) []string {
	out := make([]string, 0, len(fg.TypeDeps))
	for _, d := range fg.TypeDeps {
		out = append(out, fmt.Sprintf("%s.%s@%d -> %s#%s", d.Holder, d.Member, d.Line, d.Target, d.Type))
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
	t.Fatalf("%s: no symbol %s in %+v", fg.Path, name, fg.Symbols)
	return graph.Symbol{}
}

func wantDeps(t *testing.T, fg *graph.FileGraph, want ...string) {
	t.Helper()
	if got := typeDeps(fg); !slices.Equal(got, want) {
		t.Errorf("%s: type deps\n  %q\nwant\n  %q", fg.Path, got, want)
	}
}

// Go: the fields of structs (named and embedded) and the parameters of New*
// functions whose type is a type of another package of the repository,
// directly or through a pointer. Collections, external and own-package
// types are not dependencies of this kind.
func TestTypeDepsGo(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/app\n")
	writeFile(t, root, "repository/repo.go", "package repository\n")
	writeFile(t, root, "store/s.go", "package storage\n")
	writeFile(t, root, "usecase/user.go", `package usecase

import (
	"database/sql"

	"example.com/app/repository"
	st "example.com/app/store"
	"example.com/app/store"
)

type UserUsecase struct {
	repo  *repository.MySQLRepo
	users repository.UserRepository
	*repository.Base
	a, b  st.Cache
	all   []*repository.MySQLRepo
	byID  map[string]repository.Row
	db    *sql.DB
	own   *Local
	gen   repository.Generic[int]
	s     storage.Store
}

type Local struct{}

func NewUserUsecase(r *repository.MySQLRepo, n int) *UserUsecase { return nil }

func (u *UserUsecase) Get(x repository.Row) {}

func helper(r *repository.MySQLRepo) {}
`)
	fg := resolve(t, root, "usecase/user.go")
	wantDeps(t, fg,
		"NewUserUsecase.r@26 -> repository#MySQLRepo",
		"UserUsecase.Base@14 -> repository#Base",
		"UserUsecase.a@15 -> store#Cache",
		"UserUsecase.b@15 -> store#Cache",
		"UserUsecase.gen@20 -> repository#Generic",
		"UserUsecase.repo@12 -> repository#MySQLRepo",
		"UserUsecase.s@21 -> store#Store",
		"UserUsecase.users@13 -> repository#UserRepository",
	)
	if s := symbol(t, fg, "Get"); s.Receiver != "UserUsecase" {
		t.Errorf("receiver of Get = %q", s.Receiver)
	}
	for _, im := range fg.Imports {
		if want := im.Spec == "database/sql"; im.Std != want {
			t.Errorf("import %s: std = %v", im.Spec, im.Std)
		}
	}
}

// Go receivers: value, pointer and generic types.
func TestGoMethodReceiver(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/app\n")
	writeFile(t, root, "a/a.go", `package a
type T struct{}
type G[K comparable] struct{}
func (T) Value() {}
func (t *T) Pointer() {}
func (g *G[K]) Generic() {}
`)
	fg := resolve(t, root, "a/a.go")
	for name, want := range map[string]string{"Value": "T", "Pointer": "T", "Generic": "G"} {
		if got := symbol(t, fg, name).Receiver; got != want {
			t.Errorf("receiver of %s = %q, want %q", name, got, want)
		}
	}
}

// TypeScript: class properties and constructor parameters (parameter
// properties included) typed with an imported class; abstract classes are
// marked, methods counted.
func TestTypeDepsTypeScript(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"name": "app"}`)
	writeFile(t, root, "src/repository/user.ts", `export class HttpUserRepo { find() {} save() {} }
export abstract class BaseRepo { abstract find(): void; }
export interface UserRepository { find(): void }
export class Row { constructor(public id: string) {} get name() { return ""; } }
`)
	writeFile(t, root, "src/usecase/user.ts", `import { HttpUserRepo, UserRepository as Users } from "../repository/user";
import * as repo from "../repository/user";
import type { BaseRepo } from "../repository/user";
import { Injectable } from "@nestjs/common";
import { readFile } from "node:fs";

@Injectable()
export class UserService {
  private cache: HttpUserRepo;
  users!: Users;
  base?: BaseRepo;
  list: HttpUserRepo[] = [];
  injected: Injectable;
  constructor(private readonly repo: HttpUserRepo, other: repo.HttpUserRepo, n: number, opt?: HttpUserRepo) {}
  run(x: HttpUserRepo) {}
}
`)
	fg := resolve(t, root, "src/usecase/user.ts")
	wantDeps(t, fg,
		"UserService.base@11 -> src/repository/user.ts#BaseRepo",
		"UserService.cache@9 -> src/repository/user.ts#HttpUserRepo",
		"UserService.opt@14 -> src/repository/user.ts#HttpUserRepo",
		"UserService.other@14 -> src/repository/user.ts#HttpUserRepo",
		"UserService.repo@14 -> src/repository/user.ts#HttpUserRepo",
		"UserService.users@10 -> src/repository/user.ts#UserRepository",
	)
	for _, im := range fg.Imports {
		if want := im.Spec == "node:fs"; im.Std != want {
			t.Errorf("import %s: std = %v", im.Spec, im.Std)
		}
	}
	def := resolve(t, root, "src/repository/user.ts")
	for name, want := range map[string]graph.Symbol{
		"HttpUserRepo": {Methods: 2},
		"BaseRepo":     {Methods: 1, Abstract: true},
		"Row":          {Methods: 0}, // a constructor and an accessor are not behaviour
	} {
		s := symbol(t, def, name)
		if s.Methods != want.Methods || s.Abstract != want.Abstract {
			t.Errorf("%s: methods %d abstract %v, want %d %v", name, s.Methods, s.Abstract, want.Methods, want.Abstract)
		}
	}
}

// Python: class-level annotations and __init__ parameters typed with an
// imported class; Protocol / ABC classes and classes with abstract methods
// are abstract.
func TestTypeDepsPython(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pyproject.toml", "[project]\nname = \"app\"\n")
	writeFile(t, root, "app/__init__.py", "")
	writeFile(t, root, "app/repository.py", `import abc
from abc import ABC, abstractmethod
from typing import Protocol

class SqlUserRepo:
    def __init__(self, url): ...
    def find(self): ...
    def _helper(self): ...

class UserRepository(Protocol):
    def find(self): ...

class Base(ABC):
    pass

class Base2(abc.ABC):
    pass

class WithAbstract:
    @abstractmethod
    def find(self): ...

class Meta(metaclass=abc.ABCMeta):
    pass

class Row:
    id: int
`)
	writeFile(t, root, "app/usecase.py", `import json
from dataclasses import dataclass
from app.repository import SqlUserRepo, UserRepository as Users
from app import repository
import app.repository as r

@dataclass
class UserService:
    repo: SqlUserRepo
    users: Users
    other: "SqlUserRepo"
    many: list[SqlUserRepo]
    count: int = 0

    def __init__(self, repo: SqlUserRepo, mod: repository.SqlUserRepo, alias: r.SqlUserRepo, n: int = 1, *, kw: SqlUserRepo = None):
        self.repo = repo

    def run(self, x: SqlUserRepo): ...
`)
	fg := resolve(t, root, "app/usecase.py")
	wantDeps(t, fg,
		"UserService.alias@15 -> app/repository.py#SqlUserRepo",
		"UserService.kw@15 -> app/repository.py#SqlUserRepo",
		"UserService.mod@15 -> app/repository.py#SqlUserRepo",
		"UserService.repo@15 -> app/repository.py#SqlUserRepo",
		"UserService.repo@9 -> app/repository.py#SqlUserRepo",
		"UserService.users@10 -> app/repository.py#UserRepository",
	)
	for _, im := range fg.Imports {
		if want := im.Spec == "json" || im.Spec == "dataclasses"; im.Std != want {
			t.Errorf("import %s: std = %v", im.Spec, im.Std)
		}
	}
	def := resolve(t, root, "app/repository.py")
	for name, want := range map[string]graph.Symbol{
		"SqlUserRepo":    {Methods: 2},
		"UserRepository": {Methods: 1, Abstract: true},
		"Base":           {Abstract: true},
		"Base2":          {Abstract: true},
		"WithAbstract":   {Methods: 1, Abstract: true},
		"Meta":           {Abstract: true},
		"Row":            {},
	} {
		s := symbol(t, def, name)
		if s.Methods != want.Methods || s.Abstract != want.Abstract {
			t.Errorf("%s: methods %d abstract %v, want %d %v", name, s.Methods, s.Abstract, want.Methods, want.Abstract)
		}
	}
}

// Data classes are not services: properties are not behaviour, and a
// dataclass / attrs / pydantic model / Enum / NamedTuple is data whatever
// helpers it has.
func TestPythonDataClasses(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pyproject.toml", "[project]\nname = \"app\"\n")
	writeFile(t, root, "app/__init__.py", "")
	writeFile(t, root, "app/models.py", `from dataclasses import dataclass
import dataclasses
from enum import Enum
from functools import cached_property
from pydantic import BaseModel

class Props:
    @property
    def dsn(self): ...
    @dsn.setter
    def dsn(self, v): ...
    @cached_property
    def size(self): ...

@dataclass(frozen=True)
class Config:
    url: str
    def dsn(self): ...

@dataclasses.dataclass
class Row:
    def key(self): ...

class Color(Enum):
    RED = 1
    def label(self): ...

class User(BaseModel):
    def name(self): ...

class Service:
    @property
    def ready(self): ...
    def run(self): ...
`)
	fg := resolve(t, root, "app/models.py")
	for name, want := range map[string]int{"Props": 0, "Config": 0, "Row": 0, "Color": 0, "User": 0, "Service": 1} {
		if got := symbol(t, fg, name).Methods; got != want {
			t.Errorf("%s: methods %d, want %d", name, got, want)
		}
	}
}

// Optional dependencies: T | null (TypeScript), Optional[T] / T | None
// (Python); a default-imported class is recorded as "default" (the rule
// looks up what the file exports as default).
func TestTypeDepsOptionalAndDefault(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "package.json", `{"name": "app"}`)
	writeFile(t, root, "src/repo.ts", "export default class SqlRepo { find() {} }\nexport class Other { run() {} }\n")
	writeFile(t, root, "src/service.ts", `import SqlRepo, { Other } from "./repo";
export class Service {
  repo: SqlRepo | null;
  other?: Other | undefined;
  both: SqlRepo | Other;
  constructor(r: SqlRepo) {}
}
`)
	wantDeps(t, resolve(t, root, "src/service.ts"),
		"Service.other@4 -> src/repo.ts#Other",
		"Service.r@6 -> src/repo.ts#default",
		"Service.repo@3 -> src/repo.ts#default",
	)
	writeFile(t, root, "pyproject.toml", "[project]\nname = \"app\"\n")
	writeFile(t, root, "app/__init__.py", "")
	writeFile(t, root, "app/repo.py", "class SqlRepo:\n    def find(self): ...\n")
	writeFile(t, root, "app/service.py", `from typing import Optional
import typing
from app.repo import SqlRepo

class Service:
    a: Optional[SqlRepo]
    b: SqlRepo | None
    c: typing.Optional[SqlRepo]
    d: SqlRepo | int
`)
	wantDeps(t, resolve(t, root, "app/service.py"),
		"Service.a@6 -> app/repo.py#SqlRepo",
		"Service.b@7 -> app/repo.py#SqlRepo",
		"Service.c@8 -> app/repo.py#SqlRepo",
	)
}
