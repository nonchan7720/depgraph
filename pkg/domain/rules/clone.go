package rules

import (
	"fmt"
	"math"
	"strings"
)

// CloneMember is one function / method / class of a group of copies.
type CloneMember struct {
	File, Name, Kind string
	Line, EndLine    int
}

func (c CloneMember) ref() string { return c.File + ":" + c.Name }

// CloneOptions tune duplicate-code; a zero field is the default.
type CloneOptions struct {
	// MinTokens: smaller code is not worth reporting (getters, one-liners).
	MinTokens int
	// MinSimilarity: Jaccard similarity of fingerprints for a near copy.
	MinSimilarity float64
	// MaxFingerprintFreq: a fingerprint found in more candidates than this
	// is boilerplate (error handling, loops), not evidence of copying.
	MaxFingerprintFreq int
}

// WithDefaults fills unset (zero) options with the defaults.
func (o CloneOptions) WithDefaults() CloneOptions {
	d := DefaultCloneOptions()
	if o.MinTokens == 0 {
		o.MinTokens = d.MinTokens
	}
	if o.MinSimilarity == 0 {
		o.MinSimilarity = d.MinSimilarity
	}
	if o.MaxFingerprintFreq == 0 {
		o.MaxFingerprintFreq = d.MaxFingerprintFreq
	}
	return o
}

// DefaultCloneOptions returns the defaults of duplicate-code.
func DefaultCloneOptions() CloneOptions {
	return CloneOptions{MinTokens: 50, MinSimilarity: 0.8, MaxFingerprintFreq: 10}
}

// DuplicateCode reports a group of copies (sorted by file:name, then
// line): identical, or similar with the lowest similarity of two of them.
// Its identity is its members, so moving code does not make it new.
func DuplicateCode(g []CloneMember, identical bool, similarity float64) Finding {
	refs := make([]string, len(g))
	places := make([]string, len(g))
	members := make([]map[string]any, len(g))
	for k, c := range g {
		refs[k] = c.ref()
		places[k] = fmt.Sprintf("%s:%d-%d %s", c.File, c.Line, c.EndLine, c.Name)
		members[k] = map[string]any{"file": c.File, "name": c.Name, "kind": c.Kind, "line": c.Line, "endLine": c.EndLine}
	}
	what := "identical logic"
	if !identical {
		// floor: never round 79.6% up to the 80% threshold
		what = fmt.Sprintf("similar logic (%d%%)", int(math.Floor(similarity*100)))
	}
	return Finding{
		Rule: "duplicate-code", Severity: "warn",
		ID:      strings.Join(refs, "|"),
		Message: fmt.Sprintf("%s in %d places: %s", what, len(g), strings.Join(places, ", ")),
		Data:    map[string]any{"members": members, "identical": identical},
	}
}

// TestFileRegex matches (as a whole, RE2 and Java) the repo-relative paths
// IsTestFile accepts, for the queries of the store.
const TestFileRegex = `(.*/)?([^/]*_test\.go|[^/]*\.(test|spec)\.[^/]*|test_[^/]*\.py|[^/]*_test\.py|conftest\.py|[^/]*_(spec|test)\.rb)` +
	`|(.*/)?(__tests__|tests|test|testdata|spec)/.*`

// IsTestFile reports whether a repo-relative path is test code by the
// conventions of the supported languages.
func IsTestFile(p string) bool {
	base := p[strings.LastIndex(p, "/")+1:]
	switch {
	case strings.HasSuffix(base, "_test.go"),
		strings.Contains(base, ".test."), strings.Contains(base, ".spec."),
		strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py"),
		strings.HasSuffix(base, "_test.py"), base == "conftest.py",
		strings.HasSuffix(base, "_spec.rb"), strings.HasSuffix(base, "_test.rb"):
		return true
	}
	for _, dir := range []string{"__tests__/", "tests/", "test/", "testdata/", "spec/"} {
		if strings.HasPrefix(p, dir) || strings.Contains(p, "/"+dir) {
			return true
		}
	}
	return false
}
