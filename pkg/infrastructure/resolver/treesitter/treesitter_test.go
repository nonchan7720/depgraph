package treesitter

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGo(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/app\n")
	writeFile(t, root, "repository/x.go", "package repository\n")
	writeFile(t, root, "handler/user.go", `package handler
import (
	"fmt"
	"example.com/app/repository"
)
type UserHandler struct{}
type Store interface{}
func (h *UserHandler) Get() {}
func helper() {}
`)
	fg, err := New().ResolveFile(context.Background(), root, "handler/user.go")
	if err != nil {
		t.Fatal(err)
	}
	if fg.Package != "handler" {
		t.Errorf("package = %q", fg.Package)
	}
	wantSyms := map[string]string{"UserHandler": "struct", "Store": "interface", "Get": "method", "helper": "function"}
	if len(fg.Symbols) != len(wantSyms) {
		t.Fatalf("symbols = %+v", fg.Symbols)
	}
	for _, s := range fg.Symbols {
		if wantSyms[s.Name] != s.Kind {
			t.Errorf("%s kind = %s", s.Name, s.Kind)
		}
		if s.Exported != (s.Name[0] >= 'A' && s.Name[0] <= 'Z') {
			t.Errorf("%s exported = %v", s.Name, s.Exported)
		}
	}
	if len(fg.Imports) != 2 || fg.Imports[0].Target != "ext:fmt" || fg.Imports[1].Target != "repository" || fg.Imports[1].External {
		t.Errorf("imports = %+v", fg.Imports)
	}
}

func TestTypeScript(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "src/db/index.ts", "export function query() {}\n")
	writeFile(t, root, "src/db/util.ts", "export const format = (x: string) => x;\n")
	writeFile(t, root, "src/api/users.ts", `import { query } from '../db';
import type { Request } from 'express';
import { format } from '@/db/util';
export * from './shared';
export class UserController {}
export const Helper = () => 1;
function internal() {}
interface Row {}
`)
	writeFile(t, root, "src/api/shared.ts", "export const x = 1;\n")
	fg, err := New().ResolveFile(context.Background(), root, "src/api/users.ts")
	if err != nil {
		t.Fatal(err)
	}
	targets := map[string]string{}
	for _, im := range fg.Imports {
		targets[im.Spec] = im.Target
	}
	want := map[string]string{"../db": "src/db", "express": "ext:express", "@/db/util": "src/db", "./shared": "src/api"}
	for spec, tgt := range want {
		if targets[spec] != tgt {
			t.Errorf("import %s -> %q, want %q", spec, targets[spec], tgt)
		}
	}
	exp := map[string]bool{"UserController": true, "Helper": true, "internal": false, "Row": false}
	for _, s := range fg.Symbols {
		if v, ok := exp[s.Name]; !ok || v != s.Exported {
			t.Errorf("symbol %s exported=%v", s.Name, s.Exported)
		}
	}
	if len(fg.Symbols) != len(exp) {
		t.Errorf("symbols = %+v", fg.Symbols)
	}
}

func TestPython(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "app/__init__.py", "")
	writeFile(t, root, "app/models.py", "class User: ...\n")
	writeFile(t, root, "app/services/__init__.py", "")
	writeFile(t, root, "app/services/helper.py", "def h(): pass\n")
	writeFile(t, root, "app/services/user.py", `import os
from ..models import User
from app.services import helper
from . import helper as h
import json as j

class UserService:
    pass

@staticmethod
def Helper():
    pass

def _private():
    pass
`)
	fg, err := New().ResolveFile(context.Background(), root, "app/services/user.py")
	if err != nil {
		t.Fatal(err)
	}
	targets := map[string]string{}
	for _, im := range fg.Imports {
		targets[im.Spec] = im.Target
	}
	want := map[string]string{"os": "ext:os", "..models": "app", "app.services": "app/services", ".": "app/services", "json": "ext:json"}
	for spec, tgt := range want {
		if targets[spec] != tgt {
			t.Errorf("import %s -> %q, want %q", spec, targets[spec], tgt)
		}
	}
	exp := map[string]bool{"UserService": true, "Helper": true, "_private": false}
	for _, s := range fg.Symbols {
		if v, ok := exp[s.Name]; !ok || v != s.Exported {
			t.Errorf("symbol %s exported=%v", s.Name, s.Exported)
		}
	}
}

// 名前識別子の位置(NameLine 1始まり / Col 0始まり UTF-16)が埋まること。
func TestSymbolNamePosition(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/app\n")
	// 2 行目の「日本語」は UTF-8 で 9 バイト・UTF-16 で 3 単位。
	writeFile(t, root, "a/a.go", "package a\n"+
		"func (h *H) Get() {}\n"+
		"type H struct{}\n"+
		"var _ = \"日本語\"; func Late() {}\n")
	fg, err := New().ResolveFile(context.Background(), root, "a/a.go")
	if err != nil {
		t.Fatal(err)
	}
	type pos struct{ line, col int }
	want := map[string]pos{
		"Get": {2, 12},
		"H":   {3, 5},
		// `var _ = "` (9) + 日本語(UTF-16 で 3) + `"; func ` (8) = 20
		"Late": {4, 20},
	}
	for _, s := range fg.Symbols {
		w, ok := want[s.Name]
		if !ok {
			continue
		}
		if s.NameLine != w.line || s.Col != w.col {
			t.Errorf("%s: NameLine=%d Col=%d, want %d/%d", s.Name, s.NameLine, s.Col, w.line, w.col)
		}
	}
}

func TestSymbolNamePositionTSAndPython(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "x.ts", "export const run = () => 1;\nexport function go() {}\n")
	writeFile(t, root, "y.py", "class A:\n    pass\n\ndef main():\n    pass\n")
	ts, err := New().ResolveFile(context.Background(), root, "x.ts")
	if err != nil {
		t.Fatal(err)
	}
	py, err := New().ResolveFile(context.Background(), root, "y.py")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][2]int{}
	for _, s := range append(ts.Symbols, py.Symbols...) {
		got[s.Name] = [2]int{s.NameLine, s.Col}
	}
	want := map[string][2]int{"run": {1, 13}, "go": {2, 16}, "A": {1, 6}, "main": {4, 4}}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %v, want %v", k, got[k], w)
		}
	}
}
