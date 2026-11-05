package rules

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// NamePattern is a pattern over whole symbol names (duplicate-symbol's
// ignore). "*" is any run of characters (also none) and "?" one character;
// everything else is literal, so a name without them is that exact name and
// "Test*" is every name starting with Test.
type NamePattern struct {
	pattern string
	regex   string
	re      *regexp.Regexp
}

// CompileNamePattern parses pattern.
func CompileNamePattern(pattern string) (NamePattern, error) {
	if strings.TrimSpace(pattern) == "" {
		return NamePattern{}, errors.New("empty name pattern")
	}
	var b strings.Builder
	b.WriteString("^")
	for _, r := range pattern {
		switch r {
		case '*':
			b.WriteString(".*")
		case '?':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return NamePattern{}, fmt.Errorf("name pattern %q: %w", pattern, err)
	}
	return NamePattern{pattern: pattern, regex: b.String(), re: re}, nil
}

// Match reports whether the symbol name matches.
func (p NamePattern) Match(name string) bool { return p.re != nil && p.re.MatchString(name) }

// Regex is the anchored regular expression of p, with the same meaning in
// Go (RE2) and Java (Neo4j's =~).
func (p NamePattern) Regex() string { return p.regex }

// String returns the pattern as written.
func (p NamePattern) String() string { return p.pattern }

// DuplicateNameRegexes compiles IgnoreDuplicateNames into the regular
// expressions the store filters symbol names with.
func (c Config) DuplicateNameRegexes() ([]string, error) {
	out := make([]string, 0, len(c.IgnoreDuplicateNames))
	for _, n := range c.IgnoreDuplicateNames {
		p, err := CompileNamePattern(n)
		if err != nil {
			return nil, fmt.Errorf("rule %s: ignore: %w", RuleDuplicateSymbol, err)
		}
		out = append(out, p.regex)
	}
	return out, nil
}
