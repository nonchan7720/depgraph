package rules

import (
	"fmt"
	"strings"
)

// Unused code at project level: what only the whole repository shows, and
// no compiler, type checker or linter of the project reports (those see an
// unused local / unexported name, never an export nothing imports).
const (
	// RuleUnusedExport: an exported declaration nothing in the repository uses.
	RuleUnusedExport = "unused-export"
	// RuleUnreachableFile: a TS/JS/Python/Ruby file no entry point reaches through imports.
	RuleUnreachableFile = "unreachable-file"
	// RuleUnreachablePackage: a Go package no main package, test or public
	// package of a library module reaches through imports.
	RuleUnreachablePackage = "unreachable-package"
)

// UnusedExport reports the exported declaration name (kind) at file:line
// that nothing in the project uses, not even its own file. Its identity is
// file and name, not the line.
func UnusedExport(file string, line int, kind, name string) Finding {
	return Finding{
		Rule: RuleUnusedExport, Severity: "warn",
		ID:      file + "#" + name,
		Message: fmt.Sprintf("%s:%d %s %s is exported but nothing in the project uses it", file, line, kind, name),
		Data:    map[string]any{"file": file, "line": line, "kind": kind, "name": name},
	}
}

// UnreachableFile reports file, which no entry point reaches, together with
// the files only it leads to (dead with it). cycle: something imports file,
// but only files that are unreachable themselves.
func UnreachableFile(file string, only []string, cycle bool) Finding {
	return Finding{
		Rule: RuleUnreachableFile, Severity: "warn", ID: file,
		Message: unreachableMessage(file, "file", only, cycle),
		Data:    map[string]any{"file": file, "only": only},
	}
}

// UnreachablePackage is UnreachableFile for a Go package (directory).
func UnreachablePackage(pkg string, only []string, cycle bool) Finding {
	return Finding{
		Rule: RuleUnreachablePackage, Severity: "warn", ID: pkg,
		Message: unreachableMessage(pkg, "package", only, cycle),
		Data:    map[string]any{"package": pkg, "only": only},
	}
}

const unreachableListed = 5

func unreachableMessage(what, noun string, only []string, cycle bool) string {
	var b strings.Builder
	b.WriteString(what)
	if cycle {
		fmt.Fprintf(&b, " is imported only by files no entry point reaches")
	} else {
		fmt.Fprintf(&b, ": no entry point reaches this %s (nothing imports it)", noun)
	}
	if len(only) > 0 {
		names := only
		if len(names) > unreachableListed {
			names = names[:unreachableListed]
		}
		plural := "s"
		if len(only) == 1 {
			plural = ""
		}
		fmt.Fprintf(&b, "; %d more %s%s only reachable through it: %s", len(only), noun, plural, strings.Join(names, ", "))
		if n := len(only) - len(names); n > 0 {
			fmt.Fprintf(&b, " (+%d)", n)
		}
	}
	return b.String()
}

// The unused-code rules share their options: entries (UnusedEntries) and
// ignore (UnusedIgnore), path globs (see Glob) over files and Go package
// directories.
func init() {
	opts := []string{"entries", "ignore"}
	builtin = append(builtin,
		Rule{Name: RuleUnusedExport, Severity: SeverityWarn, Description: "an exported declaration nothing in the project uses", Options: opts},
		Rule{Name: RuleUnreachableFile, Severity: SeverityWarn, Description: "a TS/JS/Python/Ruby file no entry point reaches", Options: opts},
		Rule{Name: RuleUnreachablePackage, Severity: SeverityWarn, Description: "a Go package no entry point reaches", Options: opts},
	)
}

// IsUnusedRule reports whether rule is one of the unused-code rules.
func IsUnusedRule(rule string) bool {
	return rule == RuleUnusedExport || rule == RuleUnreachableFile || rule == RuleUnreachablePackage
}

// UnusedGlobs compiles UnusedEntries and UnusedIgnore.
func (c Config) UnusedGlobs() (entries, ignore []Glob, err error) {
	if entries, err = compileGlobs(c.UnusedEntries); err != nil {
		return nil, nil, fmt.Errorf("unused entries: %w", err)
	}
	if ignore, err = compileGlobs(c.UnusedIgnore); err != nil {
		return nil, nil, fmt.Errorf("unused ignore: %w", err)
	}
	return entries, ignore, nil
}

// MatchAny reports whether any of gs matches key.
func MatchAny(gs []Glob, key string) bool { return matchAny(gs, key) }
