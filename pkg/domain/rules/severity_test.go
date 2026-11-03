package rules

import "testing"

func TestSeverityAtLeast(t *testing.T) {
	tests := []struct {
		s, min Severity
		want   bool
	}{
		{SeverityError, SeverityWarn, true},
		{SeverityWarn, SeverityWarn, true},
		{SeverityInfo, SeverityWarn, false},
		{SeverityWarn, SeverityError, false},
		{SeverityInfo, SeverityInfo, true},
		{"bogus", SeverityInfo, false},
	}
	for _, tt := range tests {
		if got := tt.s.AtLeast(tt.min); got != tt.want {
			t.Errorf("%q.AtLeast(%q) = %v, want %v", tt.s, tt.min, got, tt.want)
		}
	}
}

func TestNewSince(t *testing.T) {
	base := []Finding{
		{Rule: RuleGodPackage, Severity: "warn", ID: "pkg/a", Message: "pkg/a is imported by 10"},
	}
	cur := []Finding{
		// same finding, message changed (fan-in grew): not new
		{Rule: RuleGodPackage, Severity: "warn", ID: "pkg/a", Message: "pkg/a is imported by 11"},
		{Rule: RuleImportCycle, Severity: "error", Message: "a -> b -> a"},
		{Rule: RuleUnusedExport, Severity: "warn", Message: "F in x.go"},
		{Rule: "custom", Severity: "info", Message: "note"},
	}
	got := NewSince(base, cur, SeverityWarn)
	if len(got) != 2 || got[0].Rule != RuleImportCycle || got[1].Rule != RuleUnusedExport {
		t.Fatalf("NewSince = %+v, want the cycle then the unused export (most severe first)", got)
	}
	if got := NewSince(base, cur, SeverityError); len(got) != 1 || got[0].Rule != RuleImportCycle {
		t.Errorf("NewSince(error) = %+v", got)
	}
}
