package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/nonchan7720/depgraph/pkg/domain/rules"
	"github.com/nonchan7720/depgraph/pkg/domain/view"
)

func TestFailOnRecorder(t *testing.T) {
	findings := []rules.Finding{
		{Rule: rules.RuleDuplicateSymbol, Severity: string(rules.SeverityWarn), Message: "a"},
		{Rule: rules.RuleGodPackage, Severity: string(rules.SeverityInfo), Message: "b"},
	}
	tests := []struct {
		floor   string
		wantErr string // "" means pass
	}{
		{"", ""}, // not given: never fails
		{"error", ""},
		{"warn", "1 finding(s) of at least warn"},
		{"info", "2 finding(s) of at least info"},
	}
	for _, tt := range tests {
		t.Run(tt.floor, func(t *testing.T) {
			var next recordingWriter
			r := &failOnRecorder{next: &next}
			if err := r.Write(view.Stats{}, findings); err != nil {
				t.Fatal(err)
			}
			if len(next.findings) != len(findings) {
				t.Errorf("the wrapped writer must get every finding, got %d", len(next.findings))
			}
			err := r.result(rules.Severity(tt.floor))
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("result(%q) = %v, want nil", tt.floor, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("result(%q) = %v, want %q", tt.floor, err, tt.wantErr)
			}
		})
	}
	// --claude-md "": no writer to forward to
	r := &failOnRecorder{}
	if err := r.Write(view.Stats{}, findings); err != nil {
		t.Fatal(err)
	}
	if err := r.result(rules.SeverityWarn); err == nil {
		t.Error("findings must be counted without a CLAUDE.md writer")
	}
}

type recordingWriter struct{ findings []rules.Finding }

func (w *recordingWriter) Write(_ view.Stats, fs []rules.Finding) error {
	w.findings = fs
	return nil
}

// An invalid --fail-on is rejected before Neo4j is reached.
func TestFailOnInvalid(t *testing.T) {
	for _, name := range []string{"scan", "check"} {
		var out bytes.Buffer
		c := newRootCommand(&options{})
		c.SetOut(&out)
		c.SetErr(&out)
		c.SetArgs([]string{name, "--root", t.TempDir(), "--fail-on", "warning"})
		err := c.Execute()
		if err == nil || !strings.Contains(err.Error(), `--fail-on "warning": want error, warn or info`) {
			t.Errorf("%s --fail-on warning = %v, want the flag error", name, err)
		}
	}
}
