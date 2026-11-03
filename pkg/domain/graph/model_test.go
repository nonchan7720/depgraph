package graph

import (
	"reflect"
	"testing"
)

func TestStalePaths(t *testing.T) {
	existing := []string{"a.go", "old/gone.go", "testdata/x.go", "b.go"}
	seen := map[string]struct{}{"a.go": {}, "b.go": {}, "new.go": {}}
	got := StalePaths(existing, seen)
	want := []string{"old/gone.go", "testdata/x.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("StalePaths = %v, want %v", got, want)
	}
	if got := StalePaths(nil, seen); len(got) != 0 {
		t.Errorf("no existing files -> nothing stale, got %v", got)
	}
}

func TestPackageKey(t *testing.T) {
	cases := map[string]string{
		"main.go":                   ".",
		"handler/user.go":           "handler",
		"pkg/domain/graph/model.go": "pkg/domain/graph",
	}
	for in, want := range cases {
		if got := PackageKey(in); got != want {
			t.Errorf("PackageKey(%q) = %q, want %q", in, got, want)
		}
	}
}
