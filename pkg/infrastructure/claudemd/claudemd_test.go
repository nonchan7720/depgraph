package claudemd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
	"github.com/nonchan7720/depgraph/pkg/domain/rules"
)

func TestUpdatePreservesHandwrittenContent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "CLAUDE.md")

	// 1. file does not exist -> created with just the section
	if err := Update(p, "first\n"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "first") {
		t.Fatalf("got %q", b)
	}

	// 2. user adds content around it
	if err := os.WriteFile(p, []byte("# My project\n\nhand written\n\n"+string(b)+"\nmore hand written\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 3. update replaces only the managed section
	if err := Update(p, "second\n"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(p)
	s := string(b)
	for _, want := range []string{"# My project", "hand written", "more hand written", "second"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
	if strings.Contains(s, "first") {
		t.Errorf("old section still present:\n%s", s)
	}
	if strings.Count(s, startMarker) != 1 || strings.Count(s, endMarker) != 1 {
		t.Errorf("markers duplicated:\n%s", s)
	}
}

func TestWriter(t *testing.T) {
	p := filepath.Join(t.TempDir(), "CLAUDE.md")
	w := Writer{Path: p}
	err := w.Write(graph.Stats{Packages: 3}, []rules.Finding{{Rule: "layer-violation", Severity: "error", Message: "a -> b"}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "a -> b") || !strings.Contains(string(b), startMarker) {
		t.Errorf("managed section not written: %s", b)
	}
}

// Every finding reaches CLAUDE.md: rules without a fixed title (new or
// configured ones) are listed after the known ones, not dropped.
func TestRenderListsEveryRule(t *testing.T) {
	out := Render(graph.Stats{}, []rules.Finding{
		{Rule: "zz-custom", Severity: "warn", Message: "custom finding"},
		{Rule: "unresolved-import", Severity: "warn", Message: `src/a.ts imports "@/x", which does not resolve`},
		{Rule: "import-cycle", Severity: "error", Message: "a -> b -> a"},
	})
	for _, want := range []string{"### Unresolved imports", `src/a.ts imports "@/x"`, "### zz-custom", "custom finding", "a -> b -> a"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if i, j := strings.Index(out, "Import cycles"), strings.Index(out, "### zz-custom"); i < 0 || j < i {
		t.Errorf("known rules come first:\n%s", out)
	}
}
