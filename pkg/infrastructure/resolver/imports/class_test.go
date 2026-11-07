package imports

import (
	"context"
	"testing"
)

type classCase struct {
	name, from, spec string
	class            string // ClassThirdParty, ClassStandard or "" (unknown)
}

func runClassCases(t *testing.T, r Resolver, root string, cases []classCase) {
	t.Helper()
	check := func(t *testing.T, how string, c classCase, got Result) {
		t.Helper()
		if got.Kind != External || got.Class != c.class || got.Basis == "" {
			t.Errorf("%s(%q, %q) = %s class %q basis %q, want external %q", how, c.from, c.spec, got.Kind, got.Class, got.Basis, c.class)
		}
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			check(t, "Resolve", c, r.Resolve(context.Background(), root, c.from, c.spec))
		})
	}
	// the batch path must agree
	if br, ok := r.(BatchResolver); ok {
		reqs := make([]Request, len(cases))
		for i, c := range cases {
			reqs[i] = Request{FromFile: c.from, Spec: c.spec}
		}
		for i, got := range br.ResolveBatch(context.Background(), root, reqs) {
			check(t, "ResolveBatch", cases[i], got)
		}
	}
}

// Go: a package is third-party when a module the importing module requires
// (or replaces with a module or a directory outside the repository, or a
// go.work of it does) provides it; a path whose first element has no dot is
// the standard library (the go command's own rule) unless a module
// provides it; anything else is not known. Nothing is executed.
func TestGoClass(t *testing.T) {
	root := tree(t, map[string]string{
		"go.mod": `module example.com/app

go 1.22

require (
	github.com/spf13/cobra v1.8.0
	golang.org/x/mod v0.20.0 // indirect
)

replace example.com/sibling => ../sibling

replace myorg/legacy => myorg/legacy v1.0.0
`,
		"main.go":        "package main\n",
		"tools/go.mod":   "module example.com/tools\n",
		"tools/m.go":     "package main\n",
		"work/go.work":   "go 1.22\n\nuse ./w\n\nreplace dotless/lib => ../../outside\n",
		"work/w/go.mod":  "module example.com/w\n",
		"work/w/main.go": "package main\n",
	})
	runClassCases(t, NewGo(), root, []classCase{
		{"required", "main.go", "github.com/spf13/cobra", ClassThirdParty},
		{"required subpackage", "main.go", "github.com/spf13/cobra/doc", ClassThirdParty},
		{"indirect requirement", "main.go", "golang.org/x/mod/modfile", ClassThirdParty},
		{"replaced by a directory outside the repository", "main.go", "example.com/sibling/x", ClassThirdParty},
		// a dotless module path is not the standard library when a module provides it
		{"dotless replaced module", "main.go", "myorg/legacy/y", ClassThirdParty},
		{"standard library", "main.go", "net/http", ClassStandard},
		{"not required", "main.go", "github.com/other/pkg", ""},
		// requirements are per module
		{"required by another module", "tools/m.go", "github.com/spf13/cobra", ""},
		{"go.work replace", "work/w/main.go", "dotless/lib/z", ClassThirdParty},
	})
}

// TypeScript: what esbuild resolves into node_modules is third-party, a Node
// built-in is the standard library ("punycode/" is the installed package,
// "punycode" the built-in); a package that is not installed is not known
// here (the resolver of the file's manifests decides from package.json).
func TestTypeScriptClass(t *testing.T) {
	root := tree(t, map[string]string{
		"src/main.ts":                        "",
		"node_modules/react/package.json":    `{"name": "react", "main": "index.js"}`,
		"node_modules/react/index.js":        "",
		"node_modules/punycode/package.json": `{"name": "punycode", "main": "punycode.js"}`,
		"node_modules/punycode/punycode.js":  "",
	})
	runClassCases(t, newTS(t), root, []classCase{
		{"installed", "src/main.ts", "react", ClassThirdParty},
		{"installed package named like a built-in", "src/main.ts", "punycode/", ClassThirdParty},
		{"built-in over an installed package", "src/main.ts", "punycode", ClassStandard},
		{"prefixed built-in", "src/main.ts", "node:fs", ClassStandard},
		{"bare built-in", "src/main.ts", "path", ClassStandard},
		{"not installed", "src/main.ts", "left-pad", ""},
	})
}
