package deps

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// only is the one manifest of root and dirs that declares dependencies.
func only(t *testing.T, root string, dirs ...string) ([]Manifest, Manifest) {
	t.Helper()
	ms := Declared(root, dirs)
	for _, m := range ms {
		if len(m.Deps) > 0 {
			return ms, m
		}
	}
	t.Fatal("no manifest with dependencies")
	return nil, Manifest{}
}

// factsOf are the facts of the dependencies names of m.
func factsOf(root string, ms []Manifest, m Manifest, names []string, l Lookup) map[string]Fact {
	facts, _ := Facts(context.Background(), root, ms, map[string][]string{m.Path: names}, l)
	return facts[m.Path]
}

// A required module no scanned file imports is still used when go.mod
// names a tool of it, or when a Go file of the module that is not in the
// graph (an excluded directory) imports it; a nested module, testdata and
// vendor are not the module's code.
func TestGoFacts(t *testing.T) {
	root := tree(t, map[string]string{
		"go.mod": `module example.com/app

require (
	github.com/excluded/lib v1.0.0
	github.com/nested/lib v1.0.0
	github.com/tool/gen v0.1.0
	github.com/unused/lib v1.2.0
	github.com/unused/library v1.2.0
)

tool github.com/tool/gen/cmd/gen
`,
		"main.go":              "package main\n",
		"gen/zz_generated.go":  "package gen\n\nimport _ \"github.com/excluded/lib/sub\"\n",
		"tools/go.mod":         "module example.com/app/tools\n",
		"tools/tools.go":       "package tools\n\nimport _ \"github.com/nested/lib\"\n",
		"testdata/x.go":        "package x\n\nimport _ \"github.com/unused/lib\"\n",
		"vendor/v/v.go":        "package v\n\nimport _ \"github.com/unused/lib\"\n",
		"docs/notes.go.txt":    "github.com/unused/lib",
		"pkg/uses_library.go":  "package pkg\n\nimport _ \"github.com/unused/library\"\n",
		"pkg/scanned_names.go": "package pkg\n\n// \"github.com/unused/lib\" is gone\n",
	})
	ms, m := only(t, root, "tools")
	scanned := map[string]bool{"main.go": true, "pkg/scanned_names.go": true}
	got := factsOf(root, ms, m, names(m), Lookup{Scanned: func(rel string) bool { return scanned[rel] }})
	want := map[string]Fact{
		"github.com/excluded/lib":   {Use: "imported by gen/zz_generated.go, which is not scanned"},
		"github.com/nested/lib":     {},
		"github.com/tool/gen":       {Use: "a tool of go.mod"},
		"github.com/unused/lib":     {},
		"github.com/unused/library": {Use: "imported by pkg/uses_library.go, which is not scanned"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("facts =\n %+v, want\n %+v", got, want)
	}
}

func installed(files map[string]string, pkgs map[string]string) map[string]string {
	for name, body := range pkgs {
		files["node_modules/"+name+"/package.json"] = body
	}
	return files
}

// What says a package nothing imports is used all the same: a script runs
// its binary, a file other than documentation names it, another dependency
// needs it as a peer, it holds the types of a package that is used.
func TestNpmUses(t *testing.T) {
	root := tree(t, installed(map[string]string{
		"package.json": `{
  "scripts": {"build": "tsc -p . && vite build"},
  "resolutions": {"left-pad": "1.3.0"},
  "dependencies": {
    "@types/node": "1.0.0", "@types/react": "1.0.0", "@types/babel__core": "1.0.0", "@types/lodash": "1.0.0",
    "@babel/core": "1.0.0", "@babel/preset-env": "1.0.0",
    "left-pad": "1.0.0", "lodash": "1.0.0", "normalize.css": "1.0.0", "pg": "1.0.0", "knex": "1.0.0",
    "react": "1.0.0", "react-dom": "1.0.0", "some-ui": "1.0.0", "typescript": "1.0.0", "vite": "1.0.0", "debug": "1.0.0",
    "husky": "1.0.0", "in-ci": "1.0.0", "sub-path": "1.0.0", "in-vue": "1.0.0",
    "babel-plugin-remove-things": "1.0.0", "eslint-config-airbnb": "1.0.0", "@types/gtag.js": "1.0.0"
  }
}`,
		"babel.config.json":        `{"presets": ["@babel/preset-env"], "plugins": ["remove-things"]}`,
		".eslintrc.yml":            "extends:\n  - airbnb\n",
		"tsconfig.json":            `{"compilerOptions": {"types": ["gtag.js"]}}`,
		"src/main.scss":            `@import "normalize.css";`,
		"src/db.ts":                "export const db = knex({ client: 'pg' });\n// debug the lodash thing\nconst level = `debug-${x}`;\n",
		"src/App.vue":              "<script>\nimport x from 'in-vue'\n</script>\n",
		"src/load.js":              "require.resolve(\"sub-path/register\");\n",
		".husky/pre-commit":        "npx husky run\n",
		".github/workflows/ci.yml": "steps:\n  - run: npx in-ci@1.2.3 --check\n",
		"README.md":                "Uses left-pad and lodash.",
		"package-lock.json":        `{"packages": {"node_modules/left-pad": {}, "node_modules/lodash": {}}}`,
		"packages/a/package.json":  `{"name": "a", "dependencies": {"left-pad": "1.0.0"}}`,
		"dist/bundle.js":           "/* lodash */ require('left-pad')",
		"renovate.json":            `{"ignoreDeps": ["left-pad"]}`,
	}, map[string]string{
		"@types/node": `{}`, "@types/react": `{}`, "@types/babel__core": `{}`, "@types/lodash": `{}`,
		"@babel/core": `{}`, "@babel/preset-env": `{"peerDependencies": {"@babel/core": "*"}}`,
		"left-pad": `{}`, "lodash": `{}`, "normalize.css": `{}`, "pg": `{}`, "knex": `{}`,
		"react": `{}`, "react-dom": `{"peerDependencies": {"react": "*"}}`,
		"some-ui":    `{"peerDependencies": {"react": "*", "react-dom": "*"}}`,
		"typescript": `{"bin": {"tsc": "bin/tsc", "tsserver": "bin/tsserver"}}`, "vite": `{"bin": "bin/vite.js"}`, "debug": `{}`,
		"husky": `{"bin": {"husky": "bin.js"}}`, "in-ci": `{}`, "sub-path": `{}`, "in-vue": `{}`,
		"babel-plugin-remove-things": `{}`, "eslint-config-airbnb": `{}`, "@types/gtag.js": `{}`,
	}))
	ms, m := only(t, root, "packages/a")
	imported := map[string]bool{"lodash": true, "knex": true, "react": true, "some-ui": true}
	got := factsOf(root, ms, m, []string{"@types/node", "@types/react", "@types/babel__core", "@types/lodash", "@babel/preset-env",
		"left-pad", "normalize.css", "pg", "react-dom", "typescript", "vite", "debug", "husky", "in-ci", "sub-path", "in-vue",
		"babel-plugin-remove-things", "eslint-config-airbnb", "@types/gtag.js"},
		Lookup{Imported: func(_ Manifest, n string) bool { return imported[n] }})
	want := map[string]Fact{
		"@types/node":        {Use: "the types of node"},
		"@types/react":       {Use: "the types of react"},
		"@types/babel__core": {Use: "the types of @babel/core"},
		"@types/lodash":      {Use: "the types of lodash"},
		"@babel/preset-env":  {Use: "named in babel.config.json"},
		"left-pad":           {},
		"normalize.css":      {Use: "named in src/main.scss"},
		"pg":                 {Use: "named in src/db.ts"},
		"react-dom":          {Use: "a peer dependency of some-ui"},
		"typescript":         {Use: "its binary tsc is named in package.json"},
		"vite":               {Use: "named in package.json"},
		"debug":              {},
		"husky":              {Use: "named in .husky/pre-commit"},
		"in-ci":              {Use: "named in .github/workflows/ci.yml"},
		"sub-path":           {Use: "named in src/load.js"},
		"in-vue":             {Use: "named in src/App.vue"},
		// a configuration names an add-on without its prefix, types by their package
		"babel-plugin-remove-things": {Use: "named in babel.config.json as remove-things"},
		"eslint-config-airbnb":       {Use: "named in .eslintrc.yml as airbnb"},
		"@types/gtag.js":             {Use: "named in tsconfig.json as gtag.js"},
	}
	for n, w := range want {
		if got[n] != w {
			t.Errorf("%s = %+v, want %+v", n, got[n], w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("facts = %+v", got)
	}
}

// Where a use that is not an import cannot be ruled out, the fact says why.
func TestNpmDoubts(t *testing.T) {
	root := tree(t, installed(map[string]string{
		"package.json": `{"dependencies": {
  "eslint": "1.0.0", "eslint-plugin-x": "1.0.0", "jest": "1.0.0", "coverage-thing": "1.0.0",
  "@babel/core": "1.0.0", "babel-plugin-y": "1.0.0", "@babel/runtime": "1.0.0",
  "vitest": "1.0.0", "@vitest/coverage-v8": "1.0.0",
  "@radix-ui/dialog": "1.0.0", "@radix-ui/popover": "1.0.0",
  "tslib": "1.0.0", "@types/gtag.js": "1.0.0", "not-installed": "1.0.0", "plain": "1.0.0",
  "sass-loader": "1.0.0", "@acme/eslint-config": "1.0.0", "preset": "1.0.0"
}}`,
	}, map[string]string{
		"eslint": `{}`, "eslint-plugin-x": `{}`, "jest": `{}`, "coverage-thing": `{"peerDependencies": {"jest": "*"}}`,
		"@babel/core": `{}`, "babel-plugin-y": `{}`, "@babel/runtime": `{}`, "vitest": `{}`, "@vitest/coverage-v8": `{}`,
		"@radix-ui/dialog": `{}`, "@radix-ui/popover": `{}`, "tslib": `{}`, "@types/gtag.js": `{}`, "plain": `{}`,
		"sass-loader": `{}`, "@acme/eslint-config": `{}`, "preset": `{}`,
	}))
	ms, m := only(t, root)
	got := factsOf(root, ms, m, []string{"eslint-plugin-x", "coverage-thing", "babel-plugin-y", "@babel/runtime", "@vitest/coverage-v8",
		"@radix-ui/popover", "tslib", "@types/gtag.js", "not-installed", "plain", "sass-loader", "@acme/eslint-config", "preset"}, Lookup{})
	want := map[string]Fact{
		"eslint-plugin-x":     {Doubt: "its name marks it as an add-on of eslint, which may load it without an import"},
		"coverage-thing":      {Doubt: "it is an add-on of jest (its peer), which may load it without an import"},
		"babel-plugin-y":      {Doubt: "its name marks it as an add-on of @babel/core, which may load it without an import"},
		"@babel/runtime":      {Doubt: "compilers insert the imports of it into what they build"},
		"@vitest/coverage-v8": {Doubt: "its name marks it as an add-on of vitest, which may load it without an import"},
		"@radix-ui/popover":   {Doubt: "not-installed is not installed, so whether it needs this package as a peer could not be read"},
		"tslib":               {Doubt: "compilers insert the imports of it into what they build"},
		"@types/gtag.js":      {Doubt: "TypeScript includes type declarations without an import"},
		"not-installed":       {Doubt: "it is not installed, so its binaries and its peers could not be read"},
		"plain":               {Doubt: "not-installed is not installed, so whether it needs this package as a peer could not be read"},
		"sass-loader":         {Doubt: "its name marks it as an add-on (a loader), which its host may load without an import"},
		"@acme/eslint-config": {Doubt: "its name marks it as an add-on of eslint, which may load it without an import"},
		"preset":              {Doubt: "its name marks it as an add-on (a preset), which its host may load without an import"},
	}
	if !reflect.DeepEqual(got, want) {
		for n, w := range want {
			if got[n] != w {
				t.Errorf("%s = %+v, want %+v", n, got[n], w)
			}
		}
	}
}

// Packages are found in the node_modules of the directories above too
// (a workspace hoists them), and a search that had to stop says so.
func TestNpmHoistedAndBudget(t *testing.T) {
	root := tree(t, installed(map[string]string{
		"package.json":            `{"name": "root", "workspaces": ["packages/*"]}`,
		"packages/a/package.json": `{"name": "a", "dependencies": {"plain": "1.0.0", "other": "1.0.0"}}`,
		"packages/a/a.txt":        "x",
		"packages/a/b.cfg":        "y",
		"packages/a/c.cfg":        "other",
	}, map[string]string{"plain": `{}`, "other": `{}`}))
	ms, m := only(t, root, "packages/a")
	if m.Path != "packages/a/package.json" {
		t.Fatalf("manifest = %+v", m)
	}
	if got := factsOf(root, ms, m, []string{"plain", "other"}, Lookup{}); !reflect.DeepEqual(got, map[string]Fact{"plain": {}, "other": {Use: "named in packages/a/c.cfg"}}) {
		t.Errorf("facts = %+v", got)
	}
	defer func(n int) { maxSearchFiles = n }(maxSearchFiles)
	maxSearchFiles = 1
	want := map[string]Fact{
		"plain": {Doubt: "the repository has too many files to search for its name"},
		"other": {Doubt: "the repository has too many files to search for its name"},
	}
	if got := factsOf(root, ms, m, []string{"plain", "other"}, Lookup{}); !reflect.DeepEqual(got, want) {
		t.Errorf("facts over budget = %+v", got)
	}
}

func TestMentions(t *testing.T) {
	for _, c := range []struct {
		text, word string
		quoted     bool
		want       bool
	}{
		{`"extends": "airbnb-base"`, "airbnb-base", false, true},
		{`run: npx prettier@3 --check .`, "prettier", false, true},
		{`node_modules/.bin/tsc -p .`, "tsc", false, true},
		{`node node_modules/webpack/bin/webpack.js`, "webpack", false, true},
		{`loader: sass-loader`, "sass", false, false},
		{`uses: lodash.debounce`, "lodash", false, false},
		{`use lodash.`, "lodash", false, true},
		{`@babel/core`, "core", false, false},
		{`my-react`, "react", false, false},
		{`react_native`, "react", false, false},
		{`require('pg')`, "pg", true, true},
		{"import(`pg/lib`)", "pg", true, true},
		{`// pg is the driver`, "pg", true, false},
		{`'pg-pool'`, "pg", true, false},
		{`'npx pg'`, "pg", true, false},
		{`"web/node_modules/@scope/pkg/dist/x.js"`, "@scope/pkg", true, true},
		{`"web/node_modules/@scope/pkg-two/dist/x.js"`, "@scope/pkg", true, false},
	} {
		if got := mentions([]byte(c.text), c.word, c.quoted); got != c.want {
			t.Errorf("mentions(%q, %q, quoted=%v) = %v, want %v", c.text, c.word, c.quoted, got, c.want)
		}
	}
}

// A file names a package for the manifest its own imports would use: a
// workflow or a script at the root of the repository names the packages of
// a package.json below it, a package that declares the package itself does
// not.
func TestNamedFromOutsideTheModule(t *testing.T) {
	root := tree(t, installed(map[string]string{
		"web/package.json":         `{"name": "web", "dependencies": {"in-ci": "1.0.0", "copied": "1.0.0", "sibling-uses": "1.0.0", "plain": "1.0.0"}}`,
		"other/package.json":       `{"name": "other", "dependencies": {"sibling-uses": "1.0.0", "plain": "1.0.0"}}`,
		"other/build.config.json":  `{"use": ["sibling-uses"]}`,
		".github/workflows/ci.yml": "steps:\n  - run: npx in-ci --check\n",
		"script/copy.mjs":          "copy(\"web/node_modules/copied/dist/x.js\");\n",
	}, map[string]string{}))
	for _, p := range []string{"in-ci", "copied", "sibling-uses", "plain"} {
		for _, dir := range []string{"web", "other"} {
			if err := os.MkdirAll(filepath.Join(root, dir, "node_modules", p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, dir, "node_modules", p, "package.json"), []byte(`{}`), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	ms := Declared(root, []string{"web", "other"})
	got, err := Facts(context.Background(), root, ms, map[string][]string{
		"web/package.json":   {"in-ci", "copied", "sibling-uses", "plain"},
		"other/package.json": {"sibling-uses", "plain"},
	}, Lookup{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]Fact{
		"web/package.json": {
			"in-ci":        {Use: "named in .github/workflows/ci.yml"},
			"copied":       {Use: "named in script/copy.mjs"},
			"sibling-uses": {},
			"plain":        {},
		},
		"other/package.json": {
			"sibling-uses": {Use: "named in other/build.config.json"},
			"plain":        {},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("facts =\n %+v, want\n %+v", got, want)
	}
}

// A search that is cut short (the hook's time limit) is an error, not a
// result: a package the search did not get to must not be reported.
func TestFactsStopWithTheContext(t *testing.T) {
	root := tree(t, installed(map[string]string{
		"package.json": `{"dependencies": {"plain": "1.0.0"}}`,
		"a.cfg":        "x",
	}, map[string]string{"plain": `{}`}))
	ms, m := only(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Facts(ctx, root, ms, map[string][]string{m.Path: {"plain"}}, Lookup{}); err == nil {
		t.Error("a cancelled search returned facts without an error")
	}
}
