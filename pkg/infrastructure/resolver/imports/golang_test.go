package imports

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGoRegression(t *testing.T) {
	root := tree(t, map[string]string{
		"go.mod":              "module example.com/app\n\ngo 1.22\n",
		"main.go":             "package main\n",
		"handler/user.go":     "package handler\n",
		"repository/x.go":     "package repository\n",
		"internal/db/conn.go": "package db\n",
	})
	runCases(t, NewGo(), root, []resolveCase{
		{"module subpackage", "handler/user.go", "example.com/app/repository", inRepo("repository")},
		{"nested subpackage", "handler/user.go", "example.com/app/internal/db", inRepo("internal/db")},
		{"module root", "handler/user.go", "example.com/app", inRepo(".")},
		{"stdlib", "handler/user.go", "fmt", ext("fmt")},
		{"stdlib nested", "handler/user.go", "net/http", ext("net/http")},
		{"third party", "handler/user.go", "github.com/spf13/cobra", ext("github.com/spf13/cobra")},
		{"prefix is not a path boundary", "handler/user.go", "example.com/application/x", ext("example.com/application/x")},
	})
}

func TestGoNoModule(t *testing.T) {
	root := tree(t, map[string]string{"main.go": "package main\n"})
	runCases(t, NewGo(), root, []resolveCase{
		{"no go.mod", "main.go", "example.com/app/x", ext("example.com/app/x")},
	})
}

func TestGoNestedModules(t *testing.T) {
	root := tree(t, map[string]string{
		"go.mod":              "module example.com/app\n",
		"handler/h.go":        "package handler\n",
		"tools/go.mod":        "module example.com/tools\n",
		"tools/cmd/gen/m.go":  "package main\n",
		"tools/lib/l.go":      "package lib\n",
		"api/go.mod":          "module github.com/acme/api\n",
		"api/v1/types.go":     "package v1\n",
		"api/internal/x/x.go": "package x\n",
	})
	runCases(t, NewGo(), root, []resolveCase{
		{"inside nested module", "tools/cmd/gen/m.go", "example.com/tools/lib", inRepo("tools/lib")},
		{"root module from nested", "tools/cmd/gen/m.go", "example.com/app/handler", inRepo("handler")},
		{"nested module from root", "handler/h.go", "github.com/acme/api/v1", inRepo("api/v1")},
		{"nested module root package", "handler/h.go", "example.com/tools", inRepo("tools")},
		// tools/ belongs to another module: example.com/app/tools/lib does not exist
		{"dir owned by another module", "handler/h.go", "example.com/app/tools/lib", unresolved()},
		{"missing package in known module", "handler/h.go", "example.com/app/nope", unresolved()},
	})
}

func TestGoWork(t *testing.T) {
	// no root go.mod: a go.work ties the modules together
	root := tree(t, map[string]string{
		"go.work":          "go 1.22\n\nuse (\n\t./svc/a\n\t./svc/b\n)\n",
		"svc/a/go.mod":     "module example.com/a\n",
		"svc/a/main.go":    "package main\n",
		"svc/b/go.mod":     "module example.com/b\n",
		"svc/b/pkg/p.go":   "package pkg\n",
		"_hidden/c/go.mod": "module example.com/c\n", // not used by go.work, ignored dir
		"_hidden/c/c.go":   "package c\n",
		"go.work.sum":      "",
	})
	runCases(t, NewGo(), root, []resolveCase{
		{"workspace module", "svc/a/main.go", "example.com/b/pkg", inRepo("svc/b/pkg")},
		{"own module root", "svc/b/pkg/p.go", "example.com/a", inRepo("svc/a")},
	})
}

func TestGoWorkUseOutsideWalk(t *testing.T) {
	// modules listed in go.work are used even inside directories the walk skips
	root := tree(t, map[string]string{
		"go.work":              "go 1.22\nuse ./app\nuse ./_vendored/lib\n",
		"app/go.mod":           "module example.com/app\n",
		"app/main.go":          "package main\n",
		"_vendored/lib/go.mod": "module example.com/lib\n",
		"_vendored/lib/l.go":   "package lib\n",
	})
	runCases(t, NewGo(), root, []resolveCase{
		{"go.work use", "app/main.go", "example.com/lib", inRepo("_vendored/lib")},
	})
}

func TestGoReplace(t *testing.T) {
	root := tree(t, map[string]string{
		"go.mod": "module example.com/app\n\nrequire (\n\texample.com/shared v0.0.0\n\tgithub.com/up/fork v1.0.0\n)\n\n" +
			"replace example.com/shared => ./_forks/shared\n" +
			"replace github.com/up/fork v1.0.0 => ../outside\n",
		"main.go":                "package main\n",
		"_forks/shared/go.mod":   "module example.com/shared\n",
		"_forks/shared/str/s.go": "package str\n",
		"other/go.mod":           "module example.com/other\n",
		"other/o.go":             "package other\n",
	})
	runCases(t, NewGo(), root, []resolveCase{
		{"replace to local dir", "main.go", "example.com/shared/str", inRepo("_forks/shared/str")},
		{"replace outside the repo stays external", "main.go", "github.com/up/fork/x", ext("github.com/up/fork/x")},
		// the replace belongs to the root module only
		{"replace not applied to other module", "other/o.go", "example.com/shared/str", ext("example.com/shared/str")},
	})
}

func TestGoWorkReplace(t *testing.T) {
	root := tree(t, map[string]string{
		"go.work":              "go 1.22\nuse ./app\nreplace example.com/shared => ./_forks/shared\n",
		"app/go.mod":           "module example.com/app\n",
		"app/main.go":          "package main\n",
		"_forks/shared/go.mod": "module example.com/shared\n",
		"_forks/shared/s.go":   "package shared\n",
	})
	runCases(t, NewGo(), root, []resolveCase{
		{"go.work replace", "app/main.go", "example.com/shared", inRepo("_forks/shared")},
	})
}

func TestGoInvalidate(t *testing.T) {
	root := tree(t, map[string]string{
		"go.mod":   "module example.com/app\n",
		"main.go":  "package main\n",
		"lib/l.go": "package lib\n",
	})
	r := NewGo()
	runCases(t, r, root, []resolveCase{{"before", "main.go", "example.com/lib", ext("example.com/lib")}})
	writeFiles(t, root, map[string]string{"lib/go.mod": "module example.com/lib\n"})
	r.Invalidate(root)
	runCases(t, r, root, []resolveCase{{"after", "main.go", "example.com/lib", inRepo("lib")}})
}

// A package directory reached through a symlink inside the repository is
// the package of its real directory: the scan reads its files there, so
// mapping the import to the link left the real package unreached.
func TestGoSymlinkedPackageDir(t *testing.T) {
	root := tree(t, map[string]string{
		"go.mod":             "module example.com/app\n",
		"main.go":            "package main\n",
		"internal/real/b.go": "package b\n",
	})
	if err := os.Symlink("real", filepath.Join(root, "internal", "b")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("internal/real", filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	runCases(t, NewGo(), root, []resolveCase{
		{"symlinked package", "main.go", "example.com/app/internal/b", inRepo("internal/real")},
		{"under a symlinked dir", "main.go", "example.com/app/linked", inRepo("internal/real")},
		{"real dir", "main.go", "example.com/app/internal/real", inRepo("internal/real")},
	})
}
