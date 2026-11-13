package rules

import "fmt"

// RuleUnusedDependency: a dependency a manifest declares (a direct `require`
// of go.mod; dependencies, devDependencies and optionalDependencies of
// package.json) that no code of its module imports and nothing else in it
// names. Dependencies pile up when code is removed or rewritten and the
// manifest is not: they are installed, audited and updated for nothing. A
// compiler sees the imports of one file, never the manifest.
//
// The severity says how sure the finding is. A dependency nothing imports,
// nothing names and nothing can load on its own is certain: it takes the
// rule's severity (error). Where a use that is not an import cannot be
// ruled out (an add-on its host loads by name, type declarations, a
// package that is not installed so that its binaries and peers could not be
// read), the finding says why and is a level lower (warn).
const RuleUnusedDependency = "unused-dependency"

func init() {
	builtin = append(builtin, Rule{Name: RuleUnusedDependency, Severity: SeverityError,
		Description: "a dependency a manifest declares that nothing imports or names (error when certain, a level lower when a use that is not an import cannot be ruled out)",
		Options:     []string{"ignore"}})
}

// UnusedDependency reports the dependency name declared at manifest:line
// (line 0: not known). doubt says why a use cannot be ruled out ("" when it
// can: the finding is certain). Its identity is the manifest and the name.
func UnusedDependency(cfg Config, manifest string, line int, name, doubt string) Finding {
	at := manifest
	if line > 0 {
		at = fmt.Sprintf("%s:%d", manifest, line)
	}
	sev := cfg.Severity(RuleUnusedDependency)
	msg := fmt.Sprintf("%s %s is declared but nothing imports it or names it", at, name)
	data := map[string]any{"manifest": manifest, "line": line, "name": name}
	if doubt != "" {
		sev = sev.doubtful()
		msg += " (not certain: " + doubt + ")"
		data["doubt"] = doubt
	}
	return Finding{Rule: RuleUnusedDependency, Severity: string(sev), ID: manifest + "#" + name, Message: msg, Data: data}
}

// doubtful is the severity of a finding that is not certain, for a rule
// whose certain findings are s: a level lower, and warn at most.
func (s Severity) doubtful() Severity {
	if s == SeverityError {
		return SeverityWarn
	}
	return SeverityInfo
}

// DependencyIgnoreRegexes compiles IgnoreDependencies (NamePattern over
// dependency names) into the regular expressions the store filters with.
func (c Config) DependencyIgnoreRegexes() ([]string, error) {
	return nameRegexes(RuleUnusedDependency, c.IgnoreDependencies)
}
