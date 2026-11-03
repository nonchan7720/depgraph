package hook

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

// Limits はコンテキストを溢れさせないための上限。
type Limits struct {
	// ErrorExamples / OtherExamples はルールごとに載せる代表例の件数。
	ErrorExamples int
	OtherExamples int
	// DiffItems は差分の New / Resolved それぞれに載せる件数。
	DiffItems int
	// MaxChars は出力全体の上限（Claude Code の hook 出力上限 10,000 文字より十分小さく）。
	MaxChars int
}

// DefaultLimits は既定の上限。
func DefaultLimits() Limits {
	return Limits{ErrorExamples: 10, OtherExamples: 5, DiffItems: 20, MaxChars: 4000}
}

var ruleOrder = map[string]int{"import-cycle": 0, "layer-violation": 1, "unresolved-import": 2, "god-package": 3, "duplicate-symbol": 4}

var ruleHints = map[string]string{
	"import-cycle":      "must fix; do not add imports that close a cycle",
	"layer-violation":   "must fix; respect layer boundaries",
	"unresolved-import": "import only what exists: check the path or package exists, or configure the alias / module",
	"god-package":       "avoid adding more dependents",
	"duplicate-symbol":  "reuse or consolidate instead of re-implementing",
}

func severityRank(s string) int {
	switch s {
	case "error":
		return 0
	case "warn":
		return 1
	case "info":
		return 2
	}
	return 3
}

func ruleRank(r string) int {
	if n, ok := ruleOrder[r]; ok {
		return n
	}
	return len(ruleOrder)
}

// sortFindings は severity → ルール → メッセージの順に並べた新しいスライスを返す。
func sortFindings(fs []rules.Finding) []rules.Finding {
	out := append([]rules.Finding(nil), fs...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if x, y := severityRank(a.Severity), severityRank(b.Severity); x != y {
			return x < y
		}
		if x, y := ruleRank(a.Rule), ruleRank(b.Rule); x != y {
			return x < y
		}
		if a.Rule != b.Rule {
			return a.Rule < b.Rule
		}
		return a.Message < b.Message
	})
	return out
}

// countLine は "1 error, 51 warn" のような件数行を作る。
func countLine(fs []rules.Finding) string {
	n := map[string]int{}
	var sevs []string
	for _, f := range fs {
		if n[f.Severity] == 0 {
			sevs = append(sevs, f.Severity)
		}
		n[f.Severity]++
	}
	if len(sevs) == 0 {
		return "0 findings"
	}
	sort.Slice(sevs, func(i, j int) bool { return severityRank(sevs[i]) < severityRank(sevs[j]) })
	parts := make([]string, 0, len(sevs))
	for _, s := range sevs {
		parts = append(parts, fmt.Sprintf("%d %s", n[s], s))
	}
	return strings.Join(parts, ", ")
}

// builder は MaxChars を超えないように行を積み、超えたら打ち切り行を足す。
type builder struct {
	b         strings.Builder
	max       int
	truncated bool
}

const truncNote = "... (truncated; run `depgraph check` or open the depgraph UI for the full list)\n"

func (w *builder) line(s string) {
	if w.truncated {
		return
	}
	if w.b.Len()+len(s)+1+len(truncNote) > w.max {
		w.truncated = true
		w.b.WriteString(truncNote)
		return
	}
	w.b.WriteString(s)
	w.b.WriteByte('\n')
}

// Summarize はセッション開始時に注入する全体の要約を作る。
// error を先に、件数が多いルールは件数と代表例だけに圧縮し、全体を MaxChars に収める。
func Summarize(stats graph.Stats, findings []rules.Finding, lim Limits) string {
	w := &builder{max: lim.MaxChars}
	w.line("## depgraph: dependency analysis of this repository")
	w.line(fmt.Sprintf("%d packages, %d files, %d symbols, %d package-level import edges.",
		stats.Packages, stats.Files, stats.Symbols, stats.Imports))
	if len(findings) == 0 {
		w.line("No architectural violations detected. Keep it that way: no import cycles, respect layer boundaries, reuse existing functions.")
		return w.b.String()
	}
	w.line(fmt.Sprintf("Findings: %s. Fix errors first and do not add code that makes these worse.", countLine(findings)))

	sorted := sortFindings(findings)
	for i := 0; i < len(sorted); {
		j := i
		for j < len(sorted) && sorted[j].Rule == sorted[i].Rule && sorted[j].Severity == sorted[i].Severity {
			j++
		}
		group := sorted[i:j]
		limit := lim.OtherExamples
		if group[0].Severity == "error" {
			limit = lim.ErrorExamples
		}
		head := fmt.Sprintf("### [%s] %s (%d", group[0].Severity, group[0].Rule, len(group))
		if len(group) > limit {
			head += fmt.Sprintf(", showing %d", limit)
		}
		head += ")"
		if h := ruleHints[group[0].Rule]; h != "" {
			head += " - " + h
		}
		w.line(head)
		for k, f := range group {
			if k == limit {
				w.line(fmt.Sprintf("- ... and %d more", len(group)-limit))
				break
			}
			w.line("- " + f.Message)
		}
		i = j
	}
	return w.b.String()
}

// RenderDiff は前回注入時からの差分を作る。変化が無ければ ""。
func RenderDiff(added, removed, cur []rules.Finding, lim Limits) string {
	if len(added) == 0 && len(removed) == 0 {
		return ""
	}
	w := &builder{max: lim.MaxChars}
	w.line("## depgraph: findings changed since the last prompt")
	w.line(fmt.Sprintf("Now: %s.", countLine(cur)))
	section := func(title string, fs []rules.Finding) {
		if len(fs) == 0 {
			return
		}
		w.line(fmt.Sprintf("%s (%d):", title, len(fs)))
		for k, f := range sortFindings(fs) {
			if k == lim.DiffItems {
				w.line(fmt.Sprintf("- ... and %d more", len(fs)-lim.DiffItems))
				break
			}
			w.line(fmt.Sprintf("- [%s] %s: %s", f.Severity, f.Rule, f.Message))
		}
	}
	section("New", added)
	section("Resolved", removed)
	return w.b.String()
}
