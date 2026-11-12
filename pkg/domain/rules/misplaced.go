package rules

import (
	"fmt"
	"path"
	"strings"
)

// RuleMisplacedFunction: a function that uses nothing of the package it is
// declared in and several names of exactly one other package of the
// repository: its code is about that package, and it lives where it was
// written, not where it belongs. Only the whole project shows what each
// function uses of which package.
//
// Not reported: a method (it belongs to its type), a function that uses a
// second package of the repository (it coordinates them) and one with a
// `depgraph:ignore misplaced-function` comment.
const RuleMisplacedFunction = "misplaced-function"

// DefaultMisplacedNames is how many names of the other package a function
// must use to be reported.
const DefaultMisplacedNames = 3

func init() {
	builtin = append(builtin, Rule{Name: RuleMisplacedFunction, Severity: SeverityInfo,
		Description: "a function that uses nothing of its own package and minNames or more names of one other package",
		Options:     []string{"minNames"}})
}

// MisplacedNamesLimit is how many names of the other package a function
// must use to be reported.
func (c Config) MisplacedNamesLimit() int {
	if c.MinMisplacedNames > 0 {
		return c.MinMisplacedNames
	}
	return DefaultMisplacedNames
}

// FuncUse is what one function uses of the repository.
type FuncUse struct {
	// Own counts the names of its own package it uses.
	Own int
	// Other: package -> the names of it the function uses.
	Other map[string][]string
	// Method: a method of a type or class.
	Method bool
}

// Misplaced returns the package the function belongs to, by what it uses:
// the only other package it uses, when it uses minNames or more names of it
// and nothing of its own. "" otherwise.
func Misplaced(f FuncUse, minNames int) string {
	if f.Method || f.Own > 0 || len(f.Other) != 1 {
		return ""
	}
	for pkg, names := range f.Other {
		if len(names) >= minNames {
			return pkg
		}
	}
	return ""
}

// MisplacedFunction reports the function name at file:line, which uses
// names (sorted) of package where and nothing of its own. Its identity is
// file and name.
func MisplacedFunction(file string, line int, name, where string, names []string) Finding {
	return Finding{
		Rule: RuleMisplacedFunction, Severity: string(SeverityInfo),
		ID: file + "#" + name,
		Message: fmt.Sprintf("%s:%d %s uses nothing of %s and %d names of %s (%s): it belongs there",
			file, line, name, path.Dir(file), len(names), where, strings.Join(names, ", ")),
		Data: map[string]any{"file": file, "line": line, "name": name, "package": where, "names": names},
	}
}
