package treesitter

import (
	"context"
	"testing"
)

// `export const { a, b: c, ...rest } = x` declares a, c and rest, each at its
// own position (not one symbol named after the pattern text).
func TestDestructuredDeclarations(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "a.ts", "export const { setA, b: renamed, ...rest } = slice.actions;\nconst [first, , third = 1] = list;\n")
	r := New()
	t.Cleanup(r.Close)
	fg, err := r.ResolveFile(context.Background(), root, "a.ts")
	if err != nil {
		t.Fatal(err)
	}
	type sym struct {
		name     string
		exported bool
		col      int
	}
	var got []sym
	for _, s := range fg.Symbols {
		got = append(got, sym{s.Name, s.Exported, s.Col})
	}
	want := []sym{{"setA", true, 15}, {"renamed", true, 24}, {"rest", true, 36}, {"first", false, 7}, {"third", false, 16}}
	if len(got) != len(want) {
		t.Fatalf("symbols = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("symbol %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}
