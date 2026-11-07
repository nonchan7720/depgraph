package treesitter

import (
	"context"
	"slices"
	"strings"
	"testing"

	gts "github.com/odvcencio/gotreesitter"
	"github.com/odvcencio/gotreesitter/grammars/kotlin"
	"github.com/odvcencio/gotreesitter/grammars/rust"
)

// 将来対応予定の言語(Rust / Kotlin)の文法が gotreesitter で実用になるかの
// 確認。Resolver にはまだ組み込まない(Ruby は組み込み済みで usage_rb_test.go が見る)。エラーの無い木が得られ、トップレベルの宣言と
// import 相当のノードが期待どおりの型・名前で取れることだけを見る。
func TestGrammarSmoke(t *testing.T) {
	cases := []struct {
		name string
		lang *gts.Language
		src  string
		// トップレベルの宣言ノードの型 -> 名前（出現順）
		decls map[string][]string
		// import 相当のノードの型と、そこから取り出したい参照先
		importType string
		imports    []string
	}{
		{
			name: "rust",
			lang: rust.Language(),
			src: `use std::collections::HashMap;
use crate::models::{User, Role};

mod models;

pub struct UserService {
    users: HashMap<u32, User>,
}

impl UserService {
    pub fn find(&self, id: u32) -> Option<&User> {
        self.users.get(&id)
    }
}

pub trait Repo {
    fn get(&self) -> u32;
}

enum Kind { A, B }

pub fn helper() -> u32 {
    1
}
`,
			decls: map[string][]string{
				"mod_item":      {"models"},
				"struct_item":   {"UserService"},
				"trait_item":    {"Repo"},
				"enum_item":     {"Kind"},
				"function_item": {"helper"},
			},
			importType: "use_declaration",
			imports:    []string{"std::collections::HashMap", "crate::models::{User, Role}"},
		},
		{
			name: "kotlin",
			lang: kotlin.Language(),
			src: `package com.example.app

import com.example.models.User
import kotlinx.coroutines.*

class UserService(private val repo: Repo) {
    fun find(id: Int): User? = repo.get(id)
}

interface Repo {
    fun get(id: Int): User?
}

object Registry

fun helper(): Int = 1
`,
			decls: map[string][]string{
				"class_declaration":    {"UserService", "Repo"},
				"object_declaration":   {"Registry"},
				"function_declaration": {"helper"},
			},
			importType: "import_header",
			imports:    []string{"com.example.models.User", "kotlinx.coroutines.*"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := []byte(tc.src)
			tree, err := parse(context.Background(), tc.lang, src)
			if err != nil {
				t.Fatal(err)
			}
			defer tree.Release()
			root := tree.RootNode()
			if root.HasError() {
				t.Fatalf("parse error:\n%s", root.SExpr(tc.lang))
			}

			gotDecls := map[string][]string{}
			var gotImports []string
			var walkImports func(n *gts.Node)
			walkImports = func(n *gts.Node) {
				if n.Type(tc.lang) == tc.importType {
					if spec := smokeImportSpec(n, tc.lang, src); spec != "" {
						gotImports = append(gotImports, spec)
					}
					return
				}
				for i := 0; i < n.NamedChildCount(); i++ {
					walkImports(n.NamedChild(i))
				}
			}
			for i := 0; i < root.NamedChildCount(); i++ {
				n := root.NamedChild(i)
				typ := n.Type(tc.lang)
				if _, ok := tc.decls[typ]; ok {
					gotDecls[typ] = append(gotDecls[typ], smokeDeclName(n, tc.lang, src))
				}
				walkImports(n)
			}
			for typ, want := range tc.decls {
				if !slices.Equal(gotDecls[typ], want) {
					t.Errorf("%s: got %q, want %q", typ, gotDecls[typ], want)
				}
			}
			if !slices.Equal(gotImports, tc.imports) {
				t.Errorf("imports: got %q, want %q", gotImports, tc.imports)
			}
		})
	}
}

// 上の「エラーの無い木」判定が空振りでないこと: 壊れた入力では HasError が立つ。
func TestGrammarSmokeDetectsErrors(t *testing.T) {
	cases := map[string]struct {
		lang *gts.Language
		src  string
	}{
		"rust":   {rust.Language(), "fn main( {\n    let x = ;\n}\n"},
		"kotlin": {kotlin.Language(), "class Foo {\n    fun bar( {\n}\n"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			tree, err := parse(context.Background(), tc.lang, []byte(tc.src))
			if err != nil {
				t.Fatal(err)
			}
			defer tree.Release()
			if !tree.RootNode().HasError() {
				t.Errorf("HasError = false for broken input:\n%s", tree.RootNode().SExpr(tc.lang))
			}
		})
	}
}

// smokeDeclName は宣言ノードの名前。name フィールドが無い文法(Kotlin)では
// 最初の型名/識別子の子を使う。
func smokeDeclName(n *gts.Node, lang *gts.Language, src []byte) string {
	if id := n.ChildByFieldName("name", lang); id != nil {
		return id.Text(src)
	}
	for i := 0; i < n.NamedChildCount(); i++ {
		c := n.NamedChild(i)
		switch c.Type(lang) {
		case "type_identifier", "simple_identifier", "identifier":
			return c.Text(src)
		}
	}
	return ""
}

// smokeImportSpec は import 相当のノードから参照先を取り出す。
func smokeImportSpec(n *gts.Node, lang *gts.Language, src []byte) string {
	switch n.Type(lang) {
	case "use_declaration": // Rust: use a::b;
		return n.ChildByFieldName("argument", lang).Text(src)
	case "import_header": // Kotlin: import a.b[.*]
		return strings.TrimSpace(strings.TrimPrefix(n.Text(src), "import"))
	}
	return ""
}
