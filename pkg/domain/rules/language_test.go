package rules

import (
	"strings"
	"testing"
)

// The setting of a file is the one of the deepest directory holding it:
// the root's, a component's (a pattern such as packages/* is one setting
// per directory it matches), or a nested .depgraph.yml's.
func TestLanguageSettingFor(t *testing.T) {
	c := DefaultConfig()
	c.Languages = []LanguageSetting{
		{Dir: ".", Lang: "python", VersionFile: ".python-version"},
		{Dir: "lib/foo", Lang: "python", VersionFile: ".python-version"},
		{Dir: "packages/*", Lang: "python", Version: "3.10"},
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ file, dir string }{
		{"app/main.py", "."},
		{"lib/foo/foo/core.py", "lib/foo"},
		{"lib/foobar/x.py", "."},
		{"packages/a/src/x.py", "packages/a"},
		{"packages/x.py", "."},
	} {
		s, ok := c.LanguageSettingFor("python", tc.file)
		if !ok || s.Dir != tc.dir {
			t.Errorf("%s: %+v %v, want dir %s", tc.file, s, ok, tc.dir)
		}
	}
	if _, ok := c.LanguageSettingFor("ruby", "app/x.rb"); ok {
		t.Error("ruby has no setting")
	}
}

func TestLanguageSettingValidate(t *testing.T) {
	for _, tc := range []struct {
		s    LanguageSetting
		want string
	}{
		{LanguageSetting{Dir: ".", Lang: "ruby", VersionFile: ".ruby-version"}, `language "ruby" is not supported yet`},
		{LanguageSetting{Dir: ".", Lang: "python"}, "versionFile or version"},
		{LanguageSetting{Dir: ".", Lang: "python", Version: "3.12", VersionFile: ".python-version"}, "not both"},
		{LanguageSetting{Dir: ".", Lang: "python", Version: ">=3.10"}, `version ">=3.10"`},
		{LanguageSetting{Dir: ".", Lang: "python", Version: "3"}, `version "3"`},
		{LanguageSetting{Dir: ".", Lang: "python", VersionFile: "/etc/python-version"}, "relative"},
		{LanguageSetting{Dir: "lib", Lang: "python", VersionFile: "../../x"}, "outside the repository"},
		{LanguageSetting{Dir: ".", Lang: "python", Version: "3.12", Stdlib: "../typeshed"}, "outside the repository"},
	} {
		c := DefaultConfig()
		c.Languages = []LanguageSetting{tc.s}
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%+v: err = %v, want %q", tc.s, err, tc.want)
		}
	}
	c := DefaultConfig()
	c.Languages = []LanguageSetting{{Dir: "lib", Lang: "python", VersionFile: "../.python-version"}, {Dir: ".", Lang: "python", Version: "3.12.4"}}
	if err := c.Validate(); err != nil {
		t.Errorf("a path up to the root and a full version are fine: %v", err)
	}
	c.Languages = append(c.Languages, LanguageSetting{Dir: "lib", Lang: "python", Version: "3.11"})
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("two settings for one directory: %v", err)
	}
}

func TestPathFrom(t *testing.T) {
	for _, tc := range []struct{ dir, p, want string }{
		{".", ".python-version", ".python-version"},
		{"lib/foo", ".python-version", "lib/foo/.python-version"},
		{"lib/foo", "../../.tool-versions", ".tool-versions"},
		{"lib/foo", "./cfg/mise.toml", "lib/foo/cfg/mise.toml"},
	} {
		if got := (LanguageSetting{Dir: tc.dir}).PathFrom(tc.p); got != tc.want {
			t.Errorf("PathFrom(%q, %q) = %q, want %q", tc.dir, tc.p, got, tc.want)
		}
	}
}

func TestMinorVersion(t *testing.T) {
	for in, want := range map[string]string{"3.12": "3.12", "3.12.4": "3.12", "3.9.18": "3.9"} {
		if got, ok := MinorVersion(in); !ok || got != want {
			t.Errorf("MinorVersion(%q) = %q, %v", in, got, ok)
		}
	}
	for _, bad := range []string{"", "3", "3.x", ">=3.10", "3.12-dev", "pypy3.10", "system", "latest", "3.12.4.1"} {
		if _, ok := MinorVersion(bad); ok {
			t.Errorf("MinorVersion(%q) accepted", bad)
		}
	}
}
