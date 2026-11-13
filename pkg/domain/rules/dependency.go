package rules

import (
	"fmt"
	"strings"
)

// RuleUnusedDependency: a dependency a manifest declares (a direct `require`
// of go.mod; dependencies, devDependencies and optionalDependencies of
// package.json; pyproject.toml and requirements files; a Gemfile) that no
// code imports and nothing else names. A Python distribution or a gem whose
// import names are not known (it is not installed where depgraph looks) is
// not checked. Dependencies pile up when code is removed or rewritten and the
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

// DependencyIgnore is one entry of unused-dependency's ignore: the
// dependencies whose name matches Name, of the Ecosystems and of the
// manifests matching Manifest when those are given. Name and Manifest are
// regular expressions that mean the same in Go and in the store.
type DependencyIgnore struct {
	Ecosystems []string // nil: every ecosystem
	Manifest   string   // "": every manifest
	Name       string
}

// dependencyLanguages are the words an ignore entry names a language by,
// and the ecosystem of its manifests.
var dependencyLanguages = map[string]string{
	"go": "go", "javascript": "npm", "typescript": "npm", "npm": "npm",
	"python": "pypi", "pypi": "pypi", "ruby": "gem", "gem": "gem",
}

// DependencyIgnores compiles IgnoreDependencies. An entry is a name pattern
// (see NamePattern: "tslib", "@types/*"), alone or after what it applies to
// and a colon: a language ("ruby:pg", "python:*") or a manifest, as a path
// glob (see Glob: "web/package.json:lodash", "services/**/requirements.txt:*").
func (c Config) DependencyIgnores() ([]DependencyIgnore, error) {
	out := make([]DependencyIgnore, 0, len(c.IgnoreDependencies))
	for _, entry := range c.IgnoreDependencies {
		fail := func(err error) ([]DependencyIgnore, error) {
			return nil, fmt.Errorf("rule %s: ignore %q: %w", RuleUnusedDependency, entry, err)
		}
		var ig DependencyIgnore
		name := entry
		if scope, rest, ok := strings.Cut(entry, ":"); ok {
			name = rest
			if eco, ok := dependencyLanguages[scope]; ok {
				ig.Ecosystems = []string{eco}
			} else {
				g, err := CompileGlob(scope)
				if err != nil {
					return fail(err)
				}
				ig.Manifest = g.Regex()
			}
		}
		p, err := CompileNamePattern(name)
		if err != nil {
			return fail(err)
		}
		ig.Name = p.regex
		out = append(out, ig)
	}
	return out, nil
}
