package rules

import (
	"fmt"
	"slices"
	"strings"
)

// RuleWideFunction: a function that calls functions of more packages of
// the repository than Config.MaxCalledPackages: it knows too much of the
// project to be changed or tested alone, and every package it reaches is a
// reason to change it. A linter counts lines and branches of one function;
// how many packages it reaches only the whole project shows.
//
// Calls of constructors (Go NewX, a class in TypeScript / JavaScript and
// Python) are not counted: a constructor or a composition root builds its
// parts from many packages without depending on what they do.
//
// A function that is wide on purpose (the composition root that wires the
// application) says so: `depgraph:ignore wide-function` in a comment on it.
const RuleWideFunction = "wide-function"

// DefaultMaxCalledPackages is the most packages a function may call into
// without being reported.
const DefaultMaxCalledPackages = 8

func init() {
	builtin = append(builtin, Rule{Name: RuleWideFunction, Severity: SeverityInfo,
		Description: "a function that calls functions of more than maxPackages packages of the repository",
		Options:     []string{"maxPackages"}})
}

// CalledPackagesLimit is the most packages a function may call into.
func (c Config) CalledPackagesLimit() int {
	if c.MaxCalledPackages > 0 {
		return c.MaxCalledPackages
	}
	return DefaultMaxCalledPackages
}

// IgnoresRule reports whether the rules a `depgraph:ignore` comment names
// (ignored) include rule.
func IgnoresRule(ignored []string, rule string) bool { return slices.Contains(ignored, rule) }

const wideListed = 10

// WideFunction reports the function name at file:line, which calls
// functions of packages (sorted; more than limit). Its identity is file and
// name.
func WideFunction(file string, line int, name string, packages []string, limit int) Finding {
	shown := packages
	if len(shown) > wideListed {
		shown = shown[:wideListed]
	}
	list := strings.Join(shown, ", ")
	if n := len(packages) - len(shown); n > 0 {
		list += fmt.Sprintf(", and %d more", n)
	}
	return Finding{
		Rule: RuleWideFunction, Severity: string(SeverityInfo),
		ID:      file + "#" + name,
		Message: fmt.Sprintf("%s:%d %s calls functions of %d packages (limit %d): %s", file, line, name, len(packages), limit, list),
		Data:    map[string]any{"file": file, "line": line, "name": name, "packages": packages},
	}
}
