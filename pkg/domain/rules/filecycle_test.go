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
