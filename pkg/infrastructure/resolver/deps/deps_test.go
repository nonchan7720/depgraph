package deps

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// tree writes files (path -> content) under a new directory.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func names(m Manifest) []string {
	var out []string
	for _, d := range m.Deps {
		out = append(out, d.Name)
	}
	return out
}

func byPath(t *testing.T, ms []Manifest, p string) Manifest {
	t.Helper()
	for _, m := range ms {
		if m.Path == p {
			return m
		}
	}
	t.Fatalf("no manifest %s in %v", p, ms)
	return Manifest{}
}

const goMod = `module example.com/app

go 1.24

require (
	github.com/used/lib v1.0.0
	github.com/unused/lib v1.2.0
	github.com/aws/sdk v1.0.0
	github.com/aws/sdk/service/s3 v1.0.0
	github.com/tool/gen v0.1.0
	example.com/sibling v0.0.0
	example.com/local v0.0.0
	golang.org/x/indirect v0.1.0 // indirect
)

tool github.com/tool/gen/cmd/gen

replace example.com/local => ./third_party/local
`

// The direct requirements of a go.mod are its dependencies: not the
// indirect ones, not a module of the repository and not one replaced by a
// directory of the repository (their imports resolve inside it).
func TestReadGoMod(t *testing.T) {
	root := tree(t, map[string]string{
		"go.mod":                     goMod,
		"sibling/go.mod":             "module example.com/sibling\n",
		"third_party/local/go.mod":   "module example.com/local\n",
		"third_party/local/local.go": "package local\n",
	})
	ms := Declared(root, []string{"sibling"})
	m := byPath(t, ms, "go.mod")
	want := []string{"github.com/aws/sdk", "github.com/aws/sdk/service/s3", "github.com/tool/gen", "github.com/unused/lib", "github.com/used/lib"}
	if got := names(m); !reflect.DeepEqual(got, want) {
		t.Errorf("deps = %q, want %q", got, want)
	}
	if m.Ecosystem != Go || m.Dir != "." || !reflect.DeepEqual(m.Languages(), []string{"go"}) {
		t.Errorf("manifest = %+v", m)
	}
	for _, d := range m.Deps {
		if d.Name == "github.com/unused/lib" && (d.Line != 7 || d.Section != "require") {
			t.Errorf("unused/lib = %+v, want line 7 of require", d)
		}
	}
	// an import belongs to the module with the longest path that provides it
	for key, want := range map[string]string{
		"ext:github.com/used/lib":               "github.com/used/lib",
		"ext:github.com/used/lib/sub/pkg":       "github.com/used/lib",
		"ext:github.com/used/library":           "",
		"ext:github.com/aws/sdk/service/s3/api": "github.com/aws/sdk/service/s3",
		"ext:github.com/aws/sdk/config":         "github.com/aws/sdk",
		"ext:golang.org/x/indirect/unix":        "", // not a direct requirement
		"ext:fmt":                               "",
	} {
		if got := m.Provider(key); got != want {
			t.Errorf("Provider(%s) = %q, want %q", key, got, want)
		}
	}
	if !m.Governs(".") || m.Governs("sibling") {
		t.Error("a go.mod governs the files of its own module only")
	}
	if !m.Declares("ext:golang.org/x/indirect/unix") || !m.Declares("ext:github.com/used/lib") || m.Declares("ext:github.com/other/lib") {
		t.Error("a go.mod declares what it requires, directly or not")
	}
}

const appPackageJSON = `{
  "name": "app",
  "scripts": {"build": "tsc -p . && vite build", "lint": "eslint ."},
  "dependencies": {
    "react": "18.3.1",
    "left-pad": "1.3.0",
    "@app/shared": "workspace:*",
    "local-thing": "file:../local-thing"
  },
  "devDependencies": {
    "typescript": "5.4.5",
    "vite": "5.2.0"
  },
  "optionalDependencies": {"fsevents": "2.3.3"},
  "peerDependencies": {"react-dom": "18.3.1"}
}
`

// dependencies, devDependencies and optionalDependencies of a package.json
// are its dependencies: not the peers (a statement to who installs the
// package) and not packages of the repository itself.
func TestReadPackageJSON(t *testing.T) {
	root := tree(t, map[string]string{
		"web/package.json":             appPackageJSON,
		"web/packages/ui/package.json": `{"name": "left-pad-fork", "dependencies": {"app": "*", "clsx": "2.1.0"}}`,
	})
	ms := Declared(root, []string{"web", "web/packages/ui"})
	if len(ms) != 2 {
		t.Fatalf("manifests = %+v", ms)
	}
	m := byPath(t, ms, "web/package.json")
	if got, want := names(m), []string{"fsevents", "left-pad", "react", "typescript", "vite"}; !reflect.DeepEqual(got, want) {
		t.Errorf("deps = %q, want %q", got, want)
	}
	if m.Ecosystem != Npm || !reflect.DeepEqual(m.Languages(), []string{"javascript", "typescript"}) {
		t.Errorf("manifest = %+v", m)
	}
	lines := map[string]int{}
	for _, d := range m.Deps {
		lines[d.Name+" "+d.Section] = d.Line
	}
	if want := map[string]int{"react dependencies": 5, "left-pad dependencies": 6, "typescript devDependencies": 11, "vite devDependencies": 12, "fsevents optionalDependencies": 14}; !reflect.DeepEqual(lines, want) {
		t.Errorf("lines = %v, want %v", lines, want)
	}
	if got := m.Provider("ext:react"); got != "react" {
		t.Errorf("Provider(ext:react) = %q", got)
	}
	if got := m.Provider("ext:react-dom"); got != "" {
		t.Errorf("Provider(ext:react-dom) = %q, want none (a peer)", got)
	}
	// a workspace package may use what a package.json above it declares
	if !m.Governs("web") || !m.Governs("web/packages/ui") || m.Governs("webapp") || m.Governs(".") {
		t.Error("a package.json governs its directory and the packages below it")
	}
	// "app" is the package of web/package.json: not a dependency
	if got, want := names(byPath(t, ms, "web/packages/ui/package.json")), []string{"clsx"}; !reflect.DeepEqual(got, want) {
		t.Errorf("deps of ui = %q, want %q", got, want)
	}
}

// The signature changes with what the manifests declare and with whether
// the packages are installed, and with nothing else.
func TestSignature(t *testing.T) {
	files := map[string]string{"package.json": `{"dependencies": {"a": "1.0.0"}}`}
	root := tree(t, files)
	s1 := Signature(root, Declared(root, nil))
	if s2 := Signature(root, Declared(root, nil)); s1 != s2 {
		t.Error("the signature of the same manifests differs")
	}
	if err := os.MkdirAll(filepath.Join(root, "node_modules", "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	s3 := Signature(root, Declared(root, nil))
	if s3 == s1 {
		t.Error("installing the packages must change the signature")
	}
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"dependencies": {"a": "1.0.0", "b": "1.0.0"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if Signature(root, Declared(root, nil)) == s3 {
		t.Error("a new dependency must change the signature")
	}
	if Signature(root, nil) != "" {
		t.Error("no manifest: no signature")
	}
}

func TestReadSkipsWhatCannotBeRead(t *testing.T) {
	root := tree(t, map[string]string{"package.json": "{ not json", "sub/go.mod": "module\n\nrequire (\n"})
	if ms := Declared(root, []string{"sub", "missing"}); len(ms) != 0 {
		t.Errorf("manifests = %+v, want none", ms)
	}
	if ms := Declared("", []string{"sub"}); ms != nil {
		t.Errorf("no root: %+v", ms)
	}
}

// An import uses the dependency of the nearest manifest that declares the
// package; when no manifest of the importing file does, every other
// manifest that declares it may be what provides it.
func TestUsers(t *testing.T) {
	root := tree(t, map[string]string{
		"package.json":            `{"name": "root", "devDependencies": {"typescript": "1.0.0", "lodash": "1.0.0"}}`,
		"packages/a/package.json": `{"name": "a", "dependencies": {"lodash": "1.0.0", "root": "workspace:*"}, "peerDependencies": {"react": "*"}}`,
		"packages/b/package.json": `{"name": "b", "dependencies": {"playwright": "1.0.0", "react": "1.0.0"}}`,
		"go.mod":                  "module example.com/app\n\nrequire github.com/x/lib v1.0.0\n",
		"tools/go.mod":            "module example.com/app/tools\n\nrequire (\n\tgithub.com/y/lib v1.0.0\n\tgithub.com/z/lib v1.0.0 // indirect\n)\n",
	})
	ix := NewIndex(Declared(root, []string{"packages/a", "packages/b", "tools"}))
	for _, c := range []struct {
		lang, dir, key string
		want           []Ref
	}{
		// the package's own declaration, not the one above it
		{"typescript", "packages/a", "ext:lodash", []Ref{{"packages/a/package.json", "lodash"}}},
		{"javascript", "packages/b", "ext:lodash", []Ref{{"package.json", "lodash"}}},
		{"typescript", ".", "ext:lodash", []Ref{{"package.json", "lodash"}}},
		{"typescript", "packages/a", "ext:typescript", []Ref{{"package.json", "typescript"}}},
		// a declares react as a peer: it is not b's dependency that it uses
		{"typescript", "packages/a", "ext:react", nil},
		// nothing above the file declares it: what a sibling installs
		{"javascript", ".", "ext:playwright", []Ref{{"packages/b/package.json", "playwright"}}},
		{"javascript", "packages/a", "ext:playwright", []Ref{{"packages/b/package.json", "playwright"}}},
		{"javascript", "packages/a", "ext:unknown", nil},
		// a workspace package is not a dependency
		{"typescript", "packages/a", "ext:root", nil},
		// a Go file never uses a package.json, a Python file no manifest that is read
		{"go", ".", "ext:lodash", nil},
		{"python", ".", "ext:lodash", nil},
		{"go", ".", "ext:github.com/x/lib/sub", []Ref{{"go.mod", "github.com/x/lib"}}},
		// a module's own go.mod only, unless it does not require the module at all
		{"go", "tools", "ext:github.com/y/lib", []Ref{{"tools/go.mod", "github.com/y/lib"}}},
		{"go", ".", "ext:github.com/y/lib", []Ref{{"tools/go.mod", "github.com/y/lib"}}},
		{"go", "tools", "ext:github.com/z/lib", nil},
		{"go", "tools", "ext:github.com/x/lib", []Ref{{"go.mod", "github.com/x/lib"}}},
	} {
		if got := ix.Users(c.lang, c.dir, c.key); !reflect.DeepEqual(got, c.want) {
			t.Errorf("Users(%s, %s, %s) = %v, want %v", c.lang, c.dir, c.key, got, c.want)
		}
	}
}
