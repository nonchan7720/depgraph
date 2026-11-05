package rules

import (
	"regexp"
	"testing"
)

func TestNamePatternMatch(t *testing.T) {
	tests := []struct {
		pattern string
		match   []string
		noMatch []string
	}{
		// no wildcard: the exact name, as before
		{"New", []string{"New"}, []string{"NewClient", "new", "XNew"}},
		// prefix
		{"Test*", []string{"Test", "TestFoo", "Test_bar"}, []string{"test", "MyTest", "Tes"}},
		// suffix and infix
		{"*Handler", []string{"Handler", "UserHandler"}, []string{"Handlers", "HandlerX"}},
		{"use*State", []string{"useState", "useFormState"}, []string{"useStates", "xuseState"}},
		{"Get?", []string{"GetX"}, []string{"Get", "GetXY"}},
		{"*", []string{"a", "Anything"}, nil},
		// regex metacharacters are literal
		{"$scope", []string{"$scope"}, []string{"scope", "x$scope"}},
		{"a.b", []string{"a.b"}, []string{"aXb"}},
		{"[id]", []string{"[id]"}, []string{"i"}},
		{"日本*", []string{"日本語"}, []string{"中日本"}},
	}
	for _, tt := range tests {
		p, err := CompileNamePattern(tt.pattern)
		if err != nil {
			t.Fatalf("%q: %v", tt.pattern, err)
		}
		// the regex is sent to Neo4j (Java regex) as well
		if _, err := regexp.Compile(p.Regex()); err != nil {
			t.Fatalf("%q: regex %q: %v", tt.pattern, p.Regex(), err)
		}
		for _, n := range tt.match {
			if !p.Match(n) {
				t.Errorf("%q should match %q (regex %s)", tt.pattern, n, p.Regex())
			}
		}
		for _, n := range tt.noMatch {
			if p.Match(n) {
				t.Errorf("%q should not match %q (regex %s)", tt.pattern, n, p.Regex())
			}
		}
	}
}

func TestNamePatternInvalid(t *testing.T) {
	for _, p := range []string{"", "  "} {
		if _, err := CompileNamePattern(p); err == nil {
			t.Errorf("%q accepted", p)
		}
	}
	c := DefaultConfig()
	c.IgnoreDuplicateNames = []string{"Test*", ""}
	if err := c.Validate(); err == nil {
		t.Error("Validate accepted an empty duplicate-symbol ignore pattern")
	}
}

// The default names are exact names: a pattern match must not widen them.
func TestDuplicateNameRegexesDefaults(t *testing.T) {
	rxs, err := DefaultConfig().DuplicateNameRegexes()
	if err != nil {
		t.Fatal(err)
	}
	if len(rxs) != len(DefaultConfig().IgnoreDuplicateNames) {
		t.Fatalf("got %d regexes", len(rxs))
	}
	for _, rx := range rxs {
		if regexp.MustCompile(rx).MatchString("NewClient") {
			t.Errorf("default %s matches NewClient", rx)
		}
	}
}
