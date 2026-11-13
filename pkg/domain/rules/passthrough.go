package rules

import (
	"fmt"
	"strings"
)

// RulePassThrough: a chain of at least two functions that only pass their
// parameters on to the next (`api.Get -> service.Get -> load`): layers that
// add a name and nothing else, which is how wrappers pile up when code is
// added in front of code instead of changed. One wrapper is an API choice
// and is not reported; methods are never counted (they may be there to
// satisfy an interface).
const RulePassThrough = "pass-through"

func init() {
	builtin = append(builtin, Rule{Name: RulePassThrough, Severity: SeverityWarn,
		Description: "a chain of two or more functions that only pass their parameters on to the next"})
}

// PassThrough reports chain: its functions but the last only pass their
// arguments on. Its identity is the first function.
func PassThrough(chain []FuncNode) Finding {
	steps := make([]string, len(chain))
	ids := make([]string, len(chain))
	for i, n := range chain {
		steps[i] = n.File + " " + n.Name
		ids[i] = n.File + "#" + n.Name
	}
	names := make([]string, len(chain)-1)
	for i, n := range chain[:len(chain)-1] {
		names[i] = n.Name
	}
	who := strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	return Finding{
		Rule: RulePassThrough, Severity: string(SeverityWarn),
		ID:      ids[0],
		Message: fmt.Sprintf("%s: %s only pass their arguments on", strings.Join(steps, " -> "), who),
		Data:    map[string]any{"functions": ids},
	}
}
