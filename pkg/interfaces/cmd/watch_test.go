package cmd

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"
)

func TestSplitChanges(t *testing.T) {
	isConfig := func(rel string) bool {
		return strings.HasSuffix(rel, "tsconfig.json") || strings.HasSuffix(rel, "__init__.py")
	}
	supports := func(rel string) bool { return strings.HasSuffix(rel, ".ts") || strings.HasSuffix(rel, ".py") }
	config, sources := splitChanges([]string{"b.ts", "tsconfig.json", "pkg/__init__.py", "a.ts", "notes.md"}, isConfig, supports)
	if want := []string{"pkg/__init__.py", "tsconfig.json"}; !reflect.DeepEqual(config, want) {
		t.Errorf("config = %v, want %v", config, want)
	}
	// __init__.py is a config file for Python resolution and a source file
	if want := []string{"a.ts", "b.ts", "pkg/__init__.py"}; !reflect.DeepEqual(sources, want) {
		t.Errorf("sources = %v, want %v", sources, want)
	}
}

// A change of go.mod / tsconfig.json drops the cached import configuration
// and rescans instead of re-analysing only the changed files.
func TestWatchRescansOnImportConfig(t *testing.T) {
	svc := &fakeService{}
	var out bytes.Buffer
	var dropped []string
	rescans := 0
	b := watchBatch{
		root: t.TempDir(), svc: svc, out: &out,
		isImportConfig:      func(rel string) bool { return rel == "tsconfig.json" },
		supports:            func(rel string) bool { return strings.HasSuffix(rel, ".ts") },
		importConfigChanged: func(rel string) { dropped = append(dropped, rel) },
		rescan:              func(context.Context) error { rescans++; return nil },
	}
	b.handle(context.Background(), []string{"a.ts", "tsconfig.json"})
	if strings.Join(dropped, ",") != "tsconfig.json" || rescans != 1 || svc.evals != 1 || len(svc.changes) != 0 {
		t.Fatalf("dropped=%v rescans=%d evals=%d changes=%v", dropped, rescans, svc.evals, svc.changes)
	}
	b.handle(context.Background(), []string{"a.ts", "notes.md"})
	if rescans != 1 || len(svc.changes) != 1 || strings.Join(svc.changes[0], ",") != "a.ts" {
		t.Fatalf("sources only: rescans=%d changes=%v", rescans, svc.changes)
	}
}
