package imports

import (
	"strings"
	"testing"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

const typeshedVersions = `# The structure of this file is as follows:
__future__: 3.0-
asyncio: 3.4-
asyncio.taskgroups: 3.11-
distutils: 3.0-3.11
os: 3.0-
tomllib: 3.11-
annotationlib: 3.14-
`

const pyrightVersions = "node_modules/pyright/dist/typeshed-fallback/stdlib/VERSIONS"

func TestPythonStdlibClassify(t *testing.T) {
	root := tree(t, map[string]string{pyrightVersions: typeshedVersions})
	std, err := LoadPythonStdlib(root, rules.LanguageSetting{Dir: ".", Lang: "python", Version: "3.12"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		target, minor, class string
	}{
		{"ext:os", "3.12", ClassStandard},
		{"ext:os.path", "3.12", ClassStandard}, // a submodule: its top-level module decides
		{"ext:asyncio.taskgroups", "3.9", ClassStandard},
		{"ext:tomllib", "3.12", ClassStandard},
		{"ext:tomllib", "3.10", ClassThirdParty}, // not in 3.10's standard library: a package of that name
		{"ext:distutils", "3.11", ClassStandard},
		{"ext:distutils", "3.12", ClassThirdParty}, // removed in 3.12: setuptools provides it
		{"ext:annotationlib", "3.12", ClassThirdParty},
		{"ext:yaml", "3.12", ClassThirdParty},
		{"ext:requests", "3.12", ClassThirdParty},
	} {
		class, basis := std.Classify(tc.target, tc.minor)
		if class != tc.class || !strings.Contains(basis, "node_modules/pyright") {
			t.Errorf("Classify(%s, %s) = %q %q, want %q", tc.target, tc.minor, class, basis, tc.class)
		}
	}
}

// Several copies must agree on a module; a disagreement is not settled.
func TestPythonStdlibCopiesMustAgree(t *testing.T) {
	root := tree(t, map[string]string{
		pyrightVersions: typeshedVersions,
		"node_modules/basedpyright/dist/typeshed-fallback/stdlib/VERSIONS": strings.Replace(typeshedVersions, "tomllib: 3.11-", "tomllib: 3.10-", 1),
	})
	std, err := LoadPythonStdlib(root, rules.LanguageSetting{Dir: ".", Lang: "python", Version: "3.12"})
	if err != nil {
		t.Fatal(err)
	}
	if class, _ := std.Classify("ext:os", "3.12"); class != ClassStandard {
		t.Errorf("os: %q", class)
	}
	if class, basis := std.Classify("ext:tomllib", "3.10"); class != "" || !strings.Contains(basis, "disagree") {
		t.Errorf("tomllib on 3.10: %q %q, want unknown", class, basis)
	}
}

// The copy is looked up from the setting's directory up to the root
// (node_modules of a workspace package, then of the root); an explicit
// stdlib directory replaces the lookup.
func TestPythonStdlibLocation(t *testing.T) {
	root := tree(t, map[string]string{
		"services/api/" + pyrightVersions: typeshedVersions,
		"typeshed/stdlib/VERSIONS":        "os: 3.0-\n",
	})
	if _, err := LoadPythonStdlib(root, rules.LanguageSetting{Dir: ".", Lang: "python", Version: "3.12"}); err == nil ||
		!strings.Contains(err.Error(), "no typeshed stdlib") {
		t.Errorf("root without a copy: %v", err)
	}
	std, err := LoadPythonStdlib(root, rules.LanguageSetting{Dir: "services/api", Lang: "python", Version: "3.12"})
	if err != nil {
		t.Fatal(err)
	}
	if _, basis := std.Classify("ext:os", "3.12"); !strings.Contains(basis, "services/api/node_modules/pyright") {
		t.Errorf("basis %q", basis)
	}
	std, err = LoadPythonStdlib(root, rules.LanguageSetting{Dir: "lib", Lang: "python", Version: "3.12", Stdlib: "../typeshed/stdlib"})
	if err != nil {
		t.Fatal(err)
	}
	if class, basis := std.Classify("ext:asyncio", "3.12"); class != ClassThirdParty || !strings.Contains(basis, "typeshed/stdlib/VERSIONS") {
		t.Errorf("explicit: %q %q", class, basis)
	}
	if _, err := LoadPythonStdlib(root, rules.LanguageSetting{Dir: ".", Lang: "python", Version: "3.12", Stdlib: "missing"}); err == nil {
		t.Error("a missing explicit stdlib is an error")
	}
}

func TestParseTypeshedVersions(t *testing.T) {
	if _, err := parseTypeshedVersions([]byte("os: 3.0-\nbroken line\n")); err == nil {
		t.Error("a malformed line is an error")
	}
	if _, err := parseTypeshedVersions([]byte("# only comments\n")); err == nil {
		t.Error("an empty file is an error")
	}
}
