package rules

import (
	"reflect"
	"testing"
)

// For every import on a cycle, the shortest cycle through it is reported
// once, starting at its smallest file; imports on no cycle are not.
func TestFileCycles(t *testing.T) {
	got := FileCycles([][2]string{
		{"src/b.ts", "src/a.ts"}, {"src/a.ts", "src/b.ts"}, // a pair
		{"src/a.ts", "src/c.ts"},                                                     // a leaf: no cycle
		{"app/x.py", "app/y.py"}, {"app/y.py", "app/z.py"}, {"app/z.py", "app/x.py"}, // a ring of three
		{"app/y.py", "app/x.py"}, // and a pair inside it
		{"src/a.ts", "src/a.ts"}, // a file importing itself is not a cycle
	})
	want := [][]string{
		{"app/x.py", "app/y.py", "app/x.py"},
		{"src/a.ts", "src/b.ts", "src/a.ts"},
		{"app/x.py", "app/y.py", "app/z.py", "app/x.py"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FileCycles =\n %v, want\n %v", got, want)
	}
}

// A cycle that only closes through a deferred import is told apart from one
// that exists when the files are loaded, and can be left out.
func TestFileCyclesDeferred(t *testing.T) {
	loads := []FileLoad{
		{From: "app/a.py", To: "app/b.py"}, {From: "app/b.py", To: "app/a.py"}, // at load time
		{From: "app/x.py", To: "app/y.py"}, {From: "app/y.py", To: "app/x.py", Deferred: true},
		{From: "app/y.py", To: "app/z.py", Deferred: true}, // on no cycle
	}
	var got []string
	for _, f := range FileCycleFindings(loads, true) {
		got = append(got, f.Message)
	}
	want := []string{
		"app/a.py -> app/b.py -> app/a.py",
		"app/x.py -> app/y.py -> app/x.py (hidden by an import inside a function of app/y.py)",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("findings =\n %q, want\n %q", got, want)
	}
	if got := FileCycleFindings(loads, false); len(got) != 1 || got[0].Message != want[0] {
		t.Errorf("without deferred imports = %+v", got)
	}
}

// A cycle longer than fileCycleMax files is not searched for.
func TestFileCyclesIsBounded(t *testing.T) {
	var edges [][2]string
	names := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}
	for i, n := range names {
		edges = append(edges, [2]string{n, names[(i+1)%len(names)]})
	}
	if got := FileCycles(edges); len(got) != 0 {
		t.Errorf("a ring of 9 files: %v", got)
	}
	if got := FileCycles(edges[:8:8]); len(got) != 0 {
		t.Errorf("an open chain: %v", got)
	}
	ring8 := append(edges[:7:7], [2]string{"h", "a"})
	if got := FileCycles(ring8); len(got) != 1 || len(got[0]) != 9 {
		t.Errorf("a ring of 8 files: %v", got)
	}
}

func TestFileCycleFinding(t *testing.T) {
	f := FileCycle([]string{"src/a.ts", "src/b.ts", "src/a.ts"})
	if f.Rule != RuleFileCycle || f.Severity != "warn" || f.Message != "src/a.ts -> src/b.ts -> src/a.ts" {
		t.Errorf("finding = %+v", f)
	}
	if r, ok := Lookup(RuleFileCycle); !ok || r.Severity != SeverityWarn {
		t.Errorf("rule not registered: %+v", r)
	}
}
