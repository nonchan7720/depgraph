package config

import (
	"strings"
	"testing"
)

func TestAddLanguageSettings(t *testing.T) {
	src := `# yaml-language-server: $schema=./schema/depgraph.schema.json
# my architecture
layers:
  domain: ["pkg/domain/**"] # the core
components:
  lib/foo:
    forbidden: {domain: [infra]}
rules:
  god-package: {fanIn: 12}
`
	out, err := AddLanguageSettings([]byte(src), []LanguageEntry{
		{Dir: ".", Lang: "python", VersionFile: ".python-version"},
		{Dir: "lib/foo", Lang: "python", VersionFile: ".python-version"},
		{Dir: "svc/a", Lang: "python", VersionFile: "Dockerfile"},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, keep := range []string{"# my architecture", "# the core", "god-package: {fanIn: 12}", "forbidden: {domain: [infra]}"} {
		if !strings.Contains(s, keep) {
			t.Errorf("lost %q:\n%s", keep, s)
		}
	}
	f, err := Parse(out)
	if err != nil {
		t.Fatalf("%v\n%s", err, s)
	}
	got := map[string]string{}
	for _, l := range f.Rules.Languages {
		got[l.Dir] = l.VersionFile
	}
	if got["."] != ".python-version" || got["lib/foo"] != ".python-version" || got["svc/a"] != "Dockerfile" || len(got) != 3 {
		t.Errorf("languages = %v\n%s", got, s)
	}
	if len(f.Rules.Components) != 1 { // svc/a is a languages-only entry
		t.Errorf("components = %+v", f.Rules.Components)
	}

	// an empty file, and a setting that is already there
	out, err = AddLanguageSettings(nil, []LanguageEntry{{Dir: ".", Lang: "python", VersionFile: ".python-version"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AddLanguageSettings(out, []LanguageEntry{{Dir: ".", Lang: "python", VersionFile: "mise.toml"}}); err == nil ||
		!strings.Contains(err.Error(), "already set") {
		t.Errorf("overwrite: %v", err)
	}
}
