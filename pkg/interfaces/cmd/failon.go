package cmd

import (
	"fmt"

	"github.com/spf13/pflag"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
	"github.com/nonchan7720/depgraph/pkg/domain/view"
	"github.com/nonchan7720/depgraph/pkg/services/analysis"
)

// addFailOnFlag registers --fail-on of scan / check (CI): exit 1 when the
// evaluation has findings of at least that severity. Not given, they never
// fail on findings.
func addFailOnFlag(fs *pflag.FlagSet, failOn *string) {
	fs.StringVar(failOn, "fail-on", "", "exit 1 when there are findings of at least this severity (error, warn, info; default: never)")
}

// parseFailOn checks --fail-on before anything is started.
func parseFailOn(s string) (rules.Severity, error) {
	floor := rules.Severity(s)
	if s != "" && !floor.Valid() {
		return "", fmt.Errorf("--fail-on %q: want error, warn or info", s)
	}
	return floor, nil
}

// failOnRecorder is the report writer of scan / check: it keeps the findings
// of the last evaluation for --fail-on and forwards them to next (the
// CLAUDE.md writer; nil when disabled).
type failOnRecorder struct {
	next     analysis.ReportWriter
	findings []rules.Finding
}

func newFailOnRecorder(o *options) *failOnRecorder {
	return &failOnRecorder{next: reportWriter(o)}
}

func (r *failOnRecorder) Write(stats view.Stats, findings []rules.Finding) error {
	r.findings = findings
	if r.next == nil {
		return nil
	}
	return r.next.Write(stats, findings)
}

// result is the error of --fail-on floor: the number of findings of at least
// floor, or nil when there are none or floor is empty.
func (r *failOnRecorder) result(floor rules.Severity) error {
	if floor == "" {
		return nil
	}
	n := 0
	for _, f := range r.findings {
		if rules.Severity(f.Severity).AtLeast(floor) {
			n++
		}
	}
	if n == 0 {
		return nil
	}
	return fmt.Errorf("%d finding(s) of at least %s (--fail-on %s)", n, floor, floor)
}
