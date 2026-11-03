package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

func TestFileChecker(t *testing.T) {
	fg := &graph.FileGraph{Path: "a/x.go", Package: "a", Imports: []graph.Import{
		{Spec: "m/b", Target: "b"},
		{Spec: "m/b/v2", Target: "b"}, // same package twice: one lookup
		{Spec: "m/a", Target: "a"},    // own package: no cycle
		{Spec: "fmt", Target: "ext:fmt", External: true},
		{Spec: "m/gone", Unresolved: true, Reason: "no package"},
	}}
	var lookups []string
	c := fileChecker{
		supports: func(string) bool { return true },
		parse:    func(context.Context, string, string) (*graph.FileGraph, error) { return fg, nil },
		importPath: func(_ context.Context, from, to string) ([]string, error) {
			lookups = append(lookups, from+">"+to)
			if from == "b" && to == "a" {
				return []string{"b", "c", "a"}, nil
			}
			return nil, nil
		},
	}
	got, err := c.CheckFile(context.Background(), "/r", "a/x.go")
	if err != nil {
		t.Fatal(err)
	}
	want := []rules.Finding{
		rules.UnresolvedImport("a/x.go", "m/gone", "no package"),
		rules.ImportCycle([]string{"a", "b", "c", "a"}),
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("findings = %v\nwant %v", got, want)
	}
	if fmt.Sprint(lookups) != "[b>a]" {
		t.Errorf("lookups = %v", lookups)
	}

	// without the database (importPath nil) unresolved imports are still found
	c.importPath = nil
	if got, err := c.CheckFile(context.Background(), "/r", "a/x.go"); err != nil || len(got) != 1 || got[0].Rule != "unresolved-import" {
		t.Errorf("no db: %v, %v", got, err)
	}
	// a deleted file, an unsupported or ignored file: nothing
	c.parse = func(context.Context, string, string) (*graph.FileGraph, error) { return nil, os.ErrNotExist }
	if got, err := c.CheckFile(context.Background(), "/r", "a/x.go"); err != nil || got != nil {
		t.Errorf("deleted: %v, %v", got, err)
	}
	c.supports = func(string) bool { return false }
	if got, err := c.CheckFile(context.Background(), "/r", "README.md"); err != nil || got != nil {
		t.Errorf("unsupported: %v, %v", got, err)
	}
	c.supports = func(string) bool { return true }
	if got, _ := c.CheckFile(context.Background(), "/r", "node_modules/x/a.js"); got != nil {
		t.Errorf("skipped path: %v", got)
	}
	// a failing lookup is reported (the hook prints nothing on error)
	c.parse = func(context.Context, string, string) (*graph.FileGraph, error) { return fg, nil }
	c.importPath = func(context.Context, string, string) ([]string, error) { return nil, errors.New("db down") }
	if _, err := c.CheckFile(context.Background(), "/r", "a/x.go"); err == nil {
		t.Error("lookup error must surface")
	}
}
