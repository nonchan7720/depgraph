package cmd

import (
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
