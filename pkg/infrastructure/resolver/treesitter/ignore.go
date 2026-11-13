package treesitter

import (
	"regexp"
	"sort"
	"strings"

	gts "github.com/odvcencio/gotreesitter"
)

// `depgraph:ignore <rule> [<rule>...]` in a comment directly above a
// function (no blank line in between) or inside it exempts that function
// from the function-level rules it names (graph.FuncCalls.Ignore). Text
// after the rule names is the reason, for the reader:
//
//	// depgraph:ignore wide-function (the composition root)
//	func Wire() { ... }
//
// A comment without a rule name exempts nothing: what is ignored is always
// written down.
var ignoreRe = regexp.MustCompile(`depgraph:ignore((?:[ \t,]+[a-z][a-z-]*[a-z])+)`)

// ignoredRules are the rules the comments of the declaration n name, sorted.
func (x *extractor) ignoredRules(n *gts.Node) []string {
	set := map[string]bool{}
	add := func(c *gts.Node) {
		for _, m := range ignoreRe.FindAllStringSubmatch(x.text(c), -1) {
			for _, r := range strings.FieldsFunc(m[1], func(r rune) bool { return r == ' ' || r == '\t' || r == ',' }) {
				set[r] = true
			}
		}
	}
	// inside the declaration
	x.walk(n, func(c *gts.Node) bool {
		if x.isComment(c) {
			add(c)
			return false
		}
		return true
	})
	// directly above it: `export function f` hangs under its export statement
	at := n
	if p := n.Parent(); p != nil && x.typ(p) == "export_statement" {
		at = p
	}
	for c := at.PrevSibling(); c != nil && x.isComment(c) && c.EndPoint().Row+1 >= at.StartPoint().Row; c = c.PrevSibling() {
		add(c)
		at = c
	}
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for r := range set {
		out = append(out, r)
	}
	sort.Strings(out)
	return out
}

func (x *extractor) isComment(c *gts.Node) bool { return strings.Contains(x.typ(c), "comment") }
