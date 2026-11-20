package rules

import "strings"

// RuleFileCycle: files of one directory that load each other (TypeScript /
// JavaScript modules, Python modules). import-cycle sees directories, so a
// cycle inside one is invisible to it; no compiler reports either. Only
// imports that load a module count: one erased with the types (import type,
// `if TYPE_CHECKING:`) or made on demand (import()) closes no cycle.
const RuleFileCycle = "file-cycle"

func init() {
	builtin = append(builtin, Rule{Name: RuleFileCycle, Severity: SeverityWarn,
		Description: "import cycle between the files of one directory (TypeScript / JavaScript, Python)",
		Options:     []string{"deferred"}})
}

// FileCycle reports the cycle files (the first file repeated at the end).
func FileCycle(files []string) Finding {
	return Finding{
		Rule: RuleFileCycle, Severity: string(SeverityWarn),
		Message: strings.Join(files, " -> "),
		Data:    map[string]any{"files": files},
	}
}
