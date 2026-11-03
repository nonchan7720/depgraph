package rules

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Glob is a pattern over package keys (slash-separated directory paths from
// the repository root, "." for the root). It matches whole path segments:
//
//	**  zero or more segments (must be a whole segment)
//	*   any characters within one segment
//	?   one character within one segment
//
// Everything else is literal. A leading "./" and a trailing "/" are ignored.
// "pkg/domain/**" matches "pkg/domain" and everything below it.
type Glob struct {
	pattern string
	regex   string
	re      *regexp.Regexp
}

// CompileGlob parses pattern.
func CompileGlob(pattern string) (Glob, error) {
	p := strings.TrimSuffix(strings.TrimPrefix(pattern, "./"), "/")
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return Glob{}, errors.New("empty package pattern")
	}
	segs := strings.Split(p, "/")
	var b strings.Builder
	b.WriteString("^")
	for i, seg := range segs {
		last := i == len(segs)-1
		switch {
		case seg == "":
			return Glob{}, fmt.Errorf("package pattern %q: empty path segment", pattern)
		case seg == "**":
			switch {
			case i == 0 && last:
				b.WriteString(".*")
			case i == 0:
				b.WriteString("(?:.*/)?") // consumes the separator before the next segment
			case last:
				b.WriteString("(?:/.*)?")
			default:
				b.WriteString("(?:/.*)?/")
			}
			continue
		case strings.Contains(seg, "**"):
			return Glob{}, fmt.Errorf("package pattern %q: ** must be a whole path segment", pattern)
		}
		if i > 0 && segs[i-1] != "**" {
			b.WriteString("/")
		}
		for _, r := range seg {
			switch r {
			case '*':
				b.WriteString("[^/]*")
			case '?':
				b.WriteString("[^/]")
			default:
				b.WriteString(regexp.QuoteMeta(string(r)))
			}
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return Glob{}, fmt.Errorf("package pattern %q: %w", pattern, err)
	}
	return Glob{pattern: pattern, regex: b.String(), re: re}, nil
}

// Match reports whether the package key matches.
func (g Glob) Match(key string) bool { return g.re != nil && g.re.MatchString(key) }

// Regex is the anchored regular expression of g. It uses only syntax that
// means the same in Go (RE2) and Java (Neo4j's =~), so the store can filter
// with it.
func (g Glob) Regex() string { return g.regex }

// String returns the pattern as written.
func (g Glob) String() string { return g.pattern }

// compileGlobs compiles every pattern; the error names the first bad one.
func compileGlobs(patterns []string) ([]Glob, error) {
	out := make([]Glob, 0, len(patterns))
	for _, p := range patterns {
		g, err := CompileGlob(p)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, nil
}

// Regexes returns the regular expressions of gs.
func Regexes(gs []Glob) []string {
	out := make([]string, len(gs))
	for i, g := range gs {
		out[i] = g.regex
	}
	return out
}

func matchAny(gs []Glob, key string) bool {
	for _, g := range gs {
		if g.Match(key) {
			return true
		}
	}
	return false
}
