package rules

import (
	"regexp"
	"testing"
)

func TestGlobMatch(t *testing.T) {
	tests := []struct {
		pattern string
		match   []string
		noMatch []string
	}{
		{"pkg/domain/**", []string{"pkg/domain", "pkg/domain/rules", "pkg/domain/a/b"}, []string{"pkg/domainx", "pkg", "x/pkg/domain"}},
		{"**/db/**", []string{"db", "pkg/db", "pkg/db/sql", "db/x"}, []string{"pkg/feedback", "pkg/dbx", "pkg/xdb/y"}},
		{"**/handler", []string{"handler", "a/handler", "a/b/handler"}, []string{"a/eventhandler", "a/handler/x"}},
		{"pkg/*/repository/**", []string{"pkg/infrastructure/repository", "pkg/x/repository/graph"}, []string{"pkg/repository", "pkg/a/b/repository"}},
		{"a/**/b", []string{"a/b", "a/x/b", "a/x/y/b"}, []string{"a/xb", "ab", "a/b/c"}},
		{"cmd/app?", []string{"cmd/app1"}, []string{"cmd/app", "cmd/app12", "cmd/app/"}},
		{"**", []string{".", "a", "a/b"}, nil},
		{"src/app.v2/**", []string{"src/app.v2/x"}, []string{"src/appXv2/x"}},
		{"app/[id]/**", []string{"app/[id]/page"}, []string{"app/i/page"}},
		{"./pkg/domain/", []string{"pkg/domain"}, []string{"pkg/domain/x"}},
		{".", []string{"."}, []string{"a"}},
	}
	for _, tt := range tests {
		g, err := CompileGlob(tt.pattern)
		if err != nil {
			t.Fatalf("%q: %v", tt.pattern, err)
		}
		// the regex is also sent to Neo4j (Java regex): it must compile in Go
		// with the same meaning, which Match relies on.
		if _, err := regexp.Compile(g.Regex()); err != nil {
			t.Fatalf("%q: regex %q: %v", tt.pattern, g.Regex(), err)
		}
		for _, k := range tt.match {
			if !g.Match(k) {
				t.Errorf("%q should match %q (regex %s)", tt.pattern, k, g.Regex())
			}
		}
		for _, k := range tt.noMatch {
			if g.Match(k) {
				t.Errorf("%q should not match %q (regex %s)", tt.pattern, k, g.Regex())
			}
		}
	}
}

func TestCompileGlobErrors(t *testing.T) {
	for _, p := range []string{"", "/", "pkg/**x", "a/b**", "a//b"} {
		if _, err := CompileGlob(p); err == nil {
			t.Errorf("%q: error expected", p)
		}
	}
}
