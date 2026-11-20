package rules

import (
	"regexp"
	"testing"
)

func TestFindClonesDefaults(t *testing.T) {
	d := DefaultCloneOptions()
	if d.MinTokens <= 0 || d.MinSimilarity <= 0 || d.MinSimilarity > 1 || d.MaxFingerprintFreq <= 1 {
		t.Errorf("defaults = %+v", d)
	}
}

func TestIsTestFile(t *testing.T) {
	for p, want := range map[string]bool{
		"a/a_test.go": true, "web/src/x.test.ts": true, "src/x.spec.tsx": true, "app/test_x.py": true,
		"app/x_test.py": true, "tests/x.py": true, "src/__tests__/a.js": true, "pkg/x/testdata/a.go": true,
		"conftest.py": true,
		"a/a.go":      false, "src/latest.ts": false, "app/contest.py": false, "pkg/attest/a.go": false,
	} {
		if got := IsTestFile(p); got != want {
			t.Errorf("IsTestFile(%q) = %v, want %v", p, got, want)
		}
	}
}

// TestFileRegex accepts exactly the paths IsTestFile does.
func TestTestFileRegexMatchesIsTestFile(t *testing.T) {
	rx := regexp.MustCompile(`^(?:` + TestFileRegex + `)$`)
	for _, p := range []string{
		"a_test.go", "pkg/a_test.go", "pkg/a.go", "pkg/test.go", "src/a.test.ts", "src/a.spec.tsx", "src/a.ts", "src/latest.ts",
		"app/test_x.py", "app/x_test.py", "app/conftest.py", "app/tests.py", "app/test_x.txt", "tests/x.py", "app/tests/x.py",
		"spec/models/a.rb", "app/a_spec.rb", "app/a_test.rb", "app/spec.rb", "__tests__/a.js", "src/__tests__/a.js",
		"pkg/testdata/a.go", "testdata/a.go", "test/a.rb", "lib/test/a.rb", "contest/a.go", "lib/mytests/a.py", "a.spec", "x/.test.",
		"inspec/a.rb", "src/test_helper.rb", "src/a.testing.ts",
	} {
		if got, want := rx.MatchString(p), IsTestFile(p); got != want {
			t.Errorf("%s: regex %v, IsTestFile %v", p, got, want)
		}
	}
}
